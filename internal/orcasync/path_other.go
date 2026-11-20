//go:build !windows

package orcasync

import (
	"os"
	"path/filepath"
)

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func unredirectedPath(path string, _ os.FileInfo) bool {
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && samePath(path, resolved)
}
