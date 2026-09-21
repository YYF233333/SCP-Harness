//go:build windows

package wsl

import (
	"context"
	"io"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/model"
)

func (r Runner) Root() string { return r.Config.WSL.Root }

func (r Runner) distro() string {
	if r.Protected {
		return r.Config.WSL.TestDistro
	}
	return r.Config.WSL.Distro
}

func (r Runner) Args(user string, args ...string) []string {
	return append([]string{"wsl.exe", "-d", r.distro(), "-u", user, "--cd", "/", "--exec"}, args...)
}

func (r Runner) Run(ctx context.Context, user string, args []string, in io.Reader, out io.Writer, timeout time.Duration, maxout, maxerr int64) (boundedexec.Result, error) {
	return boundedexec.Run(ctx, boundedexec.Command{Argv: r.Args(user, args...), Stdin: in, StdoutSink: out, Timeout: timeout, MaxStdout: maxout, MaxStderr: maxerr})
}

func (r Runner) Check(ctx context.Context) error {
	args := []string{"sh", "-c", `test -z "$WSL_INTEROP" && test ! -d /mnt/c/Windows && test "$(id -u scp)" -ne 0 && command -v python3 >/dev/null && command -v tar >/dev/null && ! mount | grep -q ' type 9p .*path=[A-Za-z]:' && python3 -c 'import configparser; c=configparser.ConfigParser(); c.read("/etc/wsl.conf"); assert all(c.getboolean(s,k) is False for s,k in [("automount","enabled"),("automount","mountFsTab"),("interop","enabled"),("interop","appendWindowsPath")])'`}
	x, e := r.Control(ctx, args, nil, nil, 65536)
	if e != nil || x.ExitCode != 0 || x.TimedOut {
		return model.Err("RUNNER_UNAVAILABLE", "runner isolation/start verification: %v: %s", e, x.Stderr)
	}
	return nil
}

func (r Runner) Terminate(ctx context.Context) error {
	x, e := boundedexec.Run(ctx, boundedexec.Command{Argv: []string{"wsl.exe", "--terminate", r.distro()}, Timeout: time.Duration(r.Config.Limits.ProcessMS) * time.Millisecond, MaxStdout: r.Config.Limits.Stdout, MaxStderr: r.Config.Limits.Stderr})
	if e != nil || x.ExitCode != 0 || x.TimedOut {
		return model.Err("RUNNER_UNAVAILABLE", "runner termination failed: %v: %s", e, x.Stderr)
	}
	return nil
}
