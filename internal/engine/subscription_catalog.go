package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type subscriptionModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	ResolvedModel string   `json:"resolvedModel,omitempty"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
}

type subscriptionCatalog struct {
	Items        []subscriptionModel `json:"items"`
	Notice       string              `json:"notice,omitempty"`
	AuthRequired bool                `json:"authRequired,omitempty"`
}

type subscriptionRequest struct {
	Provider string `json:"provider"`
	Refresh  bool   `json:"refresh,omitempty"`
}

type subscriptionSnapshot struct {
	sequence    uint64
	fingerprint string
	catalog     subscriptionCatalog
	err         error
}

// catalogFingerprint 标识一份 CLI 目录对应的可执行文件版本。CLI 升级后路径、大小或修改时间会变化。
func catalogFingerprint(provider string) string {
	var binary string
	switch provider {
	case "codex":
		binary, _ = codexExecutable()
	case "claude":
		binary, _ = claudeExecutable()
	default:
		return ""
	}
	if binary == "" {
		return ""
	}
	info, err := os.Stat(binary)
	if err != nil {
		return ""
	}
	return binary + "|" + strconv.FormatInt(info.Size(), 10) + "|" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// cachedSubscription 默认复用与当前 CLI 版本匹配的模型目录，只有 CLI 升级或手动刷新时才重新读取。
func (e *Engine) cachedSubscription(ctx context.Context, provider string, refresh bool) (subscriptionCatalog, error) {
	fingerprint := catalogFingerprint(provider)
	if !refresh {
		e.catalogMu.Lock()
		snapshot := e.catalogSnapshots[provider]
		e.catalogMu.Unlock()
		// 未登录结果不缓存，登录状态变化后可立即重新读取。
		if snapshot.err == nil && !snapshot.catalog.AuthRequired && snapshot.fingerprint == fingerprint {
			return snapshot.catalog, nil
		}
	}
	return e.refreshSubscriptionFor(ctx, provider, fingerprint)
}

func (e *Engine) refreshSubscriptionFor(ctx context.Context, provider, fingerprint string) (subscriptionCatalog, error) {
	if provider != "codex" && provider != "claude" {
		return subscriptionCatalog{}, errors.New("请选择原生 CLI 来源")
	}
	e.catalogMu.Lock()
	e.catalogSequence++
	snapshot := subscriptionSnapshot{sequence: e.catalogSequence, fingerprint: fingerprint}
	e.catalogMu.Unlock()
	snapshot.catalog, snapshot.err = e.catalog(ctx, provider)
	e.catalogMu.Lock()
	defer e.catalogMu.Unlock()
	if current := e.catalogSnapshots[provider]; current.sequence > snapshot.sequence {
		return current.catalog, current.err
	}
	if e.catalogSnapshots == nil {
		e.catalogSnapshots = make(map[string]subscriptionSnapshot, 2)
	}
	e.catalogSnapshots[provider] = snapshot
	if snapshot.err == nil && !snapshot.catalog.AuthRequired && fingerprint != "" {
		e.saveCatalogCacheLocked()
	}
	return snapshot.catalog, snapshot.err
}

func (e *Engine) subscriptionForValidation(ctx context.Context, provider string) (subscriptionCatalog, error) {
	return e.cachedSubscription(ctx, provider, false)
}

const catalogCacheVersion = 1

type catalogCacheFile struct {
	Version int                          `json:"version"`
	Entries map[string]catalogCacheEntry `json:"entries"`
}

type catalogCacheEntry struct {
	Fingerprint string              `json:"fingerprint"`
	Catalog     subscriptionCatalog `json:"catalog"`
}

func (e *Engine) catalogCachePath() string {
	return filepath.Join(e.dir, "model-catalogs.json")
}

// loadCatalogCache 读取上次成功读取的目录。缓存损坏或版本不符时按未缓存处理。
func loadCatalogCache(dir string) map[string]subscriptionSnapshot {
	data, err := os.ReadFile(filepath.Join(dir, "model-catalogs.json"))
	if err != nil {
		return nil
	}
	var file catalogCacheFile
	if json.Unmarshal(data, &file) != nil || file.Version != catalogCacheVersion {
		return nil
	}
	snapshots := make(map[string]subscriptionSnapshot, len(file.Entries))
	for provider, entry := range file.Entries {
		if (provider != "codex" && provider != "claude") || entry.Fingerprint == "" {
			continue
		}
		snapshots[provider] = subscriptionSnapshot{fingerprint: entry.Fingerprint, catalog: entry.Catalog}
	}
	return snapshots
}

// saveCatalogCacheLocked 原子写入当前内存中的有效目录，调用方需持有 catalogMu。
func (e *Engine) saveCatalogCacheLocked() {
	file := catalogCacheFile{Version: catalogCacheVersion, Entries: make(map[string]catalogCacheEntry, len(e.catalogSnapshots))}
	for provider, snapshot := range e.catalogSnapshots {
		if snapshot.err != nil || snapshot.catalog.AuthRequired || snapshot.fingerprint == "" {
			continue
		}
		file.Entries[provider] = catalogCacheEntry{Fingerprint: snapshot.fingerprint, Catalog: snapshot.catalog}
	}
	data, err := json.Marshal(file)
	if err != nil {
		log.Printf("无法编码模型目录缓存: %v", err)
		return
	}
	temp, err := os.CreateTemp(e.dir, "model-catalogs-*.tmp")
	if err != nil {
		log.Printf("无法写入模型目录缓存: %v", err)
		return
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		log.Printf("无法写入模型目录缓存: %v", err)
		return
	}
	if err := temp.Close(); err != nil {
		log.Printf("无法写入模型目录缓存: %v", err)
		return
	}
	if err := os.Rename(name, e.catalogCachePath()); err != nil {
		log.Printf("无法写入模型目录缓存: %v", err)
	}
}

var (
	errCatalogProtocol = errors.New("此 CLI 版本无法读取模型目录，请更新 CLI")
	errCatalogTimeout  = errors.New("读取模型目录超时，请刷新重试")
)

type subscriptionLoginError string

func (e subscriptionLoginError) Error() string { return string(e) }

func subscriptionAuthError(auth modelResult) error {
	if auth.failure == failureCodexLogin || auth.failure == failureClaudeLogin {
		return subscriptionLoginError(auth.message)
	}
	return errors.New(auth.message)
}

func (e *Engine) subscriptionModels(ctx context.Context, provider string) (subscriptionCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp(e.dir, "subscription-catalog-")
	if err != nil {
		return subscriptionCatalog{}, errors.New("无法创建临时目录")
	}
	defer os.RemoveAll(dir)

	var items []subscriptionModel
	switch provider {
	case "codex":
		items, err = codexModels(ctx, dir)
	case "claude":
		items, err = claudeModels(ctx, dir)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return subscriptionCatalog{}, errCatalogTimeout
	}
	var login subscriptionLoginError
	if errors.As(err, &login) {
		return subscriptionCatalog{Items: []subscriptionModel{}, Notice: login.Error(), AuthRequired: true}, nil
	}
	return subscriptionCatalog{Items: cleanCatalog(items)}, err
}

func cleanCatalog(items []subscriptionModel) []subscriptionModel {
	result := make([]subscriptionModel, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, model := range items {
		if !validNativeValue(model.ID) || model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		efforts := make([]string, 0, len(model.Efforts))
		for _, effort := range model.Efforts {
			if effort != "" && validNativeValue(effort) && !slices.Contains(efforts, effort) {
				efforts = append(efforts, effort)
			}
		}
		model.Efforts = efforts
		if !slices.Contains(efforts, model.DefaultEffort) {
			model.DefaultEffort = ""
		}
		result = append(result, model)
	}
	return result
}

func validNativeValue(value string) bool {
	return validString(value, 256) && strings.TrimSpace(value) == value && !strings.HasPrefix(value, "-")
}

func validateSubscription(items []subscriptionModel, model, effort string) error {
	if model == "" {
		return errors.New("请先选择命名模型")
	}
	for _, item := range items {
		if item.ID == model {
			if effort != "" && !slices.Contains(item.Efforts, effort) {
				return errors.New("该模型不支持选中的思考强度，请重新选择")
			}
			return nil
		}
	}
	return errors.New("所选模型已不在当前 CLI 目录中，请重新选择")
}

type cliStream struct {
	input *json.Encoder
	lines *bufio.Scanner
}

func readCLIStream[T any](cmd *exec.Cmd, read func(cliStream) (T, error)) (T, error) {
	var zero T
	in, err := cmd.StdinPipe()
	if err != nil {
		return zero, errCatalogProtocol
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return zero, errCatalogProtocol
	}
	defer out.Close()
	if err := cmd.Start(); err != nil {
		return zero, errors.New("无法启动本机 CLI")
	}
	defer func() {
		in.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()
	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 4096), 1<<20)
	return read(cliStream{json.NewEncoder(in), lines})
}

func (s cliStream) send(msg any) error {
	if err := s.input.Encode(msg); err != nil {
		return errCatalogProtocol
	}
	return nil
}

func readCLIReply[T any](s cliStream, match func(T) bool) (T, error) {
	for s.lines.Scan() {
		var frame T
		if json.Unmarshal(s.lines.Bytes(), &frame) == nil && match(frame) {
			return frame, nil
		}
	}
	var zero T
	return zero, errCatalogProtocol
}

func (e *Engine) checkSubscription(ctx context.Context, s Settings) error {
	model, effort := s.nativeChoice(s.Provider)
	catalog, err := e.subscriptionForValidation(ctx, s.Provider)
	if err != nil {
		return fmt.Errorf("无法验证命名模型：%w", err)
	}
	if catalog.AuthRequired {
		return errors.New(catalog.Notice)
	}
	return validateSubscription(catalog.Items, model, effort)
}
