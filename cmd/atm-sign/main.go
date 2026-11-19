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

	"github.com/yyxxd/agent-title-manager/internal/update"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "发布版本号 X.Y.Z")
	file := flag.String("file", "", "安装包文件名")
	size := flag.String("size", "", "安装包字节数")
	sha := flag.String("sha256", "", "安装包 SHA-256")
	output := flag.String("output", "", "输出的 update-manifest.json 路径")
	flag.Parse()
	keyHex := os.Getenv("ATM_SIGNING_KEY")
	if keyHex == "" {
		return errors.New("缺少 ATM_SIGNING_KEY 环境变量")
	}
	seed, err := hex.DecodeString(strings.TrimSpace(keyHex))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("ATM_SIGNING_KEY 必须是 64 字符十六进制 Ed25519 种子")
	}
	sizeValue, err := strconv.ParseInt(*size, 10, 64)
	if err != nil || sizeValue <= 0 {
		return errors.New("size 必须为正整数")
	}
	if *version == "" || *file == "" || len(*sha) != 64 || *output == "" {
		return errors.New("version、file、sha256、output 均为必填")
	}
	private := ed25519.NewKeyFromSeed(seed)
	manifest := update.Manifest{Version: *version, Arch: "windows-x64", File: *file,
		Size: sizeValue, SHA256: strings.ToLower(*sha), Notes: os.Getenv("ATM_RELEASE_NOTES")}
	signature, err := update.Sign(private, manifest)
	if err != nil {
		return err
	}
	manifest.Signature = signature
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*output, append(data, '\n'), 0600)
}
