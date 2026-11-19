package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// verifyHandoff re-validates everything immediately before installation. The
// handoff file and installer live in a writable directory between the two
// processes, so they are untrusted again here; the public key is compiled into
// the helper and cannot be swapped by changing the handoff.
func verifyHandoff(h Handoff, publicKeyHex string) error {
	public, err := ParsePublicKey(publicKeyHex)
	if err != nil {
		return ErrNoTrustRoot
	}
	raw, err := os.ReadFile(h.Manifest)
	if err != nil {
		return fmt.Errorf("重新读取更新清单失败: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("重新解析更新清单失败: %w", err)
	}
	if err := Verify(public, manifest); err != nil {
		return fmt.Errorf("重新验签失败: %w", err)
	}
	if manifest.Version != h.Version {
		return fmt.Errorf("%w: 交接版本与签名清单不一致", ErrUntrusted)
	}
	if filepath.Base(h.Installer) != manifest.File {
		return fmt.Errorf("%w: 安装包文件名与清单不一致", ErrUntrusted)
	}
	info, err := os.Stat(h.Installer)
	if err != nil {
		return fmt.Errorf("读取安装包失败: %w", err)
	}
	if info.Size() != manifest.Size {
		return fmt.Errorf("%w: 安装包大小与清单不一致", ErrHashMismatch)
	}
	file, err := os.Open(h.Installer)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(manifest.SHA256) {
		return fmt.Errorf("%w: 安装包哈希与清单不一致", ErrHashMismatch)
	}
	// The target must be the directory the handoff named, never a path derived
	// from the manifest. Require it to exist as a directory and be absolute.
	if !filepath.IsAbs(h.InstallDir) {
		return errors.New("安装目标必须为绝对路径")
	}
	if info, err := os.Stat(h.InstallDir); err != nil || !info.IsDir() {
		return fmt.Errorf("安装目标不存在: %v", err)
	}
	if target := filepath.Join(h.InstallDir, "AgentTitleManager.exe"); !strings.EqualFold(target, h.Restart) {
		return errors.New("重启目标与安装目录不一致")
	}
	return nil
}
