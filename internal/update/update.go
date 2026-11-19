// Package update checks signed GitHub Releases and prepares verified updates.
// The release manifest is the trust boundary: it is signed with Ed25519 and
// binds version, architecture, file name, size and SHA-256 of the installer.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Release is the subset of a GitHub Release used by the updater.
type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
	Body       string  `json:"body"`
}

type Asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Manifest is the signed update descriptor published as a release asset.
// Signature covers the canonical JSON of every other field.
type Manifest struct {
	Version   string `json:"version"`
	Arch      string `json:"arch"`
	File      string `json:"file"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Notes     string `json:"notes"`
	Signature string `json:"signature"`
}

// Check describes a newer stable release available for this platform.
type Check struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	Installer   string `json:"installer"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	DownloadURL string `json:"-"`
	Manifest    []byte `json:"-"`
}

const (
	manifestName = "update-manifest.json"
	archWindows  = "windows-x64"
)

var (
	ErrNoUpdate     = errors.New("当前已是最新版本")
	ErrNoTrustRoot  = errors.New("正式更新公钥未配置，更新暂不可用")
	ErrUntrusted    = errors.New("更新签名校验失败")
	ErrHashMismatch = errors.New("安装包校验失败")
)

// updatePublicKey is injected into the helper at build time and is its only
// trust root for the final pre-install verification.
var updatePublicKey string

// PublicKey returns the build-injected update trust root.
func PublicKey() string { return updatePublicKey }

// Newer compares two X.Y.Z versions; it rejects malformed versions.
func Newer(current, candidate string) (bool, error) {
	a, err := parseVersion(current)
	if err != nil {
		return false, err
	}
	b, err := parseVersion(candidate)
	if err != nil {
		return false, err
	}
	for i := range a {
		if a[i] != b[i] {
			return b[i] > a[i], nil
		}
	}
	return false, nil
}

func parseVersion(value string) ([3]uint64, error) {
	parts := strings.Split(value, ".")
	var out [3]uint64
	if len(parts) != 3 {
		return out, fmt.Errorf("版本号格式无效: %q", value)
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return out, fmt.Errorf("版本号格式无效: %q", value)
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return out, fmt.Errorf("版本号格式无效: %q", value)
		}
		out[i] = n
	}
	return out, nil
}

// SignedBytes returns the canonical payload the signature covers.
func SignedBytes(m Manifest) []byte {
	m.Signature = ""
	raw, _ := json.Marshal(m)
	return raw
}

// Sign signs the manifest with the given private key and returns hex signature.
func Sign(private ed25519.PrivateKey, m Manifest) (string, error) {
	if len(private) != ed25519.PrivateKeySize {
		return "", errors.New("私钥无效")
	}
	return hex.EncodeToString(ed25519.Sign(private, SignedBytes(m))), nil
}

// Verify checks the manifest signature and that it binds this platform.
func Verify(public ed25519.PublicKey, m Manifest) error {
	if len(public) != ed25519.PublicKeySize {
		return ErrUntrusted
	}
	sig, err := hex.DecodeString(m.Signature)
	if err != nil || !ed25519.Verify(public, SignedBytes(m), sig) {
		return ErrUntrusted
	}
	if m.Arch != archWindows {
		return fmt.Errorf("%w: 架构不匹配", ErrUntrusted)
	}
	if _, err := parseVersion(m.Version); err != nil {
		return fmt.Errorf("%w: 版本无效", ErrUntrusted)
	}
	if m.File == "" || m.SHA256 == "" || m.Size <= 0 {
		return fmt.Errorf("%w: 清单字段不完整", ErrUntrusted)
	}
	if strings.ContainsAny(m.File, `/\`) || filepath.Base(m.File) != m.File {
		return fmt.Errorf("%w: 文件名不合法", ErrUntrusted)
	}
	digest, err := hex.DecodeString(m.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("%w: 哈希格式无效", ErrUntrusted)
	}
	return nil
}

// ParsePublicKey accepts a 64-byte hex-encoded Ed25519 public key.
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	key, err := hex.DecodeString(strings.TrimSpace(text))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrNoTrustRoot
	}
	return ed25519.PublicKey(key), nil
}

// Source is the release source; the default is GitHub, tests use an HTTP fixture.
type Source struct {
	client     *http.Client
	releaseURL string
	publicKey  ed25519.PublicKey
}

func NewSource(releaseURL, publicKeyHex string) (*Source, error) {
	public, err := ParsePublicKey(publicKeyHex)
	if err != nil {
		return nil, err
	}
	return &Source{
		client:     &http.Client{Timeout: 20 * time.Second},
		releaseURL: releaseURL,
		publicKey:  public,
	}, nil
}

// CheckLatest fetches the latest stable release and returns a verified update
// when its signed manifest is newer than current. No update is silent.
func (s *Source) CheckLatest(ctx context.Context, current string) (Check, error) {
	release, err := s.fetchRelease(ctx)
	if err != nil {
		return Check{}, err
	}
	if release.Draft || release.Prerelease {
		return Check{}, ErrNoUpdate
	}
	version := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	newer, err := Newer(current, version)
	if err != nil {
		return Check{}, err
	}
	if !newer {
		return Check{}, ErrNoUpdate
	}
	manifestAsset := findAsset(release.Assets, manifestName)
	if manifestAsset == nil {
		return Check{}, errors.New("发布缺少更新清单")
	}
	raw, err := s.fetch(ctx, manifestAsset.BrowserDownloadURL, 256*1024)
	if err != nil {
		return Check{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Check{}, fmt.Errorf("更新清单无效: %w", err)
	}
	if manifest.Version != version {
		return Check{}, fmt.Errorf("%w: 清单版本与发布标签不一致", ErrUntrusted)
	}
	if err := Verify(s.publicKey, manifest); err != nil {
		return Check{}, err
	}
	installer := findAsset(release.Assets, manifest.File)
	if installer == nil || installer.BrowserDownloadURL == "" {
		return Check{}, errors.New("发布缺少安装包资产")
	}
	if installer.Size != manifest.Size {
		return Check{}, fmt.Errorf("%w: 资产大小与清单不一致", ErrUntrusted)
	}
	return Check{Version: manifest.Version, Notes: release.Body, Installer: manifest.File,
		Size: manifest.Size, SHA256: manifest.SHA256, DownloadURL: installer.BrowserDownloadURL,
		Manifest: raw}, nil
}

// Download streams a verified installer into dst, checking size and SHA-256.
func (s *Source) Download(ctx context.Context, check Check, dst io.Writer, progress func(int64, int64)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, check.DownloadURL, nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != check.Size {
		return fmt.Errorf("%w: 下载大小不符", ErrHashMismatch)
	}
	hash := sha256.New()
	limit := io.LimitReader(response.Body, check.Size+1)
	buf := make([]byte, 64*1024)
	var written int64
	for {
		n, readErr := limit.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > check.Size {
				return fmt.Errorf("%w: 下载体积超出清单", ErrHashMismatch)
			}
			_, _ = hash.Write(buf[:n])
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
			if progress != nil {
				progress(written, check.Size)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if written != check.Size {
		return fmt.Errorf("%w: 下载大小不符", ErrHashMismatch)
	}
	want, err := hex.DecodeString(check.SHA256)
	if err != nil || !equalBytes(hash.Sum(nil), want) {
		return ErrHashMismatch
	}
	return nil
}

func (s *Source) fetchRelease(ctx context.Context) (Release, error) {
	raw, err := s.fetch(ctx, s.releaseURL, 2*1024*1024)
	if err != nil {
		return Release{}, err
	}
	var release Release
	if err := json.Unmarshal(raw, &release); err != nil {
		return Release{}, fmt.Errorf("发布信息无效: %w", err)
	}
	return release, nil
}

func (s *Source) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("检查更新失败: HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

func findAsset(assets []Asset, name string) *Asset {
	for i := range assets {
		if assets[i].Name == name {
			return &assets[i]
		}
	}
	return nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
