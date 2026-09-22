//go:build windows && release

package main

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func prepareObservationSignal(cmd *exec.Cmd) {
	// An isolated hidden console lets the test send real Ctrl+C to this watcher
	// without broadcasting it to the test runner, scheduler, or user terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x10, HideWindow: true}
}
func interruptObservationWatcher(cmd *exec.Cmd) error {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	free := kernel.NewProc("FreeConsole")
	free.Call()
	ok, _, e := kernel.NewProc("AttachConsole").Call(uintptr(cmd.Process.Pid))
	if ok == 0 {
		return fmt.Errorf("attach watcher console: %v", e)
	}
	defer free.Call()
	ok, _, e = kernel.NewProc("SetConsoleCtrlHandler").Call(0, 1)
	if ok == 0 {
		return fmt.Errorf("ignore test process Ctrl+C: %v", e)
	}
	ok, _, e = kernel.NewProc("GenerateConsoleCtrlEvent").Call(0, 0)
	if ok == 0 {
		return fmt.Errorf("watcher Ctrl+C: %v", e)
	}
	time.Sleep(100 * time.Millisecond)
	return nil
}
