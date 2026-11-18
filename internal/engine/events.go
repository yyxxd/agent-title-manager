package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type submission struct {
	Agent            string  `json:"agent"`
	SessionID        string  `json:"sessionId"`
	Revision         string  `json:"revision"`
	CurrentTitle     string  `json:"currentTitle"`
	TitleOrigin      string  `json:"titleOrigin"`
	Project          string  `json:"project"`
	OriginalGoal     string  `json:"originalGoal"`
	ConfirmationOnly bool    `json:"confirmationOnly"`
	OrcaID           string  `json:"orcaSessionId"`
	OrcaNativeHandle string  `json:"orcaNativeHandle"`
	OrcaTitle        *string `json:"orcaTitle"`
	OrcaOrigin       string  `json:"orcaTitleOrigin"`
	NativeOnly       bool    `json:"nativeTitleOnly"`
	Turns            []turn  `json:"turns"`
}

type turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type session struct {
	revision, title, orcaID, orcaManaged    string
	projectKey, priorRevision, priorProject string
	manual                                  bool
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
		!validString(p.CurrentTitle, 2048) || !validString(p.Project, 4096) || !validString(p.OrcaID, 256) ||
		!validString(p.OrcaNativeHandle, 4096) {
		return errors.New("会话键、版本或标题无效")
	}
	if !validOrigin(p.TitleOrigin) || !validOrigin(p.OrcaOrigin) ||
		(p.OrcaTitle != nil && (p.OrcaID == "" || !validString(*p.OrcaTitle, 2048))) ||
		(p.OrcaOrigin != "" && p.OrcaTitle == nil) || (p.OrcaNativeHandle != "" && p.OrcaID == "") {
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
	err := q.QueryRowContext(ctx, `SELECT revision,title,manual,orca_id,orca_managed,project_key FROM sessions
		WHERE agent=? AND session_id=?`, agent, id).Scan(&s.revision, &s.title, &s.manual, &s.orcaID, &s.orcaManaged, &s.projectKey)
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
		s.priorRevision, s.priorProject = s.revision, s.projectKey
		s.revision = p.Revision
		_, s.projectKey = inspectProject(p.Project)
	}
	if p.OrcaID != "" && p.OrcaID != s.orcaID {
		s.orcaID = p.OrcaID
		s.orcaManaged = ""
	}
	s.title = p.CurrentTitle
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id,orca_managed,project_key)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(agent,session_id) DO UPDATE SET
		revision=excluded.revision,title=excluded.title,manual=excluded.manual,orca_id=excluded.orca_id,
		orca_managed=excluded.orca_managed,project_key=excluded.project_key`,
		p.Agent, p.SessionID, s.revision, s.title, s.manual, s.orcaID, s.orcaManaged, s.projectKey)
	return s, err
}

func (e *Engine) insertSubmission(ctx context.Context, tx *sql.Tx, p submission, s session, payload []byte) error {
	status, syncStatus := "queued", ""
	var finalized *int64
	now := e.now().UnixMilli()
	skips, err := confirmationSkips(ctx, tx, p, s)
	if err != nil {
		return err
	}
	switch {
	case p.NativeOnly:
		status = "native"
		syncStatus = syncPolicy(p.OrcaID, p.OrcaTitle, p.OrcaOrigin, s.orcaManaged, p.CurrentTitle)
	case s.manual || payload == nil || skips > 0:
		status = "kept"
		finalized = &now
		payload = nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(id,agent,session_id,revision,native_only,base,title,status,payload,
		orca_id,orca_base,orca_origin,sync_status,created,updated,finalized,turns_hash,turn_count,confirmation_skips,native_handle)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rand.Text(), p.Agent, p.SessionID, p.Revision, p.NativeOnly, p.CurrentTitle, p.CurrentTitle,
		status, payload, p.OrcaID, p.OrcaTitle, p.OrcaOrigin, syncStatus, now, now, finalized,
		turnsHash(p.OriginalGoal, p.Turns), len(p.Turns), skips, p.OrcaNativeHandle)
	return err
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
