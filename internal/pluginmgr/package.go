package pluginmgr

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	manifestName = "agent-title-plugin.json"
	runtimeName  = "atm-runtime.json"
	maxPackage   = 64 << 20
	maxEntries   = 4096
)

var (
	pluginID  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+\.)*[0-9A-Za-z-]+)?(\+([0-9A-Za-z-]+\.)*[0-9A-Za-z-]+)?$`)
	repoRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+$`)
	refRE     = regexp.MustCompile(`^[A-Fa-f0-9]{40}$`)
)

type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Kind          string `json:"kind"`
	Host          string `json:"host"`
	Entry         string `json:"entry"`
}

func validVersion(version string) bool {
	if len(version) > 128 || !versionRE.MatchString(version) {
		return false
	}
	_, pre, ok := strings.Cut(strings.SplitN(version, "+", 2)[0], "-")
	if ok {
		for _, part := range strings.Split(pre, ".") {
			if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
				return false
			}
		}
	}
	return true
}

func readManifest(dir string) (Manifest, error) {
	var m Manifest
	wire, err := readBounded(filepath.Join(dir, manifestName), 64<<10)
	if err != nil {
		return m, fmt.Errorf("无法读取 %s：%w", manifestName, err)
	}
	if json.Unmarshal(wire, &m) != nil || m.SchemaVersion != 1 || m.Kind != "agent" ||
		!pluginID.MatchString(m.ID) || strings.TrimSpace(m.Name) == "" ||
		len(m.Name) > 200 || !validVersion(m.Version) || !safePath(m.Entry) ||
		m.Entry == "." || strings.EqualFold(m.Entry, manifestName) || strings.EqualFold(m.Entry, runtimeName) {
		return m, errors.New("插件清单无效或版本不受支持")
	}
	entry := filepath.Join(dir, filepath.FromSlash(m.Entry))
	if info, err := os.Lstat(entry); err != nil || !info.Mode().IsRegular() || isReparse(info) {
		return m, errors.New("插件入口须为包内普通文件")
	}
	return m, nil
}

func safePath(name string) bool {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\:<>|\"?*") {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || strings.EqualFold(segment, ".git") ||
			strings.TrimRight(segment, ". ") != segment || windowsDevice(segment) ||
			strings.IndexFunc(segment, func(r rune) bool { return r < 32 }) >= 0 {
			return false
		}
	}
	return true
}

func windowsDevice(segment string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(segment), ".")
	base = strings.TrimRight(base, " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if tail, ok := strings.CutPrefix(base, prefix); ok && len([]rune(tail)) == 1 &&
			strings.Contains("0123456789¹²³", tail) {
			return true
		}
	}
	return false
}

type packageCopy struct {
	dest string
	size int64
	seen map[string]string
}

func (p *packageCopy) add(name string, dir bool, data func() (io.ReadCloser, error)) error {
	if !safePath(name) || strings.EqualFold(name, runtimeName) || len(p.seen) >= maxEntries {
		return errors.New("插件包路径不安全或文件过多")
	}
	if _, ok := p.seen[strings.ToLower(name)]; ok {
		return errors.New("插件包包含重复路径")
	}
	p.seen[strings.ToLower(name)] = name
	dest := filepath.Join(p.dest, filepath.FromSlash(name))
	if dir {
		return os.MkdirAll(dest, 0700)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	reader, err := data()
	if err != nil {
		return err
	}
	defer reader.Close()
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(file, io.LimitReader(reader, maxPackage-p.size+1))
	p.size += n
	if p.size > maxPackage {
		copyErr = errors.New("插件解包超过 64 MiB")
	}
	return errors.Join(copyErr, file.Close())
}

func copyLocal(ctx context.Context, source, dest string) error {
	if !filepath.IsAbs(source) {
		return errors.New("本地插件目录须使用绝对路径")
	}
	realSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	realDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return err
	}
	if inside(realSource, realDest) {
		return errors.New("插件源目录包含安装目录")
	}
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() || isReparse(info) {
		return errors.New("本地插件须为普通目录")
	}
	root, err := os.OpenRoot(realSource)
	if err != nil {
		return err
	}
	defer root.Close()
	copy := packageCopy{dest: dest, seen: map[string]string{}}
	return fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == ".git" && entry.IsDir() {
			return fs.SkipDir
		}
		info, err := entry.Info()
		if err != nil || isReparse(info) || !(info.IsDir() || info.Mode().IsRegular()) {
			return errors.New("插件包包含链接或特殊文件")
		}
		return copy.add(name, info.IsDir(), func() (io.ReadCloser, error) { return root.Open(name) })
	})
}

func inside(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func fetchGitHub(ctx context.Context, repo, ref, dest string) error {
	if !repoRE.MatchString(repo) || !safePath(repo) || !refRE.MatchString(ref) {
		return errors.New("GitHub 来源需要 owner/repo 和 40 位提交 SHA")
	}
	client := &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 3 || r.URL.Scheme != "https" || r.URL.Host != "codeload.github.com" {
				return errors.New("不受支持的 GitHub 下载跳转")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://codeload.github.com/"+repo+"/zip/"+strings.ToLower(ref), nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub 下载返回 HTTP %d", response.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxPackage+1))
	if err != nil || len(content) > maxPackage {
		return errors.New("GitHub 插件包读取失败或超过 64 MiB")
	}
	return unpackGitHub(ctx, content, path.Base(repo)+"-"+strings.ToLower(ref), dest)
}

func unpackGitHub(ctx context.Context, content []byte, top, dest string) error {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return err
	}
	copy := packageCopy{dest: dest, seen: map[string]string{}}
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, ok := strings.CutPrefix(file.Name, top+"/")
		if !ok && strings.HasPrefix(strings.ToLower(file.Name), strings.ToLower(top)+"/") {
			name, ok = file.Name[len(top)+1:], true
		}
		if !ok || !(file.Mode().IsDir() || file.Mode().IsRegular()) {
			return errors.New("GitHub 插件包包含非预期路径或特殊文件")
		}
		if name == "" && file.Mode().IsDir() {
			continue
		}
		if err := copy.add(strings.TrimSuffix(name, "/"), file.Mode().IsDir(), file.Open); err != nil {
			return err
		}
	}
	return nil
}

func readBounded(file string, limit int64) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(content)) > limit {
		err = errors.New("文件超过大小限制")
	}
	return content, err
}
