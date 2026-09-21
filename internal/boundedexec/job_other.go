//go:build !windows

package boundedexec

import (
	"os"
	"os/exec"
	"syscall"
)

func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}

func contain(p *os.Process) (func(), error) {
	return func() { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) }, nil
}
