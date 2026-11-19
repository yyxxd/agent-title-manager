package pluginmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxSettings = 4 << 20

type release struct {
	Manifest Manifest `json:"manifest"`
	Dir      string   `json:"dir"`
}

type record struct {
	release
	Enabled      bool     `json:"enabled"`
	Entry        string   `json:"entry,omitempty"`
	Registration []change `json:"registration,omitempty"`
}

type snapshot struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
}

func (s snapshot) equal(other snapshot) bool {
	return s.Exists == other.Exists && bytes.Equal(s.Data, other.Data)
}

type change struct {
	File   string   `json:"file"`
	Before snapshot `json:"before,omitempty"`
	After  snapshot `json:"after,omitempty"`
}

func (c change) reverse() change {
	c.Before, c.After = c.After, c.Before
	return c
}

type transaction struct {
	ID        string   `json:"id"`
	Next      record   `json:"-"`
	Changes   []change `json:"changes"`
	Attempted int      `json:"attempted"`
}

type registry struct {
	Items   map[string]record `json:"items"`
	Pending *transaction      `json:"pending,omitempty"`
}

var registryLock sync.Mutex
var settingsConflict = errors.New("宿主配置有并发修改，已保留现有内容")

func (m *Manager) load() (registry, error) {
	file, err := os.Open(m.stateFile())
	if errors.Is(err, os.ErrNotExist) {
		return registry{Items: map[string]record{}}, nil
	}
	if err != nil {
		return registry{}, err
	}
	defer file.Close()
	return readRegistry(file)
}

func readRegistry(file *os.File) (registry, error) {
	var state registry
	const limit = 128 << 20
	wire, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return state, err
	}
	if len(wire) > limit || json.Unmarshal(wire, &state) != nil || state.Items == nil ||
		(state.Pending != nil && (state.Pending.Attempted < 0 || state.Pending.Attempted > len(state.Pending.Changes))) {
		return state, errors.New("插件设置无法解析")
	}
	return state, nil
}

func (m *Manager) stateFile() string { return filepath.Join(m.root, "state.json") }

func (m *Manager) save(state registry) error {
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeAtomic(m.stateFile(), content, 0600)
}

func (m *Manager) locked(ctx context.Context, fn func(registry) (any, error)) (any, error) {
	registryLock.Lock()
	defer registryLock.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(m.root, "state.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := lockRegistry(f); err != nil {
		return nil, err
	}
	state, err := m.load()
	if err != nil {
		return nil, err
	}
	return fn(state)
}

func (m *Manager) execute(state registry, tx transaction) error {
	if len(tx.Changes) == 0 {
		state.Items[tx.ID] = tx.Next
		return m.save(state)
	}
	state.Pending = &tx
	for i, action := range tx.Changes {
		tx.Attempted = i + 1
		if err := m.save(state); err != nil {
			return errors.Join(err, m.rollback(state, i))
		}
		if err := action.apply(); err != nil {
			tx.Attempted = i
			if i > 0 {
				err = errors.Join(err, m.save(state))
			}
			return errors.Join(err, m.rollback(state, i))
		}
	}
	original, had := state.Items[tx.ID]
	state.Items[tx.ID] = tx.Next
	state.Pending = nil
	if err := m.save(state); err != nil {
		if had {
			state.Items[tx.ID] = original
		} else {
			delete(state.Items, tx.ID)
		}
		state.Pending = &tx
		return errors.Join(err, m.rollback(state, len(tx.Changes)))
	}
	return nil
}

func (m *Manager) rollback(state registry, applied int) error {
	for i := applied - 1; i >= 0; i-- {
		if err := state.Pending.Changes[i].reverse().apply(); err != nil {
			return fmt.Errorf("回滚未完成变更失败：%w", err)
		}
	}
	state.Pending = nil
	return m.save(state)
}

func (c change) apply() error {
	actual, err := readSettings(c.File)
	if err != nil {
		return err
	}
	if actual.equal(c.After) {
		return nil
	}
	if !actual.equal(c.Before) {
		return settingsConflict
	}
	if !c.After.Exists {
		return os.Remove(c.File)
	}
	mode := os.FileMode(0600)
	if info, err := os.Lstat(c.File); err == nil {
		mode = info.Mode().Perm()
	}
	return writeChecked(c.File, c.After.Data, mode, func() error {
		latest, err := readSettings(c.File)
		if err == nil && !latest.equal(c.Before) {
			return settingsConflict
		}
		return err
	})
}

func readSettings(file string) (snapshot, error) {
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot{}, nil
	}
	if err != nil {
		return snapshot{}, err
	}
	if !info.Mode().IsRegular() || isReparse(info) {
		return snapshot{}, errors.New("宿主设置须为普通文件")
	}
	wire, err := readBounded(file, maxSettings)
	return snapshot{Exists: true, Data: wire}, err
}

func writeAtomic(file string, content []byte, mode os.FileMode) error {
	return writeChecked(file, content, mode, nil)
}

func writeChecked(file string, content []byte, mode os.FileMode, check func() error) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	stage, err := os.CreateTemp(dir, ".atm-*")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	err = stage.Chmod(mode)
	if err == nil {
		_, err = stage.Write(content)
	}
	if err == nil {
		err = stage.Sync()
	}
	if err := errors.Join(err, stage.Close()); err != nil {
		return err
	}
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	return replaceFile(stage.Name(), file)
}

func (m *Manager) recoverPending(state registry, id string) error {
	if state.Pending.ID != id {
		return errors.New("请先停用以回滚有未完成变更的插件")
	}
	return m.rollback(state, state.Pending.Attempted)
}

func stagePackage(root string) (string, error) {
	parent := filepath.Join(root, "packages")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "pkg-"+time.Now().UTC().Format("20060102")+"-")
}
