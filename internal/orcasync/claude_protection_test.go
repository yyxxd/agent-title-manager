package orcasync

import (
	"context"
	"testing"
)

func TestClaudeProtectionDoesNotUseDefaultAccount(t *testing.T) {
	reads := 0
	i := Integration{ReadNative: func(context.Context, string, string, string) (string, error) { reads++; return "", ErrNativePending }}
	if _, _, err := i.ReadProtectionTitles(t.Context(), "claude", nativeFixture, nativeFixture, acpChat); err == nil || reads != 0 {
		t.Fatalf("unverified account read: calls=%d error=%v", reads, err)
	}
}
