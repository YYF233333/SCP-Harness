//go:build !windows

package boundedexec

import (
	"os"
	"syscall"
)

func Alive(pid int) bool {
	p, e := os.FindProcess(pid)
	return e == nil && p.Signal(syscall.Signal(0)) == nil
}
