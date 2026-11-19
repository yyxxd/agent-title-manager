package orcasync

import "testing"

func TestAntigravityTerminalOriginDefaults(t *testing.T) {
	for _, c := range []struct{ tab, pane, want string }{
		{"", "pwsh.exe", titleAuto},
		{`C:\Users\yy\AppData\Local\Microsoft\PowerShell\7\pwsh.exe`, `C:\Users\yy\AppData\Local\Microsoft\PowerShell\7\pwsh.exe`, titleAuto},
		{"Terminal", "pwsh.exe", titleAuto},
		{"agy", "agy.exe", titleAuto},
		{"我的工作会话", "pwsh.exe", titleUnknown},
	} {
		t.Run(c.tab, func(t *testing.T) {
			title := c.tab
			if c.tab == "" {
				title = ""
			}
			got := terminalOrigin(place{Title: &title, PaneTitle: c.pane}, "antigravity")
			if got != c.want {
				t.Fatalf("origin=%q want %q", got, c.want)
			}
		})
	}
}

func TestAntigravityDefaultChatTitleOrigin(t *testing.T) {
	if got := defaultTitles["antigravity"]; got != "Antigravity Chat" {
		t.Fatal(got)
	}
}
