package orcasync

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if strings.EqualFold(left, right) {
		return true
	}
	l, lerr := longPathName(left)
	r, rerr := longPathName(right)
	return lerr == nil && rerr == nil && strings.EqualFold(l, r)
}

func longPathName(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err == nil && n >= uint32(len(buf)) {
		buf = make([]uint16, n)
		n, err = windows.GetLongPathName(p, &buf[0], uint32(len(buf)))
	}
	if err != nil {
		return "", err
	}
	if n == 0 || n >= uint32(len(buf)) {
		return "", windows.ERROR_INSUFFICIENT_BUFFER
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func unredirectedPath(path string, info os.FileInfo) bool {
	// 长名称只消除 8.3 别名。逐级拒绝重解析点，包括最终仍指向同一目录的 junction。
	for current := path; ; {
		attr, ok := info.Sys().(*syscall.Win32FileAttributeData)
		if !ok || attr.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return false
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		var err error
		info, err = os.Lstat(parent)
		if err != nil {
			return false
		}
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && samePath(path, resolved)
}
