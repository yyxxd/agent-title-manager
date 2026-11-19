package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

// updatePublicKey is injected at build time and is the helper's only trust
// root; the handoff file itself is untrusted and re-verified before install.
var updatePublicKey string

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
	status := update.RunHandoff(handoff, update.NewWindowsHelper(handoff).Deps(updatePublicKey))
	if logErr == nil {
		fmt.Fprintf(log, "result ok=%v elapsed=%s message=%s\n",
			status.OK, time.Since(started).Round(time.Millisecond), status.Message)
	}
	if err := update.WriteStatus(handoff.DataDir, status); err != nil {
		fmt.Fprintln(os.Stderr, "写入更新结果失败:", err)
	}
	if !status.OK {
		os.Exit(1)
	}
}
