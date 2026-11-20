package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

func runRepair(args []string) error {
	flags := flag.NewFlagSet("repair-notes", flag.ContinueOnError)
	version := flags.String("version", "", "要修复的发布版本号 X.Y.Z")
	existing := flags.String("existing", "", "已签名的原清单路径")
	installer := flags.String("installer", "", "已发布安装包路径")
	notesPath := flags.String("notes-file", "", "UTF-8 更新说明文件")
	output := flags.String("output", "", "输出清单的路径")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *version == "" || *existing == "" || *installer == "" || *notesPath == "" || *output == "" {
		return errors.New("必须指定 version、existing、installer、notes-file 和 output")
	}

	public, err := update.ParsePublicKey(os.Getenv("UPDATE_PUBLIC_KEY"))
	if err != nil {
		return err
	}
	private, err := signingKey()
	if err != nil {
		return err
	}
	if !public.Equal(private.Public()) {
		return errors.New("签名私钥与可信公钥不匹配")
	}

	raw, err := os.ReadFile(*existing)
	if err != nil {
		return err
	}
	var manifest update.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	if err := verifyPublishedInstaller(public, manifest, *version, *installer); err != nil {
		return err
	}
	manifest.Notes, err = readNotes(*notesPath)
	if err != nil {
		return err
	}
	return writeSignedManifest(*output, private, manifest)
}

func verifyPublishedInstaller(public ed25519.PublicKey, manifest update.Manifest, version, installer string) error {
	if err := update.Verify(public, manifest); err != nil {
		return fmt.Errorf("原清单验签失败: %w", err)
	}
	if manifest.Version != version {
		return errors.New("原清单与待修复版本不一致")
	}
	expectedFile := "AgentTitleManager-" + version + "-windows-x64-setup.exe"
	if manifest.File != expectedFile || filepath.Base(installer) != expectedFile {
		return errors.New("原清单或下载的安装包文件名与待修复版本不一致")
	}

	file, err := os.Open(installer)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return err
	}
	if size != manifest.Size || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(manifest.SHA256) {
		return errors.New("已发布安装包的实际大小或 SHA-256 与原清单不一致")
	}
	return nil
}
