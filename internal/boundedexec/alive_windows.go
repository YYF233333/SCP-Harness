//go:build windows

package boundedexec

import "syscall"

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, e := syscall.OpenProcess(0x100000, false, uint32(pid))
	if e != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	v, e := syscall.WaitForSingleObject(h, 0)
	return e == nil && v == syscall.WAIT_TIMEOUT
}
