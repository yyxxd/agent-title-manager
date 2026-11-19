package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func okDeps(steps *[]string) helperDeps {
	return helperDeps{
		verify:   func() error { record(steps, "verify"); return nil },
		waitExit: func(uint32) error { record(steps, "wait"); return nil },
		prepare: func(_, _ string) (func() error, error) {
			record(steps, "backup")
			return func() error { record(steps, "restore"); return nil }, nil
		},
		install:       func(_, _ string, _ bool) error { record(steps, "install"); return nil },
		checkVersion:  func(_, version string) error { record(steps, "version:"+version); return nil },
		verifyData:    func(string) error { record(steps, "data"); return nil },
		verifyVersion: func(_, version string) error { record(steps, "registry:"+version); return nil },
		restart:       func(string) error { record(steps, "restart"); return nil },
	}
}

func record(steps *[]string, value string) {
	if steps != nil {
		*steps = append(*steps, value)
	}
}

func TestRunHandoffSuccessOrder(t *testing.T) {
	var steps []string
	status := runHandoff(testHandoff(t), okDeps(&steps))
	want := []string{"verify", "wait", "backup", "install", "version:0.1.7", "data", "registry:0.1.7", "restart"}
	if !status.OK || !reflect.DeepEqual(steps, want) {
		t.Fatalf("unexpected status or order: %+v %v", status, steps)
	}
}

func TestRunHandoffWritesStatusBeforeRestart(t *testing.T) {
	var steps []string
	deps := okDeps(&steps)
	statusWritten := false
	deps.restart = func(string) error {
		if !statusWritten {
			t.Fatal("restart happened before status write")
		}
		record(&steps, "restart")
		return nil
	}
	status := runHandoffWithStatus(testHandoff(t), deps, func(Status) error {
		statusWritten = true
		record(&steps, "status")
		return nil
	})
	if !status.OK || !reflect.DeepEqual(steps, []string{"verify", "wait", "backup", "install", "version:0.1.7", "data", "registry:0.1.7", "status", "restart"}) {
		t.Fatalf("status must be written before restart: %+v %v", status, steps)
	}
}

func TestRunHandoffStatusWriteFailureDoesNotRestart(t *testing.T) {
	var steps []string
	deps := okDeps(&steps)
	status := runHandoffWithStatus(testHandoff(t), deps, func(Status) error {
		record(&steps, "status")
		return errors.New("disk full")
	})
	if status.OK || !strings.Contains(status.Message, "status write failed") || strings.Contains(strings.Join(steps, ","), "restart") {
		t.Fatalf("status write failure must stop restart: %+v %v", status, steps)
	}
}

func TestRunHandoffVerifyFailureStopsBeforeInstall(t *testing.T) {
	var steps []string
	deps := okDeps(&steps)
	deps.verify = func() error { record(&steps, "verify-fail"); return errors.New("tampered") }
	status := runHandoff(testHandoff(t), deps)
	if status.OK || !strings.Contains(status.Message, "未安装") ||
		!reflect.DeepEqual(steps, []string{"verify-fail"}) {
		t.Fatalf("verify failure should stop immediately: %+v %v", status, steps)
	}
}

func TestRunHandoffFailuresRestoreAndRestartOld(t *testing.T) {
	cases := []struct {
		name      string
		wantSteps []string
		breakStep func(*helperDeps)
	}{
		{"install", []string{"verify", "wait", "backup", "restore", "version:0.1.6", "restart"},
			func(d *helperDeps) { d.install = func(string, string, bool) error { return errors.New("boom") } }},
		{"version", []string{"verify", "wait", "backup", "install", "restore", "restart"},
			func(d *helperDeps) {
				d.checkVersion = func(_, version string) error {
					if version == "0.1.7" {
						return errors.New("wrong")
					}
					return nil
				}
			}},
		{"data", []string{"verify", "wait", "backup", "install", "version:0.1.7", "restore", "version:0.1.6", "restart"},
			func(d *helperDeps) { d.verifyData = func(string) error { return errors.New("lost") } }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var steps []string
			deps := okDeps(&steps)
			tc.breakStep(&deps)
			status := runHandoff(testHandoff(t), deps)
			if status.OK || !reflect.DeepEqual(steps, tc.wantSteps) {
				t.Fatalf("failure not recovered: %+v %v", status, steps)
			}
		})
	}
}

func TestRunHandoffFailedRollbackDoesNotRestart(t *testing.T) {
	var steps []string
	deps := okDeps(&steps)
	deps.install = func(string, string, bool) error { return errors.New("boom") }
	deps.prepare = func(_, _ string) (func() error, error) {
		return func() error { record(&steps, "restore-fail"); return errors.New("locked") }, nil
	}
	status := runHandoff(testHandoff(t), deps)
	if status.OK || strings.Contains(status.Message, "旧版本已恢复") ||
		strings.Contains(strings.Join(steps, ","), "restart") {
		t.Fatalf("failed rollback must not claim success or restart: %+v %v", status, steps)
	}
}

func TestRunHandoffWaitTimeoutDoesNotInstall(t *testing.T) {
	var steps []string
	deps := okDeps(&steps)
	deps.waitExit = func(uint32) error { return errors.New("timeout") }
	status := runHandoff(testHandoff(t), deps)
	if status.OK || strings.Contains(strings.Join(steps, ","), "install") {
		t.Fatalf("should not install after wait failure: %+v %v", status, steps)
	}
}

func TestRunHandoffBackupFailureKeepsBackupAndDoesNotInstall(t *testing.T) {
	var steps []string
	deps := okDeps(&steps)
	deps.prepare = func(_, _ string) (func() error, error) {
		return nil, errors.New("database backup corrupt")
	}
	status := runHandoff(testHandoff(t), deps)
	if status.OK || !strings.Contains(status.Message, "备份保留") ||
		strings.Contains(strings.Join(steps, ","), "install") {
		t.Fatalf("backup failure should abort and report: %+v %v", status, steps)
	}
}

func TestHandoffRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := testHandoff(t)
	path := filepath.Join(dir, "handoff.json")
	data, _ := json.Marshal(want)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadHandoff(path)
	if err != nil || got != want {
		t.Fatalf("round trip failed: %+v %v", got, err)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Status{OK: true, Version: "0.1.7", Message: "已更新至 0.1.7"}
	if err := WriteStatus(dir, want); err != nil {
		t.Fatal(err)
	}
	peek, err := ReadStatus(dir)
	if err != nil || peek != want {
		t.Fatalf("status peek failed: %+v %v", peek, err)
	}
	if _, err := os.Stat(StatusPath(dir)); err != nil {
		t.Fatal("status file should be retained until consumed")
	}
	got, err := ConsumeStatus(dir)
	if err != nil || got != want {
		t.Fatalf("status round trip failed: %+v %v", got, err)
	}
	if _, err := os.Stat(StatusPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status file should be consumed")
	}
}

func testHandoff(t *testing.T) Handoff {
	t.Helper()
	dir := t.TempDir()
	return Handoff{AppPID: 123, Installer: filepath.Join(dir, "setup.exe"), InstallDir: filepath.Join(dir, "app"),
		DataDir: dir, Version: "0.1.7", PreviousVersion: "0.1.6",
		Restart:  filepath.Join(dir, "app", "AgentTitleManager.exe"),
		Manifest: filepath.Join(dir, "update-manifest.json"), Isolated: true}
}
