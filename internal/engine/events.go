package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type submission struct {
	Agent        string  `json:"agent"`
	SessionID    string  `json:"sessionId"`
	Revision     string  `json:"revision"`
	CurrentTitle string  `json:"currentTitle"`
	TitleOrigin  string  `json:"titleOrigin"`
	Project      string  `json:"project"`
	OrcaID       string  `json:"orcaSessionId"`
	OrcaTitle    *string `json:"orcaTitle"`
	OrcaOrigin   string  `json:"orcaTitleOrigin"`
	NativeOnly   bool    `json:"nativeTitleOnly"`
	Turns        []turn  `json:"turns"`
}

type turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type session struct {
	revision, title, orcaID, orcaManaged string
	manual                               bool
}

var validAgent = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

func validString(value string, max int) bool {
	return len(value) <= max && !strings.ContainsFunc(value, unicode.IsControl)
}

func validKey(agent, id string) bool {
	return validAgent.MatchString(agent) && id != "" && validString(id, 256)
}

func validOrigin(origin string) bool {
	return origin == "" || origin == "auto" || origin == "manual" || origin == "unknown"
}

func (p submission) validate() error {
	if !validKey(p.Agent, p.SessionID) || p.Revision == "" || !validString(p.Revision, 512) ||
		!validString(p.CurrentTitle, 2048) || !validString(p.Project, 512) || !validString(p.OrcaID, 256) {
		return errors.New("会话键、版本或标题无效")
	}
	if !validOrigin(p.TitleOrigin) || !validOrigin(p.OrcaOrigin) ||
		(p.OrcaTitle != nil && (p.OrcaID == "" || !validString(*p.OrcaTitle, 2048))) ||
		(p.OrcaOrigin != "" && p.OrcaTitle == nil) {
		return errors.New("标题来源或 Orca 绑定无效")
	}
	if p.NativeOnly && (strings.TrimSpace(p.CurrentTitle) == "" || len(p.Turns) > 0 || p.OrcaID == "" || p.OrcaTitle == nil) {
		return errors.New("仅同步模式需要宿主已确认的会话标题及完整 Orca 绑定")
	}
	if len(p.Turns) > 24 {
		return errors.New("最近对话最多 24 条")
	}
	for _, t := range p.Turns {
		if t.Role != "user" && t.Role != "assistant" {
			return errors.New("不支持的对话角色")
		}
	}
	return nil
}

func loadSession(ctx context.Context, q queryer, agent, id string) (session, error) {
	var s session
	err := q.QueryRowContext(ctx, `SELECT revision,title,manual,orca_id,orca_managed FROM sessions
		WHERE agent=? AND session_id=?`, agent, id).Scan(&s.revision, &s.title, &s.manual, &s.orcaID, &s.orcaManaged)
	return s, err
}

func (e *Engine) submit(ctx context.Context, p submission) (accepted, error) {
	if err := p.validate(); err != nil {
		return accepted{}, err
	}
	payload, err := makePrompt(p)
	if err != nil {
		return accepted{}, err
	}
	result, err := transact(ctx, e.db, func(tx *sql.Tx) (accepted, error) {
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events
			WHERE agent=? AND session_id=? AND revision=? AND native_only=?)`, p.Agent, p.SessionID, p.Revision, p.NativeOnly).Scan(&exists)
		if err != nil || exists {
			return accepted{exists}, err
		}
		s, err := observeSubmission(ctx, tx, p, e.now().UnixMilli())
		if err != nil {
			return accepted{}, err
		}
		return accepted{true}, e.insertSubmission(ctx, tx, p, s, payload)
	})
	if err == nil {
		signal(e.modelWake)
		signal(e.syncWake)
	}
	return result, err
}

func observeSubmission(ctx context.Context, tx *sql.Tx, p submission, now int64) (session, error) {
	s, err := loadSession(ctx, tx, p.Agent, p.SessionID)
	first := errors.Is(err, sql.ErrNoRows)
	if err != nil && !first {
		return s, err
	}
	if p.TitleOrigin == "manual" {
		s.manual = true
	}
	if first {
		s.manual = s.manual || (p.CurrentTitle != "" && p.TitleOrigin != "auto")
	} else if p.CurrentTitle != s.title {
		var inFlight bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events
			WHERE agent=? AND session_id=? AND status='claimed' AND title=?)`, p.Agent, p.SessionID, p.CurrentTitle).Scan(&inFlight)
		if err != nil {
			return s, err
		}
		if !inFlight && p.TitleOrigin != "auto" {
			s.manual = true
		}
	}
	if !p.NativeOnly || p.CurrentTitle != s.title || s.manual {
		_, err = tx.ExecContext(ctx, `UPDATE events SET status='stale',payload=NULL,updated=?
			WHERE agent=? AND session_id=? AND (status IN ('queued','candidate') OR (status='claimed' AND title<>?))`,
			now, p.Agent, p.SessionID, p.CurrentTitle)
		if err != nil {
			return s, err
		}
	}
	if !p.NativeOnly {
		s.revision = p.Revision
	}
	if p.OrcaID != "" && p.OrcaID != s.orcaID {
		s.orcaID = p.OrcaID
		s.orcaManaged = ""
	}
	s.title = p.CurrentTitle
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id,orca_managed)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(agent,session_id) DO UPDATE SET
		revision=excluded.revision,title=excluded.title,manual=excluded.manual,orca_id=excluded.orca_id,orca_managed=excluded.orca_managed`,
		p.Agent, p.SessionID, s.revision, s.title, s.manual, s.orcaID, s.orcaManaged)
	return s, err
}

func (e *Engine) insertSubmission(ctx context.Context, tx *sql.Tx, p submission, s session, payload []byte) error {
	status, syncStatus := "queued", ""
	var finalized *int64
	now := e.now().UnixMilli()
	switch {
	case p.NativeOnly:
		status = "native"
		syncStatus = syncPolicy(p.OrcaID, p.OrcaTitle, p.OrcaOrigin, s.orcaManaged, p.CurrentTitle)
	case s.manual || payload == nil:
		status = "kept"
		finalized = &now
		payload = nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO events(id,agent,session_id,revision,native_only,base,title,status,payload,
		orca_id,orca_base,orca_origin,sync_status,created,updated,finalized) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rand.Text(), p.Agent, p.SessionID, p.Revision, p.NativeOnly, p.CurrentTitle, p.CurrentTitle,
		status, payload, p.OrcaID, p.OrcaTitle, p.OrcaOrigin, syncStatus, now, now, finalized)
	return err
}

func makePrompt(p submission) ([]byte, error) {
	if p.NativeOnly {
		return nil, nil
	}
	turns := make([]turn, 0, min(10, len(p.Turns)))
	budget := 6000
	for i := len(p.Turns) - 1; i >= 0 && len(turns) < 10 && budget > 0; i-- {
		t := p.Turns[i]
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		runes := []rune(text)
		n := min(len(runes), 1600, budget)
		budget -= n
		turns = append(turns, turn{Role: t.Role, Text: string(runes[:n])})
	}
	for left, right := 0, len(turns)-1; left < right; left, right = left+1, right-1 {
		turns[left], turns[right] = turns[right], turns[left]
	}
	goal := ""
	for _, t := range turns {
		if t.Role == "user" {
			goal = t.Text
			break
		}
	}
	if goal == "" {
		return nil, nil
	}
	return json.Marshal(map[string]any{"recent_turns": turns, "original_goal": goal, "current_title": p.CurrentTitle, "project_hint": p.Project})
}

var categories = []string{"🎬", "🧩", "🔎", "📝", "📅", "🎨", "⚙️", "💬"}

func validTitle(title string, length int) bool {
	if strings.TrimSpace(title) != title || utf8.RuneCountInString(title) > length ||
		!validString(title, 256) || strings.ContainsAny(title, "|:：@\\`") {
		return false
	}
	for _, emoji := range categories {
		body, ok := strings.CutPrefix(title, emoji+" ")
		if !ok {
			continue
		}
		subject, goal, ok := strings.Cut(body, "｜")
		if !ok || subject == "" || goal == "" || strings.TrimSpace(subject) != subject ||
			strings.TrimSpace(goal) != goal || strings.Contains(goal, "｜") {
			return false
		}
		for _, r := range body {
			if unicode.Is(unicode.So, r) || r == '\uFE0F' || r == '\u200D' {
				return false
			}
		}
		return true
	}
	return false
}
