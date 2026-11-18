//go:build !windows

package pluginmgr

import (
	"io/fs"
	"os"
	"os/exec"
)

func isReparse(info fs.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }

func replaceFile(from, to string) error { return os.Rename(from, to) }

func lockRegistry(*os.File) error { return nil }

func hideProcess(*exec.Cmd)                                    {}
func platformHookEntry(_, _, _, direct string) (string, error) { return direct, nil }
