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
	Agent               string  `json:"agent"`
	SessionID           string  `json:"sessionId"`
	Revision            string  `json:"revision"`
	CurrentTitle        string  `json:"currentTitle"`
	TitleOrigin         string  `json:"titleOrigin"`
	Project             string  `json:"project"`
	OriginalGoal        string  `json:"originalGoal"`
	ConfirmationOnly    bool    `json:"confirmationOnly"`
	SubstantiveActivity bool    `json:"substantiveActivity"`
	OrcaID              string  `json:"orcaSessionId"`
	OrcaNativeHandle    string  `json:"orcaNativeHandle"`
	OrcaTitle           *string `json:"orcaTitle"`
	OrcaOrigin          string  `json:"orcaTitleOrigin"`
	NativeOnly          bool    `json:"nativeTitleOnly"`
	Turns               []turn  `json:"turns"`
	ProtectionEpoch     *int64  `json:"protectionEpoch"`
	SessionKind         string  `json:"sessionKind"`
	ReviewOnly          bool    `json:"reviewOnly"`
}

type turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type session struct {
	revision, title, orcaID, orcaManaged    string
	projectKey, priorRevision, priorProject string
	protectionReason                        string
	priorTitle                              string
	orcaUnlockedBase                        *string
	protectionEpoch                         int64
	manualChanged                           bool
	manual                                  bool
	supersedes, preserveInitial             bool
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
	if (p.ProtectionEpoch != nil && *p.ProtectionEpoch < 0) ||
		(p.SessionKind != "" && p.SessionKind != "project" && p.SessionKind != "bot") ||
		(p.ReviewOnly && (p.Agent != "codex" || p.NativeOnly || p.OrcaTitle == nil)) {
		return errors.New("会话保护版本或类型无效")
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
	err := q.QueryRowContext(ctx, `SELECT revision,title,manual,orca_id,orca_managed,project_key,protection_reason,
		protection_epoch,orca_unlocked_base FROM sessions WHERE agent=? AND session_id=?`, agent, id).
		Scan(&s.revision, &s.title, &s.manual, &s.orcaID, &s.orcaManaged, &s.projectKey, &s.protectionReason, &s.protectionEpoch, &s.orcaUnlockedBase)
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
	e.activeMu.Lock()
	defer e.activeMu.Unlock()
	var observed session
	result, err := transact(ctx, e.db, func(tx *sql.Tx) (accepted, error) {
		state, err := e.titleEligibility(ctx, tx, sessionRequest{Agent: p.Agent, SessionID: p.SessionID})
		if err != nil {
			return accepted{}, err
		}
		if (state.Locked && (!p.NativeOnly || p.TitleOrigin != "manual" || p.CurrentTitle == state.title)) ||
			(p.ProtectionEpoch != nil && *p.ProtectionEpoch != state.Epoch) {
			return accepted{Accepted: true}, nil
		}
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events
			WHERE agent=? AND session_id=? AND revision=? AND native_only=?)`, p.Agent, p.SessionID, p.Revision, p.NativeOnly).Scan(&exists)
		if err != nil || exists {
			return accepted{Accepted: exists}, err
		}
		observed, err = observeSubmission(ctx, tx, p, e.now().UnixMilli())
		if err != nil {
			return accepted{}, err
		}
		return accepted{Accepted: true}, e.insertSubmission(ctx, tx, p, observed, payload)
	})
	if err == nil {
		if observed.supersedes {
			e.cancelSuperseded(p.Agent, p.SessionID, observed.preserveInitial)
		}
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
		s.manualChanged = !first && s.title != p.CurrentTitle
		s.priorTitle = s.title
		if s.protectionReason != decisionManual {
			s.protectionEpoch++
		}
		s.manual = true
		s.protectionReason = decisionManual
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
	if s.orcaUnlockedBase != nil && p.OrcaID == s.orcaID && p.OrcaTitle != nil && *p.OrcaTitle != *s.orcaUnlockedBase {
		s.manual = true
	}
	s.supersedes = !p.NativeOnly || p.CurrentTitle != s.title || s.manual
	s.preserveInitial = !p.NativeOnly && !s.manual && s.title == "" && p.CurrentTitle == ""
	if s.supersedes {
		_, err = tx.ExecContext(ctx, `UPDATE events SET status='stale',payload=NULL,updated=?,decision_reason=?
			WHERE agent=? AND session_id=? AND (status='queued' OR
				(NOT (? AND base='') AND (status IN ('running','candidate') OR (status='claimed' AND (title<>? OR ?)))))`,
			now, s.protection(), p.Agent, p.SessionID, s.preserveInitial, p.CurrentTitle, s.manual)
		if err != nil {
			return s, err
		}
	}
	if s.protectionReason == decisionManual {
		_, err = tx.ExecContext(ctx, `UPDATE events SET sync_status='stale',updated=?
			WHERE agent=? AND session_id=? AND sync_status IN ('pending','writing','failed','protected')`, now, p.Agent, p.SessionID)
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
		s.orcaUnlockedBase = nil
	}
	s.title = p.CurrentTitle
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(agent,session_id,revision,title,manual,orca_id,orca_managed,project_key,
		protection_reason,protection_epoch,session_kind,orca_unlocked_base)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(agent,session_id) DO UPDATE SET
		revision=excluded.revision,title=excluded.title,manual=excluded.manual,orca_id=excluded.orca_id,
		orca_managed=excluded.orca_managed,project_key=excluded.project_key,protection_reason=excluded.protection_reason,
		protection_epoch=excluded.protection_epoch,
		orca_unlocked_base=excluded.orca_unlocked_base,
		session_kind=coalesce(nullif(excluded.session_kind,''),sessions.session_kind)`,
		p.Agent, p.SessionID, s.revision, s.title, s.manual, s.orcaID, s.orcaManaged, s.projectKey,
		s.protection(), s.protectionEpoch, p.SessionKind, s.orcaUnlockedBase)
	return s, err
}

func (e *Engine) insertSubmission(ctx context.Context, tx *sql.Tx, p submission, s session, payload []byte) error {
	status, syncStatus := "queued", ""
	reason := s.protection()
	var finalized *int64
	now := e.now().UnixMilli()
	skips, err := confirmationSkips(ctx, tx, p, s)
	if err != nil {
		return err
	}
	var throttled bool
	var throttleSkip int
	var hashes []string
	var goalHash string
	if !p.NativeOnly {
		hashes = hashTurnWindow(p.Turns)
		goalHash = hashText([]byte(p.OriginalGoal))
	}
	if payload != nil && skips == 0 {
		throttled, throttleSkip, err = throttleNaming(ctx, tx, p, s, now, hashes, goalHash)
		if err != nil {
			return err
		}
	}
	if reason == "" && !p.NativeOnly {
		switch {
		case p.ReviewOnly && p.OrcaTitle != nil && *p.OrcaTitle != "" && p.OrcaOrigin != "auto":
			reason = decisionOrcaProtected
			payload = nil
		case payload == nil:
			reason = decisionNoContent
		case skips > 0:
			reason = decisionConfirmation
		case throttled && throttleSkip == 0:
			reason = decisionDuplicate
		case throttled:
			reason = decisionThrottle
		}
	}
	switch {
	case s.manualChanged:
		status, finalized, payload = "manual_change", &now, nil
		if p.OrcaTitle != nil && *p.OrcaTitle != p.CurrentTitle {
			syncStatus = "blocked"
		}
	case p.NativeOnly:
		status = "native"
		syncStatus = syncPolicy(p.OrcaID, p.OrcaTitle, p.OrcaOrigin, s.orcaManaged, p.CurrentTitle)
	case s.manual || payload == nil || skips > 0 || throttled:
		status = "kept"
		finalized = &now
		payload = nil
	}
	base := p.CurrentTitle
	if s.manualChanged {
		base = s.priorTitle
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(id,agent,session_id,revision,native_only,base,title,status,payload,
		orca_id,orca_base,orca_origin,sync_status,created,updated,finalized,turns_hash,turn_count,confirmation_skips,native_handle,throttle_skips,
		turn_hashes,goal_hash,decision_reason,review_only)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rand.Text(), p.Agent, p.SessionID, p.Revision, p.NativeOnly, base, p.CurrentTitle,
		status, payload, p.OrcaID, p.OrcaTitle, p.OrcaOrigin, syncStatus, now, now, finalized,
		turnsHash(p.OriginalGoal, p.Turns), len(p.Turns), skips, p.OrcaNativeHandle, throttleSkip, strings.Join(hashes, ","), goalHash, reason, p.ReviewOnly)
	return err
}

var categories = []string{"🎬", "🧩", "🔎", "📝", "📅", "🎨", "⚙️", "💬"}

func acceptedTitleLength(preset int) int {
	if preset == shortTitleLength {
		return 20
	}
	return preset
}

func titleFailure(title string, limit int) failureReason {
	if length := utf8.RuneCountInString(title); length > limit {
		return failureReason{Code: failureTooLong, Length: &length, Limit: &limit}
	}
	if strings.TrimSpace(title) != title || !validString(title, 256) {
		return failureReason{Code: failureTitleFormat}
	}
	for _, emoji := range categories {
		body, ok := strings.CutPrefix(title, emoji+" ")
		if !ok {
			continue
		}
		subject, goal, ok := strings.Cut(body, "｜")
		if !ok || subject == "" || goal == "" || strings.TrimSpace(subject) != subject ||
			strings.TrimSpace(goal) != goal || strings.Contains(goal, "｜") {
			return failureReason{Code: failureSeparator}
		}
		if strings.ContainsAny(title, "|:：@\\`") {
			return failureReason{Code: failureTitleFormat}
		}
		for _, r := range body {
			if unicode.Is(unicode.So, r) || r == '\uFE0F' || r == '\u200D' {
				return failureReason{Code: failureTitleFormat}
			}
		}
		return failureReason{}
	}
	return failureReason{Code: failureCategory}
}
