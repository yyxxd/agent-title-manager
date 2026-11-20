package pluginmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
)

var stagedPackageName = regexp.MustCompile(`^pkg-[0-9]{8}-[0-9]+$`)

var nativeOnlyVersion = map[string]string{
	"codex": "0.2.0", "claude": "0.2.0", "pi": "0.2.0", "antigravity": "0.3.0",
}

// 仅在新包登记成功后清理已退役的随附包，避免破坏事务回滚或删除诊断记录。
func (m *Manager) cleanLegacyAdapters(ctx context.Context) error {
	parent := filepath.Join(m.root, "packages")
	if _, err := os.Lstat(parent); os.IsNotExist(err) {
		return nil
	}
	_, err := m.locked(ctx, func(state registry) (any, error) {
		if state.Pending != nil {
			return nil, nil
		}
		return nil, cleanRetiredPackages(parent, state)
	})
	return err
}

func cleanRetiredPackages(parent string, state registry) error {
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || isReparse(info) {
		return errors.New("插件包目录不可用于清理")
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	var joined error
	for _, entry := range entries {
		dir := entry.Name()
		if !stagedPackageName.MatchString(dir) || !entry.IsDir() {
			continue
		}
		path := filepath.Join(parent, dir)
		info, err := entry.Info()
		if err != nil || isReparse(info) {
			joined = errors.Join(joined, err)
			continue
		}
		manifest, err := readManifest(path)
		if err != nil || manifest.Host != manifest.ID || builtins[manifest.ID] == "" {
			continue
		}
		current := state.Items[manifest.ID]
		minimum := nativeOnlyVersion[manifest.ID]
		if current.Dir == "" || current.Dir == dir || newerVersion(minimum, current.Manifest.Version) ||
			!newerVersion(minimum, manifest.Version) {
			continue
		}
		joined = errors.Join(joined, removeLegacySupport(path))
	}
	return joined
}

func removeLegacySupport(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	var joined error
	for _, name := range []string{"orca.mjs", "orca-binding.mjs"} {
		info, err := root.Lstat(name)
		if os.IsNotExist(err) {
			continue
		}
		if err == nil && info.Mode().IsRegular() && !isReparse(info) {
			err = root.Remove(name)
		}
		joined = errors.Join(joined, err)
	}
	return joined
}
