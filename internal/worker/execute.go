package worker

import (
	"context"
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

func Run(ctx context.Context, runner wsl.Runner, p config.Profile, lease int64) (Outcome, error) {
	start := time.Now()
	root := runner.Config.WSL.Root
	available, e := runner.Executable(ctx, p.Command[0], root+"/workspace")
	if e != nil {
		return Outcome{Status: "TERMINATED", StartedAt: start}, e
	}
	if !available {
		return Outcome{Status: "TERMINATED", StartedAt: start}, model.Err("WORKER_UNAVAILABLE", "configured executable is missing or not executable")
	}
	args := []string{"env", "SCP_INPUT=" + root + "/input.json", "SCP_CONTEXT=" + root + "/context", "SCP_WORKSPACE=" + root + "/workspace", "SCP_RESULT=" + root + "/result.json", "sh", "-c", `cd "$SCP_WORKSPACE" 2>/dev/null || cd /; exec "$@"`, "scp-worker"}
	args = append(args, p.Command...)
	r, e := runner.Run(ctx, "scp", args, nil, nil, time.Duration(lease)*time.Millisecond, runner.Config.Limits.Stdout, runner.Config.Limits.Stderr)
	out := Outcome{Process: r, Status: "RETURNED", StartedAt: start}
	if e != nil {
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
