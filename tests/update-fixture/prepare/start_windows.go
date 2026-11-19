package main

import (
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func startHelper(binary, param string) error {
	cmd := exec.Command(binary, param)
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS}
	log, err := os.OpenFile(param+".log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Start()
}
