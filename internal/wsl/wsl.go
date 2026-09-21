package wsl

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

//go:embed files.py
var fileHelper string

type Runner struct{ Config *config.Config }

func (r Runner) Executable(ctx context.Context, command, cwd string) (bool, error) {
	x, e := r.Run(ctx, "scp", []string{"sh", "-c", `cd "$1" 2>/dev/null || cd /; command -v "$2" >/dev/null`, "scp-executable", cwd, command}, nil, nil, time.Duration(r.Config.Limits.ProcessMS)*time.Millisecond, r.Config.Limits.Stdout, r.Config.Limits.Stderr)
	if e != nil || x.TimedOut || x.Canceled {
		return false, model.Err("RUNNER_UNAVAILABLE", "executable probe: %v", e)
	}
	return x.ExitCode == 0, nil
}

func (r Runner) Args(user string, args ...string) []string {
	return append([]string{"wsl.exe", "-d", r.Config.WSL.Distro, "-u", user, "--cd", "/", "--exec"}, args...)
}
func (r Runner) Run(ctx context.Context, user string, args []string, in io.Reader, out io.Writer, timeout time.Duration, maxout, maxerr int64) (boundedexec.Result, error) {
	return boundedexec.Run(ctx, boundedexec.Command{Argv: r.Args(user, args...), Stdin: in, StdoutSink: out, Timeout: timeout, MaxStdout: maxout, MaxStderr: maxerr})
}
func (r Runner) Control(ctx context.Context, args []string, in io.Reader, out io.Writer, maxout int64) (boundedexec.Result, error) {
	return r.Run(ctx, "root", args, in, out, time.Duration(r.Config.Limits.ProcessMS)*time.Millisecond, maxout, r.Config.Limits.Stderr)
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
	x, e := boundedexec.Run(ctx, boundedexec.Command{Argv: []string{"wsl.exe", "--terminate", r.Config.WSL.Distro}, Timeout: time.Duration(r.Config.Limits.ProcessMS) * time.Millisecond, MaxStdout: r.Config.Limits.Stdout, MaxStderr: r.Config.Limits.Stderr})
	if e != nil || x.ExitCode != 0 || x.TimedOut {
		return model.Err("RUNNER_UNAVAILABLE", "runner termination failed: %v: %s", e, x.Stderr)
	}
	return nil
}
func (r Runner) Files(ctx context.Context, op string, args []string, in io.Reader, out io.Writer, maxout int64) error {
	if op == "discard" {
		return r.discard(ctx)
	}
	if op == "prepare" {
		if e := r.discard(ctx); e != nil {
			return e
		}
	}
	argv := append([]string{"python3", "-c", fileHelper, op, r.Config.WSL.Root}, args...)
	x, e := r.Control(ctx, argv, in, out, maxout)
	if e != nil || x.TimedOut {
		return model.Err("RUNNER_UNAVAILABLE", "runner file operation %s: %v", op, e)
	}
	if x.ExitCode == 42 || x.OutputExceeded {
		return model.Err("LIMIT_EXCEEDED", "runner file limit %s: %s", op, x.Stderr)
	}
	if x.ExitCode != 0 {
		return model.Err("RUNNER_UNAVAILABLE", "runner file operation %s exit %d: %s", op, x.ExitCode, x.Stderr)
	}
	return nil
}

// One cleanup has a finite wall deadline. Each helper batch has a deletion-work
// budget and returns 43 only after making progress; admission limits stay intact.
func (r Runner) discard(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(r.Config.Limits.ProcessMS)*time.Millisecond)
	defer cancel()
	limits, e := json.Marshal(r.Config.Limits)
	if e != nil {
		return model.Err("CORE_INCONSISTENT", "cleanup limits: %v", e)
	}
	args := []string{"python3", "-c", fileHelper, "discard", r.Config.WSL.Root, string(limits)}
	for {
		x, e := r.Control(bounded, args, nil, nil, r.Config.Limits.Stdout)
		if e != nil || x.TimedOut || x.Canceled || x.OutputExceeded {
			return model.Err("RUNNER_UNAVAILABLE", "transient cleanup did not complete within its execution bounds: %v: %s", e, x.Stderr)
		}
		switch x.ExitCode {
		case 0:
			return nil
		case 43:
			continue
		default:
			return model.Err("RUNNER_UNAVAILABLE", "transient cleanup failed (exit %d): %s", x.ExitCode, x.Stderr)
		}
	}
}
