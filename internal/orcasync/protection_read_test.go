package orcasync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProtectionReadsBoundAntigravityChat(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		changed, wrongAccount, readFailed bool
	}{
		{name: "verified"}, {name: "changed", changed: true}, {name: "wrong account", wrongAccount: true}, {name: "read failed", readFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const nativeName = "Protected native title"
			const orcaName = "Antigravity Chat"
			home := t.TempDir()
			t.Setenv("ORCA_USER_DATA_PATH", home)
			account := filepath.Join(home, "antigravity-chat")
			data := filepath.Join(account, "antigravity-acp")
			recorded := account
			if tc.wrongAccount {
				recorded = filepath.Join(home, "different-account")
			}
			putACPFixture(t, makeACPDatabase(t, home), data, recorded, acpChat, acpNative, "", time.Now())
			annotation := filepath.Join(data, "annotations", acpNative+".pbtxt")
			if err := os.MkdirAll(filepath.Dir(annotation), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(annotation, []byte(nativeName), 0600); err != nil {
				t.Fatal(err)
			}
			current := orcaName
			reads := 0
			i := Integration{Enabled: alwaysEnabled, origin: func(context.Context, string, string, string) string { return titleAuto }}
			i.ReadNative = func(context.Context, string, string, string) (string, error) {
				t.Fatal("chat used unscoped read")
				return "", nil
			}
			i.ReadNativeAt = func(_ context.Context, agent, id, root string) (string, error) {
				reads++
				if agent != "antigravity" || id != acpNative || !samePath(root, data) {
					t.Fatalf("incorrect scope: %q %q", id, root)
				}
				if tc.changed {
					current = "changed while reading"
				}
				if tc.readFailed {
					return "", ErrNativePending
				}
				wire, err := os.ReadFile(annotation)
				return string(wire), err
			}
			i.run = func(_ context.Context, args ...string) ([]byte, error) {
				if strings.Join(args[:2], " ") != "agent show" {
					return nil, fmt.Errorf("unexpected Orca command: %v", args)
				}
				if args[2] == acpNative {
					return providerMapping(acpChat)
				}
				if args[2] == idPrefix+acpChat {
					return replyPlace(place{ID: acpChat, Agent: "antigravity", Kind: "chat", HostID: "local", Title: &current})
				}
				return nil, fmt.Errorf("unexpected Orca lookup: %v", args)
			}
			native, orca, err := i.ReadProtectionTitles(t.Context(), "antigravity", acpNative, acpNative, acpChat)
			switch {
			case tc.wrongAccount:
				if !errors.Is(err, errProtectionNative) || reads != 0 || orca != nil {
					t.Fatalf("unexpected result %q %v %v, reads=%d", native, orca, err, reads)
				}
			case tc.readFailed:
				if !errors.Is(err, errProtectionNative) || reads != 1 || orca != nil {
					t.Fatalf("unexpected result %q %v %v, reads=%d", native, orca, err, reads)
				}
			case tc.changed:
				if !errors.Is(err, errProtectionBinding) || reads != 1 || orca != nil {
					t.Fatalf("unexpected result %q %v %v, reads=%d", native, orca, err, reads)
				}
			default:
				if err != nil || native != nativeName || orca == nil || *orca != orcaName || reads != 1 {
					t.Fatalf("unexpected result %q %v %v, reads=%d", native, orca, err, reads)
				}
			}
		})
	}
}
