//go:build linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func prepareObservationSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
func interruptObservationWatcher(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
