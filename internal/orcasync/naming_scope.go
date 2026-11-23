package orcasync

import (
	"context"
	"os"
	"path/filepath"
)

// NativeNamingScope returns the local account for an independently verified
// chat binding. A terminal uses the configured adapter's own native locator.
func NativeNamingScope(ctx context.Context, agent, session string, binding Binding) (string, error) {
	if binding.Surface == "terminal" {
		return "", nil
	}
	if binding.Surface != "chat" || !nativeID.MatchString(binding.ID) || !nativeID.MatchString(session) {
		return "", ErrNativePending
	}
	if agent == "antigravity" {
		return acpRoot(ctx, binding.ID, session)
	}
	var variable, field string
	switch agent {
	case "codex":
		variable, field = "CODEX_HOME", "threadId"
	case "claude":
		variable, field = "CLAUDE_CONFIG_DIR", "sessionId"
	case "pi":
		// The verified Pi handle is already scoped to the adapter's session root.
		return "", nil
	default:
		return "", ErrNativePending
	}
	db, err := openOrcaDatabase(orcaDatabase())
	if err != nil {
		return "", err
	}
	defer db.Close()
	var account string
	err = db.QueryRowContext(ctx, `SELECT json_extract(record_json,'$.accountHome.path')
		FROM agent_session_records
		WHERE session_id=? AND json_extract(record_json,'$.sessionId')=session_id
		AND json_extract(record_json,'$.schemaVersion')=2 AND json_extract(record_json,'$.provider')=?
		AND json_extract(record_json,'$.location.executionHostId')='local'
		AND json_extract(record_json,'$.accountHome.variable')=?
		AND json_extract(record_json,'$.providerHandleChain[#-1].handle.provider')=?
		AND json_extract(record_json,?)=?`, binding.ID, agent, variable, agent, "$.providerHandleChain[#-1].handle."+field, session).Scan(&account)
	if err != nil || !filepath.IsAbs(account) {
		return "", ErrNativePending
	}
	info, err := os.Lstat(account)
	if err != nil || !info.IsDir() || !unredirectedPath(account, info) {
		return "", ErrNativePending
	}
	return account, nil
}
