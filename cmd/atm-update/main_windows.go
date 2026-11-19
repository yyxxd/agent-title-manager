package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

// atm-update is launched by the desktop app after verification. It waits for
// the app to exit, runs the original NSIS installer and restarts the result.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: atm-update.exe <交接参数.json>")
		os.Exit(2)
	}
	handoff, err := update.ReadHandoff(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logPath := filepath.Join(handoff.DataDir, "update-helper.log")
	log, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if logErr == nil {
		defer log.Close()
		fmt.Fprintf(log, "start pid=%d version=%s previous=%s isolated=%v install=%s\n",
			handoff.AppPID, handoff.Version, handoff.PreviousVersion, handoff.Isolated, handoff.InstallDir)
	}
	started := time.Now()
	status := update.RunHandoffWithStatus(handoff, update.NewWindowsHelper(handoff).Deps(update.PublicKey()), func(status update.Status) error {
		return update.WriteStatus(handoff.DataDir, status)
	})
	if logErr == nil {
		fmt.Fprintf(log, "result ok=%v elapsed=%s message=%s\n",
			status.OK, time.Since(started).Round(time.Millisecond), status.Message)
	}
	if !status.OK {
		os.Exit(1)
	}
}
