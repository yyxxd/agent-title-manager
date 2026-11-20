package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/engine"
	"github.com/yyxxd/agent-title-manager/internal/orcasync"
)

type eventKey struct {
	agent, session, revision string
	nativeOnly               bool
}

type pendingEvent struct {
	key    eventKey
	fields map[string]json.RawMessage
	probe  bool
}

type eventIngress struct {
	done    chan struct{}
	queue   chan pendingEvent
	mutex   sync.Mutex
	pending map[eventKey]struct{}
	late    *lateBindings
}

func startIngress(parent context.Context, backend Backend) *eventIngress {
	ingress := &eventIngress{
		done: make(chan struct{}), queue: make(chan pendingEvent, 24),
		pending: make(map[eventKey]struct{}), late: startLateBindings(parent, backend),
	}
	go ingress.run(context.WithoutCancel(parent), backend)
	return ingress
}

func (ingress *eventIngress) stop() {
	close(ingress.queue)
	<-ingress.done
	ingress.late.stop()
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
		result, err := s.backend.Engine(ctx, method, canonical)
		if err == nil && method == "title.ack" && s.backend.orcaEnabled(ctx, agent) {
			if eventRevision, sessionRevision, unbound := engine.UnboundAckRevisions(result); unbound {
				s.ingress.late.enqueue(agent, fieldString(fields, "sessionId"), eventRevision, sessionRevision,
					fieldString(fields, "id"), fieldString(fields, "observedTitle"))
			}
		}
		return result, err
	}
	key := eventKey{agent, fieldString(fields, "sessionId"), fieldString(fields, "revision"), string(fields["nativeTitleOnly"]) == "true"}
	if key.session == "" || key.revision == "" {
		return nil, errors.New("缺少会话 ID 或版本")
	}
	event := pendingEvent{key: key, fields: fields}
	event.probe = event.needsBinding() && s.backend.orcaEnabled(ctx, agent)
	if key.nativeOnly && !event.probe {
		return map[string]bool{"accepted": true}, nil
	}
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
	if !event.probe && !ingress.hasSession(event.key) {
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
		if err := event.forward(ctx, backend, ingress.late); err != nil {
			log.Print("后台提交被拒绝，未记录会话内容")
		}
		ingress.mutex.Lock()
		delete(ingress.pending, event.key)
		ingress.mutex.Unlock()
	}
}

func (backend Backend) orcaEnabled(ctx context.Context, agent string) bool {
	return backend.OrcaEnabled != nil && backend.OrcaEnabled(ctx, agent)
}

func (event pendingEvent) forward(ctx context.Context, backend Backend, late *lateBindings) error {
	agent := event.key.agent
	var bound bool
	if event.probe && backend.orcaEnabled(ctx, agent) && backend.ResolveTitleOrigin != nil {
		timeout := 2 * time.Second
		resolve := backend.ResolveTitleOrigin
		if event.key.nativeOnly && backend.RetryBinding != nil {
			timeout, resolve = 8*time.Second, backend.RetryBinding
		}
		bindingCtx, cancel := context.WithTimeout(ctx, timeout)
		binding, approved := resolve(bindingCtx, agent, event.key.session, event.handle(), event.hint())
		cancel()
		bound = approved && backend.orcaEnabled(ctx, agent)
		if bound {
			event.setBinding(binding)
		}
	}
	if event.key.nativeOnly {
		if !bound || backend.NativeTitleMatches == nil || !backend.orcaEnabled(ctx, agent) {
			return nil
		}
		readCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
		match, err := backend.NativeTitleMatches(readCtx, agent, event.key.session,
			fieldString(event.fields, "orcaNativeHandle"), fieldString(event.fields, "currentTitle"))
		cancel()
		if err != nil || !match || !backend.orcaEnabled(ctx, agent) {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	enabled, err := backend.Enabled(ctx, agent)
	if err != nil || !enabled {
		return err
	}
	params, err := event.params(bound)
	if err != nil {
		return err
	}
	_, err = backend.Engine(ctx, "event.submit", params)
	if err == nil && !event.key.nativeOnly {
		late.remember(event, event.probe && !bound && backend.orcaEnabled(ctx, agent))
	}
	return err
}

func (event pendingEvent) handle() string { return fieldString(event.fields, "nativeHandle") }
func (event pendingEvent) hint() string {
	wire, ok := event.fields["hostSessionHint"]
	if !ok {
		return ""
	}
	var hint string
	if json.Unmarshal(wire, &hint) != nil || hint == "" {
		// 绑定无效时仍允许原生会话走普通命名流程。
		return "\x00"
	}
	return hint
}

func (event pendingEvent) needsBinding() bool {
	// Antigravity Desktop 不上报 locator。Pi 只有文件路径能反查。
	switch event.key.agent {
	case "codex", "claude":
		return true
	case "pi", "antigravity":
		return event.handle() != ""
	}
	return false
}

func (event pendingEvent) setBinding(binding orcasync.Binding) {
	for key, value := range map[string]string{
		"orcaSessionId": binding.ID, "orcaTitle": binding.Title,
		"orcaTitleOrigin": binding.Origin, "orcaNativeHandle": binding.Handle,
	} {
		event.fields[key], _ = json.Marshal(value)
	}
}

func (event pendingEvent) params(bound bool) (json.RawMessage, error) {
	fields := []string{"agent", "sessionId", "revision", "currentTitle", "titleOrigin", "nativeTitleOnly",
		"originalGoal", "confirmationOnly", "substantiveActivity", "project", "turns"}
	if bound {
		fields = append(fields, "orcaSessionId", "orcaTitle", "orcaTitleOrigin", "orcaNativeHandle")
		if event.key.nativeOnly {
			identity, err := json.Marshal([]string{event.key.revision, fieldString(event.fields, "orcaSessionId"), fieldString(event.fields, "orcaTitle")})
			if err != nil {
				return nil, err
			}
			event.fields["revision"], _ = json.Marshal(fmt.Sprintf("%x", sha256.Sum256(identity)))
		}
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
