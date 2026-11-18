// Package engine implements the shared title RPC and its durable background work.
package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

type Engine struct {
	db                  *sql.DB
	dir                 string
	lock                *os.File
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	closeOnce           sync.Once
	closeErr            error
	modelWake, syncWake chan struct{}
	generate            func(context.Context, attemptRequest, func() error) modelResult
	syncer              func(context.Context, string, string, string) error
	isEnabled           func(context.Context, string) bool
	now                 func() time.Time
}

// Open starts one durable worker per queue. dataDir belongs to a single running engine.
func Open(dataDir string, syncer func(context.Context, string, string, string) error, enabled ...func(context.Context, string) bool) (*Engine, error) {
	if dataDir == "" {
		return nil, errors.New("缺少数据目录")
	}
	if len(enabled) > 1 {
		return nil, errors.New("只能提供一个启用判断")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	lock, err := lockDirectory(filepath.Join(dataDir, "engine.lock"))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "engine.sqlite3"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(db); err != nil {
		db.Close()
		lock.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{db: db, dir: dataDir, lock: lock, ctx: ctx, cancel: cancel, now: time.Now,
		modelWake: make(chan struct{}, 1), syncWake: make(chan struct{}, 1), syncer: syncer}
	e.generate = e.runProvider
	if len(enabled) == 1 {
		e.isEnabled = enabled[0]
	}
	e.wg.Add(1)
	go e.worker(e.modelWake, e.processModel)
	if syncer != nil {
		e.wg.Add(1)
		go e.worker(e.syncWake, e.processSync)
	}
	return e, nil
}

func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		e.cancel()
		e.wg.Wait()
		e.closeErr = errors.Join(e.db.Close(), e.lock.Close())
	})
	return e.closeErr
}

func (e *Engine) worker(wake chan struct{}, process func() (bool, error)) {
	defer e.wg.Done()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		for e.ctx.Err() == nil {
			worked, err := process()
			if err != nil && e.ctx.Err() == nil {
				log.Printf("标题队列无法推进: %v", err)
			}
			if err != nil || !worked {
				break
			}
		}
		select {
		case <-e.ctx.Done():
			return
		case <-wake:
		case <-poll.C:
		}
	}
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

type accepted struct {
	Accepted bool `json:"accepted"`
}

// Handle dispatches RPC for the trusted host. Bridge callers must pass a separate allowlist.
func (e *Engine) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if len(params) > 128<<10 {
		return nil, errors.New("请求过大")
	}
	switch method {
	case "event.submit":
		if err := requiredParams(params, "currentTitle", "turns"); err != nil {
			return nil, err
		}
		return dispatch(params, func(p submission) (accepted, error) { return e.submit(ctx, p) })
	case "title.claim":
		if err := requiredParams(params, "currentTitle"); err != nil {
			return nil, err
		}
		return dispatch(params, func(p claimRequest) (claimResponse, error) { return e.claim(ctx, p) })
	case "title.ack":
		return dispatch(params, func(p ackRequest) (accepted, error) { return e.ack(ctx, p) })
	case "settings.get":
		return e.getSettings(ctx)
	case "settings.save":
		return dispatch(params, func(p settingsPatch) (Settings, error) { return e.saveSettings(ctx, p) })
	case "providers.test":
		return dispatch(params, func(p providerTest) (testResult, error) { return e.testProvider(ctx, p) })
	case "stats.daily":
		return dispatch(params, func(p dateRequest) (dailyStats, error) { return e.daily(ctx, p) })
	case "records.list":
		return dispatch(params, func(p listRequest) (recordList, error) { return e.records(ctx, p) })
	case "sync.retry", "sync.adopt":
		return dispatch(params, func(p idRequest) (accepted, error) { return e.resync(ctx, p, method == "sync.adopt") })
	default:
		return nil, fmt.Errorf("未知方法 %q", method)
	}
}

func requiredParams(params json.RawMessage, names ...string) error {
	if !utf8.Valid(params) {
		return errors.New("参数必须使用 UTF-8")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return errors.New("参数必须为 JSON 对象")
	}
	for _, name := range names {
		value := fields[name]
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("缺少必填字段 %s", name)
		}
	}
	return nil
}

func dispatch[P any, R any](params json.RawMessage, fn func(P) (R, error)) (R, error) {
	var p P
	var zero R
	if len(params) > 0 {
		if !utf8.Valid(params) || !bytes.HasPrefix(bytes.TrimSpace(params), []byte{'{'}) || json.Unmarshal(params, &p) != nil {
			return zero, errors.New("参数必须为 JSON 对象，且字段类型必须匹配")
		}
	}
	return fn(p)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func transact[T any](ctx context.Context, db *sql.DB, fn func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	result, err := fn(tx)
	if err != nil {
		return zero, err
	}
	return result, tx.Commit()
}
