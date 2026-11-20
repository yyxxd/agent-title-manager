package desktop

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/engine"
)

type eventBindingKey struct{ agent, session, revision string }

type unresolved struct {
	handle, hint string
	until        time.Time
}

type lateBinding struct {
	eventBindingKey
	unresolved
	id, title, sessionRevision string
}

type lateBindings struct {
	mu     sync.Mutex
	items  map[eventBindingKey]unresolved
	queue  chan lateBinding
	cancel context.CancelFunc
	done   chan struct{}
}

func startLateBindings(parent context.Context, backend Backend) *lateBindings {
	ctx, cancel := context.WithCancel(parent)
	l := &lateBindings{items: make(map[eventBindingKey]unresolved), queue: make(chan lateBinding, 24), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-l.queue:
				l.retry(ctx, backend, job)
			}
		}
	}()
	return l
}

func (l *lateBindings) stop() { l.cancel(); <-l.done }

func (l *lateBindings) remember(event pendingEvent, pending bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := eventBindingKey{event.key.agent, event.key.session, event.key.revision}
	if !pending {
		delete(l.items, key)
		return
	}
	now := time.Now()
	if len(l.items) >= 256 {
		oldest := key
		for k, item := range l.items {
			if !item.until.After(now) {
				delete(l.items, k)
			} else if first, ok := l.items[oldest]; !ok || first.until.After(item.until) {
				oldest = k
			}
		}
		if len(l.items) >= 256 {
			delete(l.items, oldest)
		}
	}
	l.items[key] = unresolved{event.handle(), event.hint(), now.Add(10 * time.Minute)}
}

func (l *lateBindings) enqueue(agent, session, revision, sessionRevision, id, title string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := eventBindingKey{agent, session, revision}
	item, ok := l.items[key]
	if !ok || !item.until.After(time.Now()) {
		return
	}
	select {
	case l.queue <- lateBinding{key, item, id, title, sessionRevision}:
	default:
		log.Print("Orca 补绑队列已满")
	}
}

func (l *lateBindings) retry(ctx context.Context, backend Backend, job lateBinding) {
	if backend.RetryBinding == nil || backend.AttachBinding == nil || !backend.orcaEnabled(ctx, job.agent) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	binding, ok := backend.RetryBinding(ctx, job.agent, job.session, job.handle, job.hint)
	if !ok || !backend.orcaEnabled(ctx, job.agent) {
		return
	}
	if err := backend.AttachBinding(ctx, engine.ConfirmedBinding{
		ID: job.id, Agent: job.agent, SessionID: job.session, Revision: job.revision, NativeTitle: job.title,
		SessionRevision: job.sessionRevision,
		OrcaID:          binding.ID, OrcaTitle: binding.Title, Origin: binding.Origin, NativeHandle: binding.Handle,
	}); err != nil {
		log.Print("Orca 补绑写入失败")
	}
}
