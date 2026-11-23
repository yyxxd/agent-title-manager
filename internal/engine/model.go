package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type usage struct {
	Input  *int64 `json:"input_tokens"`
	Output *int64 `json:"output_tokens"`
	Cached *int64 `json:"cached_input_tokens"`
}

type modelResult struct {
	decision    decision
	usage       usage
	actualTier  string
	actualModel string
	status      string
	message     string
	confirmed   bool
	failure     failureCode
}

type decision struct {
	Action string `json:"action"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type modelJob struct {
	id, agent, sessionID, base string
	payload                    []byte
	settings                   Settings
	secret                     []byte
}

type attemptRequest struct {
	eventID  *string
	agent    string
	provider string
	settings Settings
	secret   []byte
	payload  []byte
	feedback *titleRetry
}

const currentModel = `s.title=e.base AND s.manual=0 AND (s.revision=e.revision OR e.base='')`

type activeModel struct {
	job    *modelJob
	cancel context.CancelFunc
}

func (e *Engine) nextModel() (*modelJob, error) {
	ctx := e.ctx
	return transact(ctx, e.db, func(tx *sql.Tx) (*modelJob, error) {
		var job modelJob
		err := tx.QueryRowContext(ctx, `SELECT q.id,q.agent,q.session_id,q.base,q.payload FROM events q
			WHERE q.status='queued' AND NOT EXISTS(SELECT 1 FROM events first
				WHERE first.agent=q.agent AND first.session_id=q.session_id AND first.native_only=0
				  AND first.base='' AND first.status IN ('candidate','claimed'))
			ORDER BY q.created,q.rowid LIMIT 1`).Scan(&job.id, &job.agent, &job.sessionID, &job.base, &job.payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		job.settings, job.secret, err = loadSettings(ctx, tx)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE events SET status='running',payload=NULL,updated=? WHERE id=?`, e.now().UnixMilli(), job.id)
		return &job, err
	})
}

func (e *Engine) processModel() (bool, error) {
	e.activeMu.Lock()
	job, err := e.nextModel()
	if err != nil || job == nil {
		e.activeMu.Unlock()
		return false, err
	}
	ctx, cancel := context.WithCancel(e.ctx)
	e.activeModel = &activeModel{job, cancel}
	e.activeMu.Unlock()
	defer func() {
		cancel()
		e.activeMu.Lock()
		e.activeModel = nil
		e.activeMu.Unlock()
	}()
	result, err := e.runModelJob(ctx, job)
	if ctx.Err() != nil && e.ctx.Err() == nil {
		result.status = "stale"
		return true, errors.Join(err, e.finishModel(job, result))
	}
	if err != nil {
		return true, err
	}
	return true, e.finishModel(job, result)
}

// 调用方持有 activeMu，事务提交与新任务登记之间才能准确取消旧请求。
func (e *Engine) cancelSuperseded(agent, sessionID string, keepInitial bool) {
	active := e.activeModel
	if active != nil && active.job.agent == agent && active.job.sessionID == sessionID &&
		(!keepInitial || active.job.base != "") {
		active.cancel()
	}
}

func (e *Engine) runModelJob(ctx context.Context, job *modelJob) (modelResult, error) {
	req := attemptRequest{eventID: &job.id, agent: job.agent, provider: job.settings.Provider,
		settings: job.settings, secret: job.secret, payload: job.payload}
	result, err := e.invoke(ctx, req)
	if err != nil || ctx.Err() != nil {
		return result, err
	}
	if req.provider == "codex" && req.settings.FallbackEnabled && result.confirmed && apiReady(req.settings) {
		req.provider = "api"
		result, err = e.invoke(ctx, req)
		if err != nil || ctx.Err() != nil {
			return result, err
		}
	}
	if req.feedback = correctionFeedback(result, job.base, job.settings.MaxTitleLength); req.feedback != nil {
		return e.invoke(ctx, req)
	}
	return result, nil
}

func apiReady(s Settings) bool { return s.APIBaseURL != "" && s.APIModel != "" }

func (e *Engine) finishModel(job *modelJob, r modelResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := e.now().UnixMilli()
	_, err := transact(ctx, e.db, func(tx *sql.Tx) (bool, error) {
		var base string
		var current, reviewOnly bool
		err := tx.QueryRowContext(ctx, `SELECT e.base,e.review_only,`+currentModel+`
			FROM events e JOIN sessions s ON e.agent=s.agent AND e.session_id=s.session_id WHERE e.id=?`, job.id).
			Scan(&base, &reviewOnly, &current)
		if err != nil {
			return false, err
		}
		title, status, finalized, reason := modelOutcome(base, current, r, job.settings.MaxTitleLength, now)
		decisionReason := ""
		if status == "kept" {
			decisionReason = keptDecision(base, r)
		}
		if status == "candidate" {
			conflict, err := titleConflict(ctx, tx, job.id, title)
			if err != nil {
				return false, err
			}
			if conflict {
				title, status, finalized = base, "kept", &now
				decisionReason = decisionCollision
			}
		}
		if status == "candidate" && reviewOnly {
			status, decisionReason = "needs_review", "conditional_write_unavailable"
		}
		_, err = tx.ExecContext(ctx, `UPDATE events SET status=?,title=?,updated=?,finalized=?,
			failure_code=?,failure_length=?,failure_limit=?,decision_reason=? WHERE id=? AND status='running'`,
			status, title, now, finalized, reason.Code, reason.Length, reason.Limit, decisionReason, job.id)
		return true, err
	})
	return err
}

func modelOutcome(base string, current bool, r modelResult, maxLength int, now int64) (string, string, *int64, failureReason) {
	switch {
	case !current || r.status == "stale":
		return base, "stale", nil, failureReason{}
	case r.status == "disabled":
		return base, "disabled", nil, failureReason{}
	case r.status == "interrupted":
		return base, "interrupted", nil, failureReason{Code: r.failure}
	case r.status != "completed":
		if r.failure == "" {
			r.failure = failureGeneric
		}
		return base, "failed", nil, failureReason{Code: r.failure}
	case r.decision.Action == "keep" || r.decision.Title == base:
		return base, "kept", &now, failureReason{}
	}
	if reason := titleFailure(r.decision.Title, acceptedTitleLength(maxLength)); reason.Code != "" {
		return base, "failed", nil, reason
	}
	return r.decision.Title, "candidate", nil, failureReason{}
}

func (e *Engine) invoke(parent context.Context, req attemptRequest) (modelResult, error) {
	ctx, cancel := context.WithTimeout(parent, 100*time.Second)
	defer cancel()
	var id int64
	var startErr error
	if ctx.Err() != nil {
		return modelResult{status: "interrupted", failure: contextFailure(ctx.Err())}, nil
	}
	if !e.attemptEnabled(ctx, req) {
		return modelResult{status: "disabled"}, nil
	}
	r := e.generate(ctx, req, func() error {
		if !e.attemptEnabled(ctx, req) {
			startErr = errDisabled
			return startErr
		}
		id, startErr = e.startAttempt(ctx, req)
		return startErr
	})
	if errors.Is(startErr, errStale) {
		return modelResult{status: "stale"}, nil
	}
	if errors.Is(startErr, errDisabled) {
		return modelResult{status: "disabled"}, nil
	}
	if errors.Is(startErr, context.Canceled) || errors.Is(startErr, context.DeadlineExceeded) {
		return modelResult{status: "interrupted", failure: contextFailure(startErr)}, nil
	}
	if startErr != nil {
		return modelResult{}, startErr
	}
	if id == 0 {
		return r, nil
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer finishCancel()
	_, err := e.db.ExecContext(finishCtx, `UPDATE attempts SET status=?,actual_tier=?,actual_model=?,input_tokens=?,output_tokens=?,cached_tokens=? WHERE id=?`,
		r.status, r.actualTier, r.actualModel, r.usage.Input, r.usage.Output, r.usage.Cached, id)
	return r, err
}

var errStale = errors.New("会话已发生变化")
var errDisabled = errors.New("命名插件已停用")

func (e *Engine) attemptEnabled(ctx context.Context, req attemptRequest) bool {
	return req.eventID == nil || e.isEnabled == nil || e.isEnabled(ctx, req.agent)
}

func (e *Engine) startAttempt(ctx context.Context, req attemptRequest) (int64, error) {
	return transact(ctx, e.db, func(tx *sql.Tx) (int64, error) {
		if req.eventID != nil {
			var current bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events e JOIN sessions s
				ON e.agent=s.agent AND e.session_id=s.session_id
				WHERE e.id=? AND e.status='running' AND `+currentModel+`)`,
				*req.eventID).Scan(&current)
			if err != nil {
				return 0, err
			}
			if !current {
				return 0, errStale
			}
		}
		model, source, tier := providerInfo(req.provider, req.settings)
		result, err := tx.ExecContext(ctx, `INSERT INTO attempts(event_id,agent,provider,model,source,requested_tier,status,started)
			VALUES(?,?,?,?,?,?,'running',?)`, req.eventID, req.agent, req.provider, model, source, tier, e.now().UnixMilli())
		if err != nil {
			return 0, err
		}
		return result.LastInsertId()
	})
}

func providerInfo(provider string, s Settings) (model, source, tier string) {
	if provider == "claude" {
		return s.ClaudeModel, "claude_subscription", "default"
	}
	if provider == "api" {
		source := "openai_compatible"
		switch s.APIProtocol {
		case protocolAnthropic:
			source = "anthropic_compatible"
		case protocolGemini:
			source = "gemini_compatible"
		}
		return s.APIModel, source, "default"
	}
	tier = "default"
	if s.Fast {
		tier = "priority"
	}
	return s.CodexModel, "codex_subscription", tier
}

func readDecision(data []byte) (decision, error) {
	var d decision
	err := json.Unmarshal(data, &d)
	if err != nil || (d.Action != "keep" && d.Action != "rename") || (d.Action == "rename" && d.Title == "") {
		return decision{}, errors.New("命名服务返回的决策格式无效")
	}
	return d, nil
}

func response(data []byte, u usage, tier string) modelResult {
	d, err := readDecision(data)
	r := modelResult{decision: d, usage: u, actualTier: knownTier(tier), status: "completed"}
	if err != nil {
		r.status = "invalid_response"
		r.message = err.Error()
		r.failure = failureResponse
	}
	return r
}

func knownTier(tier string) string {
	switch tier {
	case "default", "priority", "flex", "scale":
		return tier
	default:
		return ""
	}
}

func cleanTokens(count *int64) *int64 {
	if count == nil || *count < 0 || *count > 1_000_000_000 {
		return nil
	}
	return count
}

func (u usage) clean() usage {
	u.Input = cleanTokens(u.Input)
	u.Output = cleanTokens(u.Output)
	u.Cached = cleanTokens(u.Cached)
	if u.Input != nil && u.Cached != nil && *u.Cached > *u.Input {
		u.Cached = nil
	}
	return u
}
