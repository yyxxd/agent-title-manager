package pluginmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const codexHookRegistrationPrefix = "atm-registration-"

var codexHookRegistration = regexp.MustCompile(`^atm-registration-[A-Z2-7]{26}$`)

func (m *Manager) codexHookProof(r record) (string, string) {
	_, token, _ := strings.Cut(r.Entry, " "+codexHookRegistrationPrefix)
	token = codexHookRegistrationPrefix + token
	if !codexHookRegistration.MatchString(token) || m.version == "" || len(r.Registration) != 1 {
		return "", ""
	}
	sum := sha256.Sum256([]byte(m.version + "\x00" + r.Manifest.Version + "\x00" + r.Registration[0].File + "\x00" + r.Entry))
	return token, hex.EncodeToString(sum[:])
}

func (m *Manager) codexHookObserved(r record) bool {
	_, proof := m.codexHookProof(r)
	if proof == "" {
		return false
	}
	path := m.codexHookProofPath()
	if info, err := os.Stat(path); err != nil || info.Size() != int64(len(proof)) {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && string(data) == proof
}

func (m *Manager) codexHookProofPath() string { return filepath.Join(m.root, "codex-hook-observed") }

// ObserveCodexHook records only the identity of the command the host executed.
func (m *Manager) ObserveCodexHook(ctx context.Context, params json.RawMessage) (any, error) {
	var req struct {
		Registration string `json:"registration"`
	}
	if json.Unmarshal(params, &req) != nil || !codexHookRegistration.MatchString(req.Registration) {
		return nil, errors.New("Codex Hook 登记参数无效")
	}
	return m.locked(ctx, func(state registry) (any, error) {
		r := state.Items["codex"]
		token, proof := m.codexHookProof(r)
		if !r.Enabled || state.Pending != nil || token != req.Registration {
			return nil, errors.New("Codex Hook 登记已失效")
		}
		if err := m.checkRegistration("codex", r); err != nil {
			return nil, err
		}
		if !m.codexHookObserved(r) {
			if err := writeAtomic(m.codexHookProofPath(), []byte(proof), 0600); err != nil {
				return nil, err
			}
		}
		return struct{}{}, nil
	})
}
