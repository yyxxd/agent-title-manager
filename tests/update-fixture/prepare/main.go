// Command prepare-handoff drives the real update verification and starts the
// real helper for isolated acceptance runs. It is not part of shipped builds.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := flag.String("data", "", "isolated ATM_DATA_DIR")
	installDir := flag.String("install", "", "isolated install directory")
	release := flag.String("release", "", "fixture release URL")
	public := flag.String("public", "", "hex test public key")
	helper := flag.String("helper", "", "path to real atm-update.exe")
	current := flag.String("current", "0.2.0", "installed version")
	flag.Parse()
	if *dataDir == "" || *installDir == "" || *release == "" || *public == "" || *helper == "" {
		return fmt.Errorf("all arguments are required")
	}
	for _, path := range []string{*dataDir, *installDir, *helper} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("paths must be absolute: %s", path)
		}
	}
	source, err := update.NewSource(*release, *public)
	if err != nil {
		return err
	}
	check, err := source.CheckLatest(context.Background(), *current)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	updates := filepath.Join(*dataDir, "updates")
	if err := os.MkdirAll(updates, 0700); err != nil {
		return err
	}
	installerPath := filepath.Join(updates, check.Installer)
	file, err := os.Create(installerPath)
	if err != nil {
		return err
	}
	if err := source.Download(context.Background(), check, file, func(done, total int64) {
		fmt.Printf("progress %d/%d\n", done, total)
	}); err != nil {
		file.Close()
		return fmt.Errorf("download: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	manifestPath := filepath.Join(updates, "update-manifest.json")
	if err := os.WriteFile(manifestPath, check.Manifest, 0600); err != nil {
		return err
	}
	copyPath := filepath.Join(updates, "atm-update.exe")
	if err := copyFile(*helper, copyPath); err != nil {
		return err
	}
	handoff := update.Handoff{AppPID: uint32(os.Getpid()), Installer: installerPath,
		InstallDir: *installDir, DataDir: *dataDir, Version: check.Version,
		PreviousVersion: *current, Restart: filepath.Join(*installDir, "AgentTitleManager.exe"),
		Manifest: manifestPath, Isolated: true}
	data, err := json.Marshal(handoff)
	if err != nil {
		return err
	}
	paramPath := filepath.Join(*dataDir, "update-handoff.json")
	if err := os.WriteFile(paramPath, data, 0600); err != nil {
		return err
	}
	fmt.Printf("handoff prepared for %s, starting helper\n", check.Version)
	return startHelper(copyPath, paramPath)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}
