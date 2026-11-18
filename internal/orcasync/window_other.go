//go:build !windows

package orcasync

import "os/exec"

func hideWindow(*exec.Cmd) {}
