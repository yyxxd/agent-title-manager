package pluginmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func (m *Manager) modify(ctx context.Context, state registry, method string, req request) (any, error) {
	if state.Pending != nil {
		if method != "plugins.disable" {
			return nil, errors.New("请先停用以回滚有未完成变更的插件")
		}
		if err := m.recoverPending(state, req.ID); err != nil {
			return nil, err
		}
		state.Pending = nil
		return m.item(state, req.ID), nil
	}
	old, installed := state.Items[req.ID]
	if !installed && builtins[req.ID] == "" {
		return nil, errors.New("插件尚未安装")
	}
	switch method {
	case "plugins.enable":
		return m.enable(ctx, state, req, old)
	case "plugins.disable":
		return m.stop(state, req.ID, old)
	default:
		return nil, errors.New("未知插件操作")
	}
}

func (m *Manager) commitItem(state registry, id string, next record, changes []change) (any, error) {
	if err := m.execute(state, transaction{ID: id, Next: next, Changes: changes}); err != nil {
		return nil, err
	}
	state.Items[id] = next
	return m.item(state, id), nil
}

func (m *Manager) enable(ctx context.Context, state registry, req request, old record) (any, error) {
	if old.Enabled {
		return m.item(state, req.ID), nil
	}
	next := old
	next.Enabled = true
	switch {
	case req.ID == "orca":
		return m.commitItem(state, req.ID, next, nil)
	case builtins[req.ID] == "":
		if err := m.writeRuntime(m.packageDir(old.release)); err != nil {
			return nil, err
		}
		return m.commitItem(state, req.ID, next, nil)
	default:
		return m.enableBuiltin(ctx, state, req, next)
	}
}

func (m *Manager) enableBuiltin(ctx context.Context, state registry, req request, next record) (any, error) {
	h, err := resolveHost(req.ID)
	if err != nil {
		return nil, err
	}
	base, err := readSettings(h.settings)
	if err != nil {
		return nil, err
	}
	rel, staged, err := m.installedRelease(ctx, req.ID, next.release)
	if err != nil {
		return nil, err
	}
	result, err := m.registerBuiltin(state, h, base, next, rel)
	if err != nil && staged {
		err = errors.Join(err, os.RemoveAll(m.packageDir(rel)))
	}
	return result, err
}

func (m *Manager) bundledManifest(id string) (Manifest, error) {
	manifest, err := readManifest(filepath.Join(m.bundled, id))
	if err != nil {
		return Manifest{}, err
	}
	if manifest.ID != id || manifest.Host != id {
		return Manifest{}, errors.New("随附适配器身份与宿主不符")
	}
	return manifest, nil
}

func (m *Manager) installedRelease(ctx context.Context, id string, prior release) (release, bool, error) {
	if prior.Dir != "" {
		if installed, err := readManifest(m.packageDir(prior)); err == nil && installed == prior.Manifest {
			return prior, false, nil
		}
		return release{}, false, errors.New("现有适配器文件缺失，请通过插件面板安装随附更新")
	}
	manifest, err := m.bundledManifest(id)
	if err != nil {
		return release{}, false, err
	}
	rel, err := m.bundledRelease(ctx, id, manifest)
	return rel, err == nil, err
}

func (m *Manager) bundledRelease(ctx context.Context, id string, manifest Manifest) (release, error) {
	stage, err := stagePackage(m.root)
	if err != nil {
		return release{}, err
	}
	if err = copyLocal(ctx, filepath.Join(m.bundled, id), stage); err == nil {
		var staged Manifest
		staged, err = readManifest(stage)
		if err == nil && staged != manifest {
			err = errors.New("安装期间随附适配器已改变")
		}
	}
	if err != nil {
		return release{}, errors.Join(err, os.RemoveAll(stage))
	}
	return release{manifest, filepath.Base(stage)}, nil
}

func (m *Manager) registerBuiltin(state registry, h host, base snapshot, next record, rel release) (any, error) {
	entry, err := m.hostEntry(h.id, rel)
	if err != nil {
		return nil, err
	}
	owned, err := registration(h, base, entry, true)
	if err != nil {
		return nil, err
	}
	if owned.Before.equal(owned.After) {
		return nil, errors.New("适配器注册已存在，请先检查宿主设置")
	}
	next.release, next.Entry, next.Registration = rel, entry, []change{owned}
	return m.commitItem(state, h.id, next, []change{owned})
}

func (m *Manager) stop(state registry, id string, old record) (any, error) {
	next := old
	next.Enabled = false
	changes, err := unregister(id, old)
	if err != nil {
		return nil, err
	}
	next.Registration, next.Entry = nil, ""
	return m.commitItem(state, id, next, changes)
}

func unregister(id string, old record) ([]change, error) {
	if !old.Enabled || len(old.Registration) == 0 {
		return nil, nil
	}
	owned := old.Registration[0]
	actual, err := readSettings(owned.File)
	if err != nil {
		return nil, err
	}
	return unregisterFrom(id, old, actual)
}

func unregisterFrom(id string, old record, actual snapshot) ([]change, error) {
	owned := old.Registration[0]
	if actual.equal(owned.After) {
		return []change{owned.reverse()}, nil
	}
	h := host{id: id, settings: owned.File}
	ret, err := registration(h, actual, old.Entry, false)
	return appendChanged(nil, ret), err
}

func appendChanged(changes []change, c change) []change {
	if c.Before.equal(c.After) {
		return changes
	}
	return append(changes, c)
}

func withChanges(base snapshot, file string, planned []change) snapshot {
	for _, action := range planned {
		if action.File == file {
			return action.After
		}
	}
	return base
}

func combine(changes []change, next change) []change {
	out := append([]change(nil), changes...)
	for i, original := range out {
		if original.File != next.File {
			continue
		}
		next.Before = original.Before
		if next.Before.equal(next.After) {
			return append(out[:i], out[i+1:]...)
		}
		out[i] = next
		return out
	}
	return appendChanged(out, next)
}
