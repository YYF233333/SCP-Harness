package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/model"
	"scp-harness/internal/wsl"
)

type Outcome struct {
	Process   boundedexec.Result
	Status    string
	Reason    *string
	StartedAt time.Time
}

func Run(ctx context.Context, runner wsl.Runner, p config.Profile, lease int64, stdout, stderr io.Writer) (Outcome, error) {
	start := time.Now()
	root := runner.Root()
	available, e := runner.Executable(ctx, p.Command[0], root+"/workspace")
	if e != nil {
		return Outcome{Status: "TERMINATED", StartedAt: start}, e
	}
	if !available {
		return Outcome{Status: "TERMINATED", StartedAt: start}, model.Err("WORKER_UNAVAILABLE", "configured executable is missing or not executable")
	}
	args := []string{"env", "SCP_INPUT=" + root + "/input.json", "SCP_CONTEXT=" + root + "/context", "SCP_WORKSPACE=" + root + "/workspace", "SCP_RESULT=" + root + "/result.json", "sh", "-c", `cd "$SCP_WORKSPACE" 2>/dev/null || cd /; exec "$@"`, "scp-worker"}
	args = append(args, p.Command...)
	runner.Stdout, runner.Stderr = stdout, stderr
	r, e := runner.Run(ctx, "scp", args, nil, nil, time.Duration(lease)*time.Millisecond, runner.Config.Limits.Stdout, runner.Config.Limits.Stderr)
	out := Outcome{Process: r, Status: "RETURNED", StartedAt: start}
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) {
			out.Status = "TIMED_OUT"
			return out, nil
		}
		if errors.Is(e, context.Canceled) {
			out.Status = "INTERRUPTED"
			return out, nil
		}
		out.Status = "TERMINATED"
		reason := "RUNNER_UNAVAILABLE"
		out.Reason = &reason
		return out, model.Err("RUNNER_UNAVAILABLE", "worker launch: %v", e)
	}
	if r.Canceled {
		out.Status = "INTERRUPTED"
	} else if r.TimedOut {
		out.Status = "TIMED_OUT"
	} else {
		for _, code := range p.Unavailable {
			if code == r.ExitCode {
				out.Status = "TERMINATED"
				reason := "WORKER_UNAVAILABLE"
				out.Reason = &reason
				break
			}
		}
		if out.Status == "RETURNED" && r.ExitCode != 0 {
			out.Status = "CRASHED"
		}
	}
	return out, nil
}

// OpenLogs creates host-owned sinks before launch, outside the worker bundle.
func OpenLogs(stdout, stderr string) (*os.File, *os.File, error) {
	if e := os.MkdirAll(filepath.Dir(stdout), 0700); e != nil {
		return nil, nil, model.Err("STORAGE_FAILURE", "log directory: %v", e)
	}
	out, e := os.OpenFile(stdout, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, nil, model.Err("STORAGE_FAILURE", "stdout log: %v", e)
	}
	errout, e := os.OpenFile(stderr, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		out.Close()
		return nil, nil, model.Err("STORAGE_FAILURE", "stderr log: %v", e)
	}
	return out, errout, nil
}

func CloseLogs(stdout, stderr *os.File) error {
	var result error
	for _, f := range []*os.File{stdout, stderr} {
		if f == nil {
			continue
		}
		e := f.Sync()
		ce := f.Close()
		if e != nil || ce != nil {
			result = model.Err("STORAGE_FAILURE", "log flush: %v/%v", e, ce)
		}
	}
	return result
}
func Logs(dir string, r boundedexec.Result) (string, string, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", "", model.Err("STORAGE_FAILURE", "log directory: %v", e)
	}
	out, errout := filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log")
	for path, data := range map[string][]byte{out: r.Stdout, errout: r.Stderr} {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if e != nil {
			return "", "", model.Err("STORAGE_FAILURE", "log open: %v", e)
		}
		_, e = f.Write(data)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil || ce != nil {
			return "", "", model.Err("STORAGE_FAILURE", "log write: %v/%v", e, ce)
		}
	}
	return out, errout, nil
}
