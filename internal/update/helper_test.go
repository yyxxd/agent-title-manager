package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRunHandoffSuccessOrder(t *testing.T) {
	var steps []string
	status := runHandoff(testHandoff(t), helperDeps{
		waitExit:     func(uint32) error { steps = append(steps, "wait"); return nil },
		prepare:      func(_, _ string) (func() error, error) { steps = append(steps, "backup"); return func() error { steps = append(steps, "restore"); return nil }, nil },
		install:      func(_, _ string, _ bool) error { steps = append(steps, "install"); return nil },
		checkVersion: func(_, _ string) error { steps = append(steps, "version"); return nil },
		verifyData:   func(string) error { steps = append(steps, "data"); return nil },
		restart:      func(string) error { steps = append(steps, "restart"); return nil },
	})
	if !status.OK || !reflect.DeepEqual(steps, []string{"wait", "backup", "install", "version", "data", "restart"}) {
		t.Fatalf("unexpected status or order: %+v %v", status, steps)
	}
}

func TestRunHandoffFailuresRestoreAndRestartOld(t *testing.T) {
	cases := []struct {
		name      string
		wantSteps []string
		breakStep func(*helperDeps)
	}{
		{"install", []string{"restore", "restart"}, func(d *helperDeps) { d.install = func(string, string, bool) error { return errors.New("boom") } }},
		{"version", []string{"install", "restore", "restart"}, func(d *helperDeps) { d.checkVersion = func(string, string) error { return errors.New("wrong") } }},
		{"data", []string{"install", "version", "restore", "restart"}, func(d *helperDeps) { d.verifyData = func(string) error { return errors.New("lost") } }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var steps []string
			deps := helperDeps{
				waitExit:     func(uint32) error { return nil },
				prepare:      func(_, _ string) (func() error, error) { return func() error { steps = append(steps, "restore"); return nil }, nil },
				install:      func(_, _ string, _ bool) error { steps = append(steps, "install"); return nil },
				checkVersion: func(_, _ string) error { steps = append(steps, "version"); return nil },
				verifyData:   func(string) error { steps = append(steps, "data"); return nil },
				restart:      func(string) error { steps = append(steps, "restart"); return nil },
			}
			tc.breakStep(&deps)
			status := runHandoff(testHandoff(t), deps)
			if status.OK || !reflect.DeepEqual(steps, tc.wantSteps) {
				t.Fatalf("failure not recovered: %+v %v", status, steps)
			}
		})
	}
}

func TestRunHandoffWaitTimeoutDoesNotInstall(t *testing.T) {
	installed := false
	status := runHandoff(testHandoff(t), helperDeps{
		waitExit: func(uint32) error { return errors.New("timeout") },
		prepare:  func(_, _ string) (func() error, error) { return nil, nil },
		install:  func(_, _ string, _ bool) error { installed = true; return nil },
		restart:  func(string) error { return nil },
	})
	if status.OK || installed {
		t.Fatalf("should not install after wait failure: %+v %v", status, installed)
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
	got, err := ReadStatus(dir)
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
		DataDir: dir, Version: "0.1.7", Restart: filepath.Join(dir, "app", "AgentTitleManager.exe"), Isolated: true}
}
