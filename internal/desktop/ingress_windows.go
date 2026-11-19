package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"
)

type eventKey struct {
	agent, session, revision string
	nativeOnly               bool
}

type pendingEvent struct {
	key    eventKey
	fields map[string]json.RawMessage
}

type eventIngress struct {
	done    chan struct{}
	queue   chan pendingEvent
	mutex   sync.Mutex
	pending map[eventKey]struct{}
}

func startIngress(parent context.Context, backend Backend) *eventIngress {
	ingress := &eventIngress{
		done: make(chan struct{}), queue: make(chan pendingEvent, 24),
		pending: make(map[eventKey]struct{}),
	}
	go ingress.run(context.WithoutCancel(parent), backend)
	return ingress
}

func (ingress *eventIngress) stop() {
	close(ingress.queue)
	<-ingress.done
}

func (s *Service) pluginEvent(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return nil, errors.New("事件 JSON 无效")
	}
	agent := fieldString(fields, "agent")
	if !s.pluginEnabled(ctx, agent) {
		return nil, errors.New("Agent 插件未启用或目前不可用")
	}
	if method != "event.submit" {
		if method == "title.claim" && s.ingress.sessionPending(agent, fieldString(fields, "sessionId")) {
			return map[string]any{"candidate": nil, "status": "ingress_pending"}, nil
		}
		canonical, err := canonicalParams(fields, "agent", "sessionId", "currentTitle", "revision", "id", "observedTitle")
		if err != nil {
			return nil, err
		}
		return s.backend.Engine(ctx, method, canonical)
	}
	key := eventKey{agent, fieldString(fields, "sessionId"), fieldString(fields, "revision"), string(fields["nativeTitleOnly"]) == "true"}
	if key.session == "" || key.revision == "" {
		return nil, errors.New("缺少会话 ID 或版本")
	}
	event := pendingEvent{key, fields}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	queued, duplicate, err := s.ingress.add(event)
	if err != nil {
		return nil, err
	}
	if queued {
		return map[string]any{"accepted": true, "status": "ingress_pending", "duplicate": duplicate}, nil
	}
	body, err := event.params(false)
	if err != nil {
		return nil, err
	}
	return s.backend.Engine(ctx, method, body)
}

func (s *Service) pluginEnabled(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	enabled, err := s.backend.Enabled(ctx, id)
	return err == nil && enabled
}

func (ingress *eventIngress) add(event pendingEvent) (queued, duplicate bool, err error) {
	ingress.mutex.Lock()
	defer ingress.mutex.Unlock()
	if _, found := ingress.pending[event.key]; found {
		return true, true, nil
	}
	if !event.needsBinding() && !ingress.hasSession(event.key) {
		return false, false, nil
	}
	select {
	case ingress.queue <- event:
		ingress.pending[event.key] = struct{}{}
		return true, false, nil
	default:
		return false, false, errors.New("ingress_busy: 事件核验队列已满，请重试")
	}
}

func (ingress *eventIngress) hasSession(key eventKey) bool {
	for pending := range ingress.pending {
		if key.agent == pending.agent && key.session == pending.session {
			return true
		}
	}
	return false
}

func (ingress *eventIngress) sessionPending(agent, session string) bool {
	ingress.mutex.Lock()
	defer ingress.mutex.Unlock()
	return ingress.hasSession(eventKey{agent: agent, session: session})
}

func (ingress *eventIngress) run(ctx context.Context, backend Backend) {
	defer close(ingress.done)
	for event := range ingress.queue {
		if err := event.forward(ctx, backend); err != nil {
			log.Print("后台提交被拒绝，未记录会话内容")
		}
		ingress.mutex.Lock()
		delete(ingress.pending, event.key)
		ingress.mutex.Unlock()
	}
}

func (event pendingEvent) forward(ctx context.Context, backend Backend) error {
	agent := event.key.agent
	var bound bool
	if event.needsBinding() && backend.ResolveTitleOrigin != nil {
		bindingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		handle := fieldString(event.fields, "orcaNativeHandle")
		orcaID := fieldString(event.fields, "orcaSessionId")
		title, origin, approved := backend.ResolveTitleOrigin(bindingCtx, agent, event.key.session, handle, orcaID)
		cancel()
		bound = approved
		if approved {
			if origin != "auto" && origin != "manual" {
				origin = "unknown"
			}
			event.fields["orcaTitle"], _ = json.Marshal(title)
			event.fields["orcaTitleOrigin"], _ = json.Marshal(origin)
		}
	}
	if event.key.nativeOnly && !bound {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	enabled, err := backend.Enabled(ctx, agent)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	params, err := event.params(bound)
	if err != nil {
		return err
	}
	_, err = backend.Engine(ctx, "event.submit", params)
	return err
}

func (event pendingEvent) needsBinding() bool {
	return isBuiltinAgent(event.key.agent) && fieldString(event.fields, "orcaNativeHandle") != "" &&
		fieldString(event.fields, "orcaSessionId") != ""
}

func (event pendingEvent) params(bound bool) (json.RawMessage, error) {
	fields := []string{"agent", "sessionId", "revision", "currentTitle", "titleOrigin", "nativeTitleOnly",
		"originalGoal", "confirmationOnly", "project", "turns"}
	if bound {
		fields = append(fields, "orcaSessionId", "orcaTitle", "orcaTitleOrigin", "orcaNativeHandle")
	}
	return canonicalParams(event.fields, fields...)
}

func fieldString(fields map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(fields[key], &value) != nil || strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}

func canonicalParams(fields map[string]json.RawMessage, allow ...string) (json.RawMessage, error) {
	result := make(map[string]json.RawMessage, len(allow))
	for _, field := range allow {
		if value, ok := fields[field]; ok {
			result[field] = value
		}
	}
	return json.Marshal(result)
}

func isBuiltinAgent(id string) bool {
	return id == "codex" || id == "claude" || id == "pi" || id == "antigravity"
}
