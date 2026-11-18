package pluginmgr

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")

func hideProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func isReparse(info fs.FileInfo) bool {
	attr, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return info.Mode()&os.ModeSymlink != 0 || (ok && attr.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0)
}

func replaceFile(from, to string) error {
	src, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	move := kernel.NewProc("MoveFileExW")
	const replaceWriteThrough = 0x1 | 0x8
	ok, _, err := move.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)), replaceWriteThrough)
	if ok == 0 {
		return fmt.Errorf("替换文件失败：%w", err)
	}
	return nil
}

func lockRegistry(file *os.File) error {
	var overlapped syscall.Overlapped
	const exclusiveNonBlocking = 0x2 | 0x1
	lock := kernel.NewProc("LockFileEx")
	ok, _, err := lock.Call(file.Fd(), exclusiveNonBlocking, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		return fmt.Errorf("插件设置正在使用：%w", err)
	}
	return nil
}
