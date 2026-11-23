package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"slices"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/yyxxd/agent-title-manager/internal/engine"
	"github.com/yyxxd/agent-title-manager/internal/ipc"
	"github.com/yyxxd/agent-title-manager/internal/orcasync"
	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
)

type Backend struct {
	Engine             ipc.Handler
	Plugins            ipc.Handler
	ObserveCodexHook   func(context.Context, json.RawMessage) (any, error)
	AutoUpdateBuiltins func(context.Context) (any, error)
	Enabled            func(context.Context, string) (bool, error)
	OrcaEnabled        func(context.Context, string) bool
	ResolveTitleOrigin func(context.Context, string, string, string, string) (orcasync.Binding, bool)
	RetryBinding       func(context.Context, string, string, string, string) (orcasync.Binding, bool)
	NativeTitleMatches func(context.Context, string, string, string, string) (bool, error)
	ReadNativeTitle    orcasync.NativeTitleReader
	AttachBinding      func(context.Context, engine.ConfirmedBinding) error
	Observe            func(context.Context)
	Close              func() error
}

type Service struct {
	scope             string
	open              func() (Backend, error)
	backend           Backend
	listener          net.Listener
	done              chan struct{}
	ingress           *eventIngress
	quit              func()
	appearance        *appearance
	appearanceChanged func()
	emit              func(string, any)
	pendingAutoUpdate any
	updater           *updateController
	stopObserver      func()
}

func (s *Service) bindEmitter(emit func(string, any)) {
	s.emit = emit
	if s.pendingAutoUpdate != nil {
		emit("plugins:auto-update", s.pendingAutoUpdate)
		s.pendingAutoUpdate = nil
	}
}

func NewService(scope string, open func() (Backend, error)) *Service {
	return &Service{scope: scope, open: open}
}

// SetUpdater attaches the in-app update controller to the service.
func (s *Service) SetUpdater(c *updateController) {
	s.updater = c
	if c != nil {
		// Late-bound: Service.emit is assigned when the Wails app starts.
		c.bind(func(name string, data any) {
			if s.emit != nil {
				s.emit(name, data)
			}
		})
	}
}

func (s *Service) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	listener, err := ipc.Listen(s.scope)
	if err != nil {
		return err
	}
	backend, err := s.open()
	if err != nil {
		return errors.Join(err, listener.Close())
	}
	s.backend, s.listener, s.done = backend, listener, make(chan struct{})
	if backend.AutoUpdateBuiltins != nil {
		if result, err := backend.AutoUpdateBuiltins(ctx); err == nil && s.emit != nil {
			if summary, ok := result.(pluginmgr.AutoUpdateResult); ok && (len(summary.Upgraded) > 0 || len(summary.Failed) > 0) {
				s.emit("plugins:auto-update", result)
			}
		} else if err == nil {
			if summary, ok := result.(pluginmgr.AutoUpdateResult); ok && (len(summary.Upgraded) > 0 || len(summary.Failed) > 0) {
				s.pendingAutoUpdate = result
			}
		} else if err != nil {
			log.Printf("plugin auto-update: %v", err)
		}
	}
	s.ingress = startIngress(ctx, backend)
	if s.updater != nil {
		s.updater.start(ctx)
	}
	go func() {
		defer close(s.done)
		if err := ipc.Serve(ctx, listener, s.pluginEvent); err != nil {
			log.Printf("bridge: %v", err)
		}
	}()
	if backend.Observe != nil {
		observerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			backend.Observe(observerCtx)
		}()
		s.stopObserver = func() { cancel(); <-done }
	}
	return nil
}

func (s *Service) ServiceShutdown() error {
	if s.listener == nil {
		return nil
	}
	if s.stopObserver != nil {
		s.stopObserver()
	}
	err := s.listener.Close()
	<-s.done
	s.ingress.stop()
	return errors.Join(err, s.backend.Close())
}

var engineMethods = []string{"providers.test", "providers.models", "providers.subscriptionModels", "providers.endpoints", "stats.daily", "stats.range", "records.list", "records.open", "sync.retry", "sync.adopt"}
var pluginMethods = []string{"plugins.list", "plugins.install", "plugins.update", "plugins.enable", "plugins.disable"}

func (s *Service) Call(ctx context.Context, method string, body string) (any, error) {
	if len(body) > 256*1024 || !json.Valid([]byte(body)) || !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return nil, errors.New("设置请求无效")
	}
	switch {
	case method == "settings.get" || method == "settings.save":
		return s.publicSettings(ctx, method, json.RawMessage(body))
	case method == "records.unlock":
		return s.unlockRecord(ctx, json.RawMessage(body))
	case slices.Contains(engineMethods, method):
		return s.backend.Engine(ctx, method, json.RawMessage(body))
	case slices.Contains(pluginMethods, method):
		return s.backend.Plugins(ctx, method, json.RawMessage(body))
	case method == "desktop.get":
		return s.preferences()
	case method == "desktop.startup":
		return s.setPreferences(json.RawMessage(body))
	case method == "desktop.appearance.get":
		return s.appearance.snapshot(), nil
	case method == "desktop.appearance.set":
		state, err := s.appearance.set(json.RawMessage(body))
		if err == nil {
			s.appearanceChanged()
		}
		return state, err
	case method == "desktop.quit":
		s.quit()
		return nil, nil
	case method == "desktop.update.get":
		return s.updater.snapshot()
	case method == "desktop.update.check":
		return s.updater.refresh(ctx)
	case method == "desktop.update.install":
		return s.updater.install(ctx, json.RawMessage(body))
	default:
		return nil, errors.New("桌面不支持此方法")
	}
}

func (s *Service) publicSettings(ctx context.Context, method string, body json.RawMessage) (any, error) {
	value, err := s.backend.Engine(ctx, method, body)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return canonicalParams(fields, "provider", "codexModel", "codexEffort", "claudeModel", "claudeEffort", "fast", "apiProtocol", "apiBaseUrl", "apiModel",
		"hasApiKey", "fallbackEnabled", "maxTitleLength")
}
