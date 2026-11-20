package orcasync

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/pluginmgr"
)

func TestDisabledIntegrationDoesNotTouchOrca(t *testing.T) {
	i := Integration{
		Enabled: func(context.Context, string) (bool, error) { return false, nil },
		run: func(context.Context, ...string) ([]byte, error) {
			t.Fatal("disabled Orca launched a CLI command")
			return nil, nil
		},
		origin: func(context.Context, string, string, string) string {
			t.Fatal("disabled Orca queried title origin")
			return ""
		},
	}
	if _, ok := i.ResolveTitleOrigin(t.Context(), "codex", nativeFixture, "", "chat"); ok {
		t.Fatal("resolved while disabled")
	}
	if _, ok := i.RetryBinding(t.Context(), "antigravity", nativeFixture, nativeFixture, "chat"); ok {
		t.Fatal("retried while disabled")
	}
	if err := i.SyncVerified(t.Context(), "chat", "", "title", "codex", nativeFixture, nativeFixture); !errors.Is(err, ErrSkipped) {
		t.Fatal(err)
	}
	if _, err := i.readACPSettlements(t.Context(), time.Now().UnixMilli()); !errors.Is(err, ErrSkipped) {
		t.Fatal("disabled Orca opened ACP database")
	}
	if err := i.RunACPObserver(t.Context(), "unused", nil, nil); err != nil {
		t.Fatal("disabled Orca started ACP observer")
	}
}

func TestIntegrationDiscoversAllNativeAdapters(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "pi", "antigravity"} {
		t.Run(agent, func(t *testing.T) {
			handle := nativeFixture
			if agent == "pi" {
				handle, _ = piTitleFile(t, "native")
			}
			shows := 0
			i := Integration{
				Enabled:  alwaysEnabled,
				Validate: (&pluginmgr.Manager{}).ValidateNativeHandle,
				origin:   func(context.Context, string, string, string) string { return titleAuto },
				run: func(_ context.Context, args ...string) ([]byte, error) {
					switch strings.Join(args[:2], " ") {
					case "agent show":
						if args[2] == handle {
							return providerMapping("chat")
						}
						shows++
						if agent == "antigravity" {
							return replyPlace(place{ID: "chat", Agent: agent, Kind: "terminal", HostID: "local", PaneKey: "tab:leaf"})
						}
						title := "native baseline"
						return replyPlace(place{ID: "chat", Agent: agent, Kind: "chat", HostID: "local", Title: &title})
					case "terminal list":
						return singleTerminalLayout(agent, false)
					default:
						t.Fatalf("unexpected command %v", args)
						return nil, nil
					}
				},
			}
			got, ok := i.ResolveTitleOrigin(t.Context(), agent, nativeFixture, handle, idPrefix+"chat")
			if !ok || got.ID != "chat" || got.Origin != titleAuto || got.Handle != handle || shows != 1 {
				t.Fatalf("unexpected binding: %+v, found=%v, shows=%d", got, ok, shows)
			}
			for _, hint := range []string{"different", idPrefix} {
				if _, ok := i.ResolveTitleOrigin(t.Context(), agent, nativeFixture, handle, hint); ok || shows != 1 {
					t.Fatalf("trusted conflicting hint %q", hint)
				}
			}
		})
	}
}

func TestTerminalDiscoveryRetriesAndRejectsSplitTabs(t *testing.T) {
	probes := 0
	split := false
	i := Integration{Enabled: alwaysEnabled, run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "terminal" {
			return singleTerminalLayout("antigravity", split)
		}
		if args[2] == nativeFixture {
			probes++
			if probes == 1 {
				return nil, errors.New("not mapped yet")
			}
			return providerMapping("chat")
		}
		return replyPlace(place{ID: "chat", Agent: "antigravity", Kind: "terminal", HostID: "local", PaneKey: "tab:leaf"})
	}}
	got, ok := i.RetryBinding(t.Context(), "antigravity", nativeFixture, nativeFixture, "chat")
	if !ok || got.Title != "agy" || probes != 2 {
		t.Fatalf("delayed lookup: %+v, found=%v, probes=%d", got, ok, probes)
	}
	split = true
	if _, ok := i.ResolveTitleOrigin(t.Context(), "antigravity", nativeFixture, nativeFixture, "chat"); ok {
		t.Fatal("split tab was approved")
	}
}

func alwaysEnabled(context.Context, string) (bool, error) { return true, nil }

func replyPlace(at place) ([]byte, error) {
	return json.Marshal(map[string]any{"ok": true, "result": map[string]any{"orcaSessionId": at.ID, "kind": at.Kind, "places": []place{at}}})
}

func singleTerminalLayout(agent string, split bool) ([]byte, error) {
	pane := map[string]any{"type": "terminal", "handle": "fixture", "leafId": "leaf", "title": "agy"}
	var panes any = pane
	if split {
		panes = map[string]any{"type": "split", "children": []any{pane, pane}}
	}
	return json.Marshal(map[string]any{
		"ok": true, "result": map[string]any{
			"terminals": []terminalEntry{{Handle: "fixture", HostID: "local", TabID: "tab", LeafID: "leaf", Identity: agent}},
			"visualLayouts": []any{map[string]any{
				"type": "tabs", "tabs": []any{map[string]any{"tabId": "tab", "title": "agy", "panes": panes}},
			}},
		},
	})
}
