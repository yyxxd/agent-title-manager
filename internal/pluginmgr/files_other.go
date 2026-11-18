//go:build !windows

package pluginmgr

import (
	"io/fs"
	"os"
)

func isReparse(info fs.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

func replaceFile(from, to string) error { return os.Rename(from, to) }

func lockRegistry(*os.File) error { return nil }
