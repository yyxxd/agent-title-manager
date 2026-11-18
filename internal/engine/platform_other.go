//go:build !windows

package engine

import (
	"errors"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

func lockDirectory(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func credential(input []byte, _ bool) ([]byte, error) {
	if len(input) == 0 {
		return nil, nil
	}
	return nil, errors.New("API 密钥存储需要 Windows DPAPI")
}

func hideProcess(*exec.Cmd) {}
