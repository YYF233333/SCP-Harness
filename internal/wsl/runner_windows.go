//go:build windows

package wsl

import (
	"context"
	_ "embed"
	"io"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/model"
)

//go:embed isolation.py
var workerIsolation string

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
	if r.Stdout != nil {
		out = r.Stdout
	}
	return boundedexec.Run(ctx, boundedexec.Command{Argv: r.Args(user, args...), Stdin: in, StdoutSink: out, StderrSink: r.Stderr, Env: cleanEnvironment(), ReplaceEnv: true, Timeout: timeout, MaxStdout: maxout, MaxStderr: maxerr})
}

func (r Runner) Check(ctx context.Context) error {
	check := `test -z "$WSL_INTEROP" && test ! -d /mnt/c/Windows && test "$(id -u scp)" -ne 0 && command -v python3 >/dev/null && command -v tar >/dev/null && ! mount | grep -q ' type 9p .*path=[A-Za-z]:' && python3 -c 'import configparser; c=configparser.ConfigParser(); c.read("/etc/wsl.conf"); assert all(c.getboolean(s,k) is False for s,k in [("automount","enabled"),("automount","mountFsTab"),("interop","enabled"),("interop","appendWindowsPath")])'`
	if !r.Protected {
		// WSL boot.command runs asynchronously. Do not admit a worker before
		// root has finished installing the volatile mounts and OS permissions.
		check = `while test ! -f /run/scp-worker-ready; do sleep 0.1; done; ` + check + ` && exec python3 -c "$1"`
	}
	args := []string{"sh", "-c", check, "scp-isolation", workerIsolation}
	x, e := r.Control(ctx, args, nil, nil, 65536)
	if e != nil || x.ExitCode != 0 || x.TimedOut || x.Canceled {
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
