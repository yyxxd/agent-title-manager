package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

func (m *Manager) AutoUpdateBuiltins(ctx context.Context) (AutoUpdateResult, error) {
	result := AutoUpdateResult{}
	state, loadErr := m.load()
	if loadErr != nil {
		result.Failed = append(result.Failed, AutoUpdateFailed{ID: "", Name: "内置插件", Error: loadErr.Error()})
		m.autoMu.Lock()
		m.autoUpdate = result
		m.autoMu.Unlock()
		return result, nil
	}
	for _, id := range builtinOrder {
		old, ok := state.Items[id]
		if !ok || old.Dir == "" {
			continue
		}
		manifest, err := m.bundledManifest(id)
		if err != nil || !newerVersion(manifest.Version, old.Manifest.Version) {
			continue
		}
		item, err := m.Handle(ctx, "plugins.update", json.RawMessage(fmt.Sprintf(`{"id":%q,"source":"bundled"}`, id)))
		if err != nil {
			result.Failed = append(result.Failed, AutoUpdateFailed{ID: id, Name: builtins[id], CurrentVersion: old.Manifest.Version, TargetVersion: manifest.Version, Error: err.Error()})
			continue
		}
		updated := item.(Item)
		result.Upgraded = append(result.Upgraded, AutoUpdated{ID: id, Name: builtins[id], FromVersion: old.Manifest.Version, ToVersion: updated.Version, Enabled: updated.Enabled})
	}
	if err := m.cleanLegacyAdapters(ctx); err != nil {
		result.Failed = append(result.Failed, AutoUpdateFailed{Name: "旧版插件辅助文件", Error: err.Error()})
	}
	m.autoMu.Lock()
	m.autoUpdate = result
	m.autoMu.Unlock()
	return result, nil
}

func (m *Manager) updateBuiltin(ctx context.Context, state registry, id string) (any, error) {
	if state.Pending != nil {
		return nil, errors.New("请先停用以回滚有未完成变更的插件")
	}
	old := state.Items[id]
	if builtins[id] == "" || old.Dir == "" {
		return nil, errors.New("该插件没有可更新的已安装内置适配器")
	}
	manifest, err := m.bundledManifest(id)
	if err != nil {
		return nil, err
	}
	if !newerVersion(manifest.Version, old.Manifest.Version) {
		return nil, errors.New("当前安装包未提供更高版本的适配器")
	}
	rel, err := m.bundledRelease(ctx, id, manifest)
	if err != nil {
		return nil, err
	}
	result, err := m.replaceBuiltinRegistration(state, id, old, rel)
	if err != nil {
		err = errors.Join(err, os.RemoveAll(m.packageDir(rel)))
	}
	return result, err
}

func (m *Manager) replaceBuiltinRegistration(state registry, id string, old record, rel release) (any, error) {
	next := old
	next.release = rel
	if !old.Enabled {
		return m.commitItem(state, id, next, nil)
	}
	h, err := resolveHost(id)
	if err != nil {
		return nil, err
	}
	if len(old.Registration) != 1 || old.Registration[0].File != h.settings {
		return nil, errors.New("宿主配置目录已改变，请先停用旧适配器")
	}
	actual, err := readSettings(h.settings)
	if err != nil {
		return nil, err
	}
	changes, err := unregisterFrom(id, old, actual)
	if err != nil {
		return nil, err
	}
	base := withChanges(actual, h.settings, changes)
	entry, err := m.hostEntry(id, rel)
	if err != nil {
		return nil, err
	}
	owned, err := registration(h, base, entry, true)
	if err != nil {
		return nil, err
	}
	if owned.Before.equal(owned.After) {
		return nil, errors.New("新版适配器注册已存在，请检查宿主设置")
	}
	next.Entry, next.Registration = entry, []change{owned}
	return m.commitItem(state, id, next, combine(changes, owned))
}

func newerVersion(candidate, current string) bool {
	left := strings.SplitN(strings.SplitN(candidate, "+", 2)[0], "-", 2)
	right := strings.SplitN(strings.SplitN(current, "+", 2)[0], "-", 2)
	leftCore, rightCore := strings.Split(left[0], "."), strings.Split(right[0], ".")
	if len(leftCore) != 3 || len(rightCore) != 3 {
		return false
	}
	for i := range leftCore {
		if cmp := compareNumber(leftCore[i], rightCore[i]); cmp != 0 {
			return cmp > 0
		}
	}
	if len(left) != len(right) {
		return len(left) == 1
	}
	if len(left) == 1 {
		return false
	}
	return comparePrerelease(strings.Split(left[1], "."), strings.Split(right[1], ".")) > 0
}

func comparePrerelease(a, b []string) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		aNumeric, bNumeric := strings.Trim(a[i], "0123456789") == "", strings.Trim(b[i], "0123456789") == ""
		if aNumeric && bNumeric {
			if cmp := compareNumber(a[i], b[i]); cmp != 0 {
				return cmp
			}
		} else if aNumeric != bNumeric {
			if aNumeric {
				return -1
			}
			return 1
		} else if cmp := strings.Compare(a[i], b[i]); cmp != 0 {
			return cmp
		}
	}
	return len(a) - len(b)
}

func compareNumber(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}
