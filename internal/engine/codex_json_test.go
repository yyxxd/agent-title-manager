package engine

import (
	"strings"
	"testing"
)

func TestCodexAcceptsDecisionAfterRecoverableItemError(t *testing.T) {
	warning := `{"type":"item.completed","item":{"type":"error","message":"MCP context unavailable"}}`
	decision := `{"type":"item.completed","item":{"type":"agent_message","text":"{\"action\":\"rename\",\"title\":\"🧩 构建｜修复路径\"}"}}`
	done := `{"type":"turn.completed","usage":{"input_tokens":19,"cached_input_tokens":0,"output_tokens":5}}`
	trace := func(events ...string) modelResult {
		return parseCodex([]byte(strings.Join(events, "\n")), false, nil, nil)
	}
	if r := trace(warning, decision, done); r.status != "completed" || r.decision.Title != goodTitle ||
		!hasCount(r.usage.Cached, 0) || r.actualTier != "" {
		t.Fatal(r)
	}
	if r := trace(decision, warning, done); r.status != "failed" {
		t.Fatal(r)
	}
	if r := trace(`{"type":"turn.failed","error":{"message":"fatal"}}`, decision, done); r.status != "failed" {
		t.Fatal(r)
	}
}
