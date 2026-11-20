// Command atm-sign produces a signed update manifest for a release asset.
// The private key is read from the environment and never printed or logged.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yyxxd/agent-title-manager/internal/update"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "repair-notes" {
		return runRepair(os.Args[2:])
	}
	version := flag.String("version", "", "发布版本号 X.Y.Z")
	file := flag.String("file", "", "安装包文件名")
	size := flag.String("size", "", "安装包字节数")
	sha := flag.String("sha256", "", "安装包 SHA-256")
	output := flag.String("output", "", "输出的 update-manifest.json 路径")
	notesFile := flag.String("notes-file", "", "UTF-8 更新说明文件，优先于 ATM_RELEASE_NOTES")
	flag.Parse()
	private, err := signingKey()
	if err != nil {
		return err
	}
	sizeValue, err := strconv.ParseInt(*size, 10, 64)
	if err != nil || sizeValue <= 0 {
		return errors.New("size 必须为正整数")
	}
	if *version == "" || *file == "" || len(*sha) != 64 || *output == "" {
		return errors.New("version、file、sha256、output 均为必填")
	}
	notes := os.Getenv("ATM_RELEASE_NOTES")
	if *notesFile != "" {
		notes, err = readNotes(*notesFile)
		if err != nil {
			return err
		}
	}
	manifest := update.Manifest{Version: *version, Arch: "windows-x64", File: *file,
		Size: sizeValue, SHA256: strings.ToLower(*sha), Notes: notes}
	return writeSignedManifest(*output, private, manifest)
}

func signingKey() (ed25519.PrivateKey, error) {
	keyHex := os.Getenv("ATM_SIGNING_KEY")
	if keyHex == "" {
		return nil, errors.New("缺少 ATM_SIGNING_KEY 环境变量")
	}
	seed, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("ATM_SIGNING_KEY 必须是 64 字符十六进制 Ed25519 种子")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func readNotes(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) || strings.TrimSpace(string(data)) == "" {
		return "", errors.New("更新说明必须是非空 UTF-8 文本")
	}
	return string(data), nil
}

func writeSignedManifest(output string, private ed25519.PrivateKey, manifest update.Manifest) error {
	signature, err := update.Sign(private, manifest)
	if err != nil {
		return err
	}
	manifest.Signature = signature
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(data, '\n'), 0600)
}
