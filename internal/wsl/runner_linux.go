//go:build linux

package wsl

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/model"
)

// Local execution uses the current unprivileged Linux user. It exercises real
// filesystem/process semantics; Windows host isolation is a release boundary.
func (r Runner) Root() string {
	name := "local-worker"
	if r.Protected {
		name = "local-test"
	}
	return filepath.Join(filepath.Dir(r.Config.Database), filepath.Base(r.Config.Database)+"."+name)
}

func (r Runner) Args(user string, args ...string) []string {
	return args
}

func (r Runner) Run(ctx context.Context, user string, args []string, in io.Reader, out io.Writer, timeout time.Duration, maxout, maxerr int64) (boundedexec.Result, error) {
	if user == "scp" {
		// Record the process group before exec so explicit recovery can terminate
		// workers left by a crashed Core. The start time rejects a reused leader PID.
		launch := "import os,sys\npid=os.getpid()\nstart=open('/proc/self/stat').read().rsplit(')',1)[1].split()[19]\nwith open(sys.argv[1],'w') as f: f.write(str(pid)+' '+start)\nos.execvp(sys.argv[2],sys.argv[2:])\n"
		args = append([]string{"python3", "-c", launch, r.Root() + ".process"}, args...)
	}
	if r.Stdout != nil {
		out = r.Stdout
	}
	return boundedexec.Run(ctx, boundedexec.Command{Argv: args, Stdin: in, StdoutSink: out, StderrSink: r.Stderr, Timeout: timeout, MaxStdout: maxout, MaxStderr: maxerr})
}

func (r Runner) Check(ctx context.Context) error {
	if os.Geteuid() == 0 {
		return model.Err("RUNNER_UNAVAILABLE", "local Linux execution requires an unprivileged user")
	}
	x, e := r.Control(ctx, []string{"sh", "-c", "command -v python3 >/dev/null && command -v tar >/dev/null"}, nil, nil, 4096)
	if e != nil || x.ExitCode != 0 || x.TimedOut || x.Canceled {
		return model.Err("RUNNER_UNAVAILABLE", "local execution prerequisites: %v: %s", e, x.Stderr)
	}
	return nil
}

func (r Runner) Terminate(ctx context.Context) error {
	if ctx.Err() != nil {
		return model.Err("RUNNER_UNAVAILABLE", "local termination canceled: %v", ctx.Err())
	}
	path := r.Root() + ".process"
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return model.Err("RUNNER_UNAVAILABLE", "local process record: %v", e)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return model.Err("RUNNER_UNAVAILABLE", "invalid local process record")
	}
	pid, e := strconv.Atoi(fields[0])
	if e != nil || pid <= 1 {
		return model.Err("RUNNER_UNAVAILABLE", "invalid local process group")
	}
	stat, e := os.ReadFile("/proc/" + fields[0] + "/stat")
	if e != nil && !os.IsNotExist(e) {
		return model.Err("RUNNER_UNAVAILABLE", "local process identity: %v", e)
	}
	if e == nil {
		current := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
		if len(current) < 20 || current[19] != fields[1] {
			return model.Err("RUNNER_UNAVAILABLE", "local process identity changed")
		}
	}
	if e = syscall.Kill(-pid, syscall.SIGKILL); e != nil && e != syscall.ESRCH {
		return model.Err("RUNNER_UNAVAILABLE", "local process group termination: %v", e)
	}
	// SIGKILL delivery is asynchronous. Wait for all non-zombie group members
	// before capture, so descendants cannot race the Artifact snapshot.
	deadline := time.Now().Add(time.Duration(r.Config.Limits.ProcessMS) * time.Millisecond)
	for {
		entries, e := os.ReadDir("/proc")
		if e != nil {
			return model.Err("RUNNER_UNAVAILABLE", "local process inventory: %v", e)
		}
		alive := false
		for _, entry := range entries {
			if _, e := strconv.Atoi(entry.Name()); e != nil {
				continue
			}
			data, e := os.ReadFile("/proc/" + entry.Name() + "/stat")
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return model.Err("RUNNER_UNAVAILABLE", "local process status: %v", e)
			}
			status := strings.Fields(string(data)[strings.LastIndexByte(string(data), ')')+1:])
			if len(status) > 2 && status[2] == fields[0] && status[0] != "Z" && status[0] != "X" {
				alive = true
				break
			}
		}
		if !alive {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return model.Err("RUNNER_UNAVAILABLE", "local process group did not terminate within its bounds")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = os.Remove(path); e != nil {
		return model.Err("RUNNER_UNAVAILABLE", "local process record cleanup: %v", e)
	}
	return nil
}
