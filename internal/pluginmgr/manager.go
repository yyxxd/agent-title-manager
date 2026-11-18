// Package pluginmgr serves trusted desktop plugin management requests.
package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	"github.com/yyxxd/agent-title-manager/internal/orcasync"
)

type Manager struct {
	root, bundled string
	gate          atomic.Pointer[enabledFlags]
}

type Item struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
	Kind             string `json:"kind"`
	Detail           string `json:"detail,omitempty"`
	EntryPath        string `json:"entryPath,omitempty"`
	TakeoverRequired bool   `json:"takeoverRequired,omitempty"`
	CanRestore       bool   `json:"canRestore,omitempty"`
}

type request struct {
	ID       string `json:"id"`
	Source   string `json:"source"`
	Path     string `json:"path"`
	Repo     string `json:"repo"`
	Ref      string `json:"ref"`
	Takeover bool   `json:"takeover"`
}

var builtins = map[string]string{
	"codex": "Codex", "claude": "Claude Code", "pi": "Pi", "orca": "Orca",
}

func New(dataDir string, bundledDir string) *Manager {
	return &Manager{root: filepath.Join(dataDir, "plugin-manager"), bundled: bundledDir}
}

// Enabled is a read-only gate for the three bridge methods. It never takes the
// management lock, so a package download does not delay a native hook.
func (m *Manager) Enabled(ctx context.Context, id string) (bool, error) {
	if id == "orca" || !pluginID.MatchString(id) {
		return false, nil
	}
	return m.enabled(ctx, id)
}

func (m *Manager) enabled(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	file, err := os.Open(m.stateFile())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if cached := m.gate.Load(); cached != nil && os.SameFile(cached.file, info) &&
		cached.file.ModTime() == info.ModTime() && cached.file.Size() == info.Size() {
		return cached.items[id], nil
	}
	state, err := readRegistry(file)
	if err != nil {
		return false, err
	}
	flags := &enabledFlags{file: info, items: map[string]bool{}}
	for key, r := range state.Items {
		flags.items[key] = r.Enabled && (state.Pending == nil || state.Pending.ID != key)
	}
	m.gate.Store(flags)
	return flags.items[id], nil
}

type enabledFlags struct {
	file  os.FileInfo
	items map[string]bool
}

func (m *Manager) Sync(ctx context.Context, id, expectedTitle, newTitle string) error {
	enabled, err := m.enabled(ctx, "orca")
	if err != nil {
		return err
	}
	if !enabled {
		return fmt.Errorf("%w：桌面已关闭 Orca 同步", orcasync.ErrSkipped)
	}
	return orcasync.Sync(ctx, id, expectedTitle, newTitle)
}

func (m *Manager) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == "plugins.list" {
		state, err := m.load()
		if err != nil {
			return nil, err
		}
		return struct {
			Items []Item `json:"items"`
		}{m.list(state)}, nil
	}
	var req request
	if len(params) == 0 || json.Unmarshal(params, &req) != nil ||
		(method != "plugins.install" && !pluginID.MatchString(req.ID)) {
		return nil, errors.New("插件请求参数无效")
	}
	switch method {
	case "plugins.install", "plugins.update":
		return m.install(ctx, method, req)
	case "plugins.enable", "plugins.disable", "plugins.restore":
		return m.locked(ctx, func(state registry) (any, error) { return m.modify(ctx, state, method, req) })
	default:
		return nil, errors.New("未知插件管理请求")
	}
}

func (m *Manager) list(state registry) []Item {
	items := make([]Item, 0, len(builtins)+len(state.Items))
	for _, id := range []string{"codex", "claude", "pi", "orca"} {
		items = append(items, m.item(state, id))
	}
	var ids []string
	for id := range state.Items {
		if builtins[id] == "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		items = append(items, m.item(state, id))
	}
	return items
}

func (m *Manager) item(state registry, id string) Item {
	r := state.Items[id]
	item := Item{
		ID: id, Name: r.Manifest.Name, Version: r.Manifest.Version, Enabled: r.Enabled,
		Kind: "community", Status: "disabled", CanRestore: r.Previous != nil || len(r.Takeover) > 0,
	}
	if r.Enabled {
		item.Status = "enabled"
	}
	if name := builtins[id]; name != "" {
		item.Name, item.Kind = name, "builtin"
		m.describeBuiltin(id, &item, r)
	} else {
		m.describeCommunity(&item, r)
	}
	if state.Pending != nil && state.Pending.ID == id {
		item.Enabled, item.CanRestore, item.Status = false, true, "recovery_required"
		item.Detail = "插件变更尚未完成。请选择恢复，或先处理宿主设置冲突。"
	}
	return item
}

func (m *Manager) describeCommunity(item *Item, r record) {
	dir := m.packageDir(r.release)
	if manifest, err := readManifest(dir); err != nil || manifest != r.Manifest {
		item.Status, item.Detail = "unavailable", "插件文件缺失或清单已改变。"
		return
	}
	item.EntryPath = filepath.Join(dir, filepath.FromSlash(r.Manifest.Entry))
	if r.Enabled {
		item.Detail = "插件已注册，由开发者自行启动入口。"
	}
}

func (m *Manager) describeBuiltin(id string, item *Item, r record) {
	if id == "orca" {
		item.Version = "1"
		item.Detail = "请在 Orca 设置中关闭「Name chats automatically」，避免两个应用同时改名。"
		return
	}
	manifest, bundleErr := readManifest(filepath.Join(m.bundled, id))
	if item.Version == "" && bundleErr == nil {
		item.Version = manifest.Version
	}
	if r.Enabled {
		m.describeEnabled(id, item, r)
		return
	}
	h, err := resolveHost(id)
	if err == nil {
		var before snapshot
		before, err = readSettings(h.settings)
		if err == nil {
			var changes []change
			changes, err = legacyChanges(h, before)
			item.TakeoverRequired = len(changes) > 0
		}
	}
	switch {
	case err != nil:
		item.Status, item.Detail = "action_required", "请检查宿主设置文件是否可读取。"
	case item.TakeoverRequired:
		item.Status, item.Detail = "action_required", "检测到已知的旧版自动命名插件，需明确选择接管。"
	case bundleErr != nil:
		item.Status, item.Detail = "unavailable", "缺少随桌面应用发行的适配器文件。"
	}
}

func (m *Manager) describeEnabled(id string, item *Item, r record) {
	h, err := resolveHost(id)
	if err == nil && len(r.Registration) != 0 && r.Registration[0].File == h.settings {
		var cur snapshot
		cur, err = readSettings(h.settings)
		if err == nil {
			var missing change
			missing, err = registration(h, cur, r.Entry, true)
			if err == nil && !missing.Before.equal(missing.After) {
				err = errors.New("宿主注册不完整")
			}
		}
	} else {
		err = errors.New("宿主配置目录已改变")
	}
	if _, manifestErr := readManifest(m.packageDir(r.release)); err != nil || manifestErr != nil {
		item.Status, item.Detail = "action_required", "宿主注册或适配器文件已改变，请停用后重新启用。"
		return
	}
	switch id {
	case "codex":
		item.Status, item.Detail = "action_required", "Hook 已注册。首次使用或脚本更新后，请在 Codex 的 /hooks 中审核并信任；当前信任状态由 Codex 管理。"
	case "claude", "pi":
		item.Detail = "已写入宿主设置。请重启宿主以加载适配器。"
	}
}

func (m *Manager) packageDir(rel release) string {
	return filepath.Join(m.root, "packages", rel.Dir)
}

func (m *Manager) install(ctx context.Context, method string, req request) (any, error) {
	if !filepath.IsAbs(m.root) {
		return nil, errors.New("插件数据目录须为绝对路径")
	}
	if req.Source != "local" && req.Source != "github" {
		return nil, errors.New("请选择本地目录或 GitHub 提交")
	}
	stage, err := stagePackage(m.root)
	if err != nil {
		return nil, err
	}
	if req.Source == "local" {
		err = copyLocal(ctx, req.Path, stage)
	} else {
		err = fetchGitHub(ctx, req.Repo, req.Ref, stage)
	}
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(stage))
	}
	result, err := m.locked(ctx, func(state registry) (any, error) {
		return m.installStaged(state, method, req, stage)
	})
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(stage))
	}
	return result, nil
}

func (m *Manager) installStaged(state registry, method string, req request, stage string) (any, error) {
	if state.Pending != nil {
		return nil, errors.New("请先恢复有未完成变更的插件")
	}
	manifest, err := readManifest(stage)
	if err != nil {
		return nil, err
	}
	if builtins[manifest.ID] != "" || manifest.Host != "external" {
		return nil, errors.New("社区插件须使用唯一 ID 和 external 宿主")
	}
	current, found := state.Items[manifest.ID]
	update := method == "plugins.update"
	if update && (req.ID != manifest.ID || !found || current.Enabled || current.Manifest.Version == manifest.Version) {
		return nil, errors.New("更新须对应已停用的插件，并提供不同版本")
	}
	if !update && found {
		return nil, errors.New("插件已安装，请使用更新")
	}
	next := record{release: release{Manifest: manifest, Dir: filepath.Base(stage)}}
	if update {
		next.Previous = &current.release
	}
	return m.commitItem(state, manifest.ID, next, nil)
}
