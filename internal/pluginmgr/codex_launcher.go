package pluginmgr

import (
	"bytes"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed codex-launcher.mjs
var codexLauncher []byte

func stableCodexRelease(id string, rel release) bool {
	return id == "codex" && rel.Manifest.Entry == "index.mjs" && !newerVersion("0.2.2", rel.Manifest.Version)
}

func (m *Manager) ensureCodexLauncher() (string, error) {
	path := filepath.Join(m.root, "codex-hook.mjs")
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return path, writeAtomic(path, codexLauncher, 0600)
	case err != nil:
		return "", err
	case !info.Mode().IsRegular() || isReparse(info) || info.Size() != int64(len(codexLauncher)):
		return "", errors.New("Codex Hook 启动器已被修改")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(content, codexLauncher) {
		return "", errors.New("Codex Hook 启动器已被修改")
	}
	return path, nil
}
