//go:build windows

package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"scp-harness/internal/model"
)

var createControlMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

// LockTaskControl is the non-waiting Core control gate, also held by explicit
// recovery. The name uses physical database identity, not a config/path alias.
// Atomic named-object creation grants the gate only to its first creator.
// Other handles are closed immediately on ERROR_ALREADY_EXISTS. No thread owns
// the Win32 mutex: its handle lifetime is the gate, so Go thread migration is
// harmless and process death releases it. The durable cancellation flag is
// independent and is never cleared by unlocking. Handles are not inherited.
func (c *Core) LockTaskControl(taskID string) (func(), error) {
	f, e := os.Open(c.Config.Database)
	if e != nil {
		return nil, model.Err("BLOCKED", "Task control database identity unavailable: %v", e)
	}
	var info syscall.ByHandleFileInformation
	e = syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info)
	f.Close()
	if e != nil {
		return nil, model.Err("BLOCKED", "Task control database identity unavailable: %v", e)
	}
	identity := fmt.Sprintf("%08x:%08x:%08x:%s", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, taskID)
	name, e := syscall.UTF16PtrFromString(fmt.Sprintf("Global\\SCP-Harness-v0-control-%x", sha256.Sum256([]byte(identity))))
	if e != nil {
		return nil, model.Err("BLOCKED", "Task control lock name: %v", e)
	}
	h, _, callErr := createControlMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, model.Err("BLOCKED", "Task control lock unavailable: %v", callErr)
	}
	if callErr == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, model.Err("BLOCKED", "Task control operation already in progress")
	}
	return func() { syscall.CloseHandle(syscall.Handle(h)) }, nil
}
