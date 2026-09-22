package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/core"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
	"scp-harness/internal/wsl"
)

type Scheduler struct {
	Core  *core.Core
	Owner string
	mu    sync.Mutex
}

func New(c *core.Core) *Scheduler { return &Scheduler{Core: c, Owner: model.ID()} }
func (s *Scheduler) Run(ctx context.Context) (string, error) {
	lock := filepath.Join(filepath.Dir(s.Core.Config.Database), "run.lock")
	if e := os.Mkdir(lock, 0700); e != nil {
		return "", model.Err("BLOCKED", "scheduler lock exists or inaccessible; inspect and run scp recover: %v", e)
	}
	if e := os.WriteFile(filepath.Join(lock, "owner"), []byte(strconv.Itoa(os.Getpid())), 0600); e != nil {
		return "", artifact.StorageError("scheduler lock owner", e)
	}
	clean := false
	defer func() {
		if clean {
			_ = os.Remove(filepath.Join(lock, "owner"))
			_ = os.Remove(lock)
		}
	}()
	state, e := s.Core.Read()
	if e != nil {
		return "", e
	}
	for _, a := range state.Attempts {
		if a.Active() {
			return "", model.Err("BLOCKED", "dangling Attempt; run scp recover")
		}
	}
	if state.Slot.State != "IDLE" {
		return "", model.Err("BLOCKED", "dangling execution slot; run scp recover")
	}
	for {
		if ctx.Err() != nil {
			if e := s.Core.Release(s.Owner); e != nil {
				return "FAIL_STOP", e
			}
			clean = true
			return "SIGNAL", nil
		}
		state, e = s.Core.Read()
		if e != nil {
			return "", e
		}
		if state.FailStop() {
			clean = true
			return "FAIL_STOP", nil
		}
		ran, e := s.Step(ctx)
		if e != nil {
			code := model.Code(e)
			if model.Exit(code) == 5 {
				blockErr := s.Core.Block(code, "", "", "", e.Error())
				slog.Error("global fail-stop", "error", e)
				// If the blocker itself cannot be persisted, keep run.lock so
				// a later process cannot resume implicitly after storage returns.
				clean = blockErr == nil
				return "FAIL_STOP", e
			}
			if model.Exit(code) == 4 || code == "LIMIT_EXCEEDED" || code == "PRECONDITION_CHANGED" {
				slog.Warn("execution blocked", "error", e)
			} else {
				_ = s.Core.Release(s.Owner)
				clean = true
				return "", e
			}
		}
		if !ran || e != nil {
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}
func (s *Scheduler) Step(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, e := s.Core.Next(s.Owner)
	if e != nil || p == nil {
		return false, e
	}
	slog.Info("execution step", "task", p.TaskID, "operation", p.Operation, "target", p.TargetID)
	switch p.Operation {
	case "protected_test":
		e = s.test(ctx, *p)
	case "promotion":
		e = s.promote(ctx, *p)
	default:
		e = s.attempt(ctx, *p)
	}
	if e != nil {
		code := model.Code(e)
		if code == "WORKER_UNAVAILABLE" || code == "RUNNER_UNAVAILABLE" || code == "REPOSITORY_UNAVAILABLE" || code == "STORAGE_FAILURE" || code == "CORE_INCONSISTENT" {
			profile, runner := "", ""
			if p.Operation == "protected_test" {
				runner = s.Core.Config.WSL.TestDistro
			} else if p.Operation != "promotion" {
				profile = s.Core.Config.Profile(p.Operation).ID
				runner = s.Core.Config.WSL.Distro
			}
			if be := s.Core.Block(code, p.TaskID, profile, runner, e.Error()); be != nil {
				return true, be
			}
		}
		_ = s.Core.Release(s.Owner)
	}
	return true, e
}
func (s *Scheduler) temp(id string) (string, error) {
	dir := filepath.Join(filepath.Dir(s.Core.Config.Database), "runtime", id)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", artifact.StorageError("runtime directory", e)
	}
	return dir, nil
}
func (s *Scheduler) upload(ctx context.Context, runner wsl.Runner, dir, mode string, synthetic bool, source string) error {
	c := s.Core
	f, e := os.CreateTemp(filepath.Dir(dir), "transfer-*.tar")
	if e != nil {
		return artifact.StorageError("transfer", e)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	// The bundle includes both projected context and workspace, each individually
	// bounded before bundling. The transport bound accounts for the two copies.
	limits := c.Config.Limits
	limits.Bytes *= 3
	limits.Files *= 3
	if e = artifact.Pack(dir, f, limits); e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return artifact.StorageError("transfer seek", e)
	}
	boundJSON, _ := json.Marshal(limits)
	if e = runner.Files(ctx, "prepare", []string{string(boundJSON)}, f, nil, c.Config.Limits.Stdout); e != nil {
		return e
	}
	if source != "" {
		snapshot, e := os.Open(source)
		if e != nil {
			return artifact.StorageError("workspace source", e)
		}
		e = runner.Files(ctx, "restore", nil, snapshot, nil, c.Config.Limits.Stdout)
		snapshot.Close()
		if e != nil {
			return e
		}
	}
	if synthetic {
		if e = c.Git.Synthetic(ctx); e != nil {
			return e
		}
	}
	b, _ := json.Marshal(c.Config.Limits)
	return runner.Files(ctx, "permissions", []string{string(b), mode}, nil, nil, c.Config.Limits.Stdout)
}
func (s *Scheduler) capture(a *model.Attempt, p model.Step, dir string) (*model.Artifact, error) {
	c := s.Core
	f, e := os.CreateTemp(dir, "capture-*.tar")
	if e != nil {
		return nil, artifact.StorageError("capture file", e)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	limits, _ := json.Marshal(c.Config.Limits)
	if e = c.Runner.Files(context.Background(), "capture", []string{string(limits), a.ID}, nil, f, artifact.MaxTar(c.Config.Limits)); e != nil {
		return nil, e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return nil, artifact.StorageError("capture seek", e)
	}
	base := a.SHA
	anchor := p.OptionID
	if p.TargetType == "ARTIFACT" {
		state, e := c.Read()
		if e != nil {
			return nil, e
		}
		source := state.Artifacts[p.TargetID]
		base = source.BaseSHA
		anchor = source.Anchor
	}
	value := model.Artifact{ID: model.ID(), TaskID: a.TaskID, Anchor: anchor, AttemptID: a.ID, BaseSHA: base, Created: model.Now()}
	value, e = artifact.Publish(c.Config.Artifacts, value, f.Name(), c.Config.Limits)
	if e != nil {
		return nil, e
	}
	return &value, nil
}

// monitor only cancels the one active process; it never dispatches work.
func (s *Scheduler) monitor(parent context.Context, task, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state, e := s.Core.Read()
				if e != nil || state.Cancellations[task] || id != "" && state.Interrupts[id] || state.FailStop() {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
func (s *Scheduler) attempt(ctx context.Context, p model.Step) error {
	c := s.Core
	current, e := c.Read()
	if e != nil {
		return e
	}
	task := current.Tasks[p.TaskID]
	sha, e := c.Git.Resolve(ctx, task.RepoPath, task.RepoRef)
	if e != nil {
		return e
	}
	if sha != task.SHA {
		if e = c.Update(func(st *model.State) error { st.Tasks[p.TaskID].SHA = sha; st.Touch(p.TaskID); return nil }); e != nil {
			return e
		}
	}
	a, e := c.PrepareAttempt(p, s.Owner)
	if e != nil {
		return e
	}
	start := time.Now()
	profile := c.Config.Profile(p.Operation)
	monitor, stop := s.monitor(ctx, p.TaskID, a.ID)
	defer stop()
	active, cancel := context.WithTimeout(monitor, time.Duration(a.Lease)*time.Millisecond)
	defer cancel()
	dir, e := s.temp(a.ID)
	var visible map[string]bool
	prepared := false
	if e == nil {
		e = c.Runner.Check(active)
	}
	if e == nil {
		visible, e = c.Materialize(active, a, p, dir)
	}
	if e == nil {
		source := ""
		if profile.Workspace != "none" {
			source = dir + ".snapshot.tar"
			if p.TargetType == "ARTIFACT" {
				st, re := c.Read()
				if re != nil {
					return re
				}
				source = st.Artifacts[p.TargetID].BlobPath
			}
		}
		e = s.upload(active, c.Runner, dir, profile.Workspace, profile.Synthetic, source)
		prepared = e == nil
	}
	out := worker.Outcome{Status: "TERMINATED"}
	out.Process.ExitCode = -1
	var stdoutFile, stderrFile *os.File
	if e == nil {
		stdoutFile, stderrFile, e = worker.OpenLogs(a.Stdout, a.Stderr)
	}
	if e == nil {
		e = c.MarkRunning(a.ID)
	}
	if e == nil {
		remaining := a.Lease - time.Since(start).Milliseconds()
		if remaining <= 0 {
			out.Status = "TIMED_OUT"
		} else {
			out, e = worker.Run(active, c.Runner, profile, remaining, stdoutFile, stderrFile)
		}
	}
	launchFailure := e != nil && out.Reason != nil && *out.Reason == "RUNNER_UNAVAILABLE"
	// Distro termination is unconditional even on normal worker return: children
	// cannot continue writing while Core captures or reviews the submitted state.
	terminateErr := c.Runner.Terminate(context.Background())
	if terminateErr != nil {
		e = terminateErr
	}
	status := out.Status
	reason := ""
	if e != nil {
		reason = model.Code(e)
	}
	if out.Reason != nil && e == nil {
		reason = *out.Reason
		e = model.Err(reason, "worker profile %s returned unavailable exit code %d", profile.ID, out.Process.ExitCode)
	}
	if e != nil && model.Exit(model.Code(e)) >= 4 {
		status = "TERMINATED"
		reason = model.Code(e)
	}
	if !launchFailure && active.Err() == context.DeadlineExceeded && model.Exit(model.Code(e)) < 5 {
		status = "TIMED_OUT"
		reason = ""
		e = nil
	} else if !launchFailure && monitor.Err() != nil && model.Exit(model.Code(e)) < 5 {
		status = "INTERRUPTED"
		reason = ""
		e = nil
	}
	if terminateErr != nil {
		status = "TERMINATED"
		reason = "RUNNER_UNAVAILABLE"
		e = terminateErr
	}
	var captured *model.Artifact
	if dir != "" && profile.Workspace == "writable" && terminateErr == nil {
		captured, terminateErr = s.capture(a, p, dir)
		if terminateErr != nil {
			if model.Exit(model.Code(terminateErr)) >= 4 {
				e = terminateErr
				status = "TERMINATED"
				reason = model.Code(e)
			} else if e == nil {
				reason = model.Code(terminateErr)
			}
		}
	}
	var result worker.Result
	valid := false
	if prepared && status == "RETURNED" && e == nil {
		var b bytes.Buffer
		re := c.Runner.Files(context.Background(), "result", []string{strconv.FormatInt(c.Config.Limits.Result, 10)}, nil, &b, c.Config.Limits.Result)
		if re == nil {
			result, re = worker.Parse(b.Bytes(), p.Operation, c.Config.Limits.Result)
		}
		valid = re == nil
		if re != nil {
			slog.Warn("invalid worker output", "attempt", a.ID, "error", re)
			if model.Code(re) == "RUNNER_UNAVAILABLE" {
				e = re
				status = "TERMINATED"
				reason = "RUNNER_UNAVAILABLE"
			}
		}
	}
	logErr := worker.CloseLogs(stdoutFile, stderrFile)
	if logErr != nil {
		e = logErr
		status = "TERMINATED"
		reason = "STORAGE_FAILURE"
	}
	var exit *int
	if out.Process.Started {
		code := out.Process.ExitCode
		exit = &code
	}
	v := core.Completion{AttemptID: a.ID, Step: p, Artifact: captured, Result: result, Valid: valid, Status: status, Reason: reason, ExitCode: exit, Stdout: a.Stdout, Stderr: a.Stderr, Elapsed: time.Since(start).Milliseconds(), Uncertain: status == "TIMED_OUT" || terminateErr != nil && model.Code(terminateErr) == "RUNNER_UNAVAILABLE", Visible: visible}
	if ce := c.Complete(v); ce != nil {
		return ce
	}
	return e
}
func (s *Scheduler) test(ctx context.Context, p model.Step) error {
	c := s.Core
	id := model.ID()
	lease, e := c.TestLease(p, id)
	if e != nil {
		return e
	}
	start := time.Now()
	monitor, stop := s.monitor(ctx, p.TaskID, "")
	defer stop()
	active, cancel := context.WithTimeout(monitor, time.Duration(lease)*time.Millisecond)
	defer cancel()
	state, e := c.Read()
	if e != nil {
		return e
	}
	a := state.Artifacts[p.TargetID]
	dir, e := s.temp(id)
	if e == nil {
		e = c.TestRunner.Check(active)
	}
	if e == nil {
		e = os.Mkdir(filepath.Join(dir, "workspace"), 0700)
	}
	if e == nil {
		e = artifact.Restore(*a, filepath.Join(dir, "workspace"), c.Config.Limits)
	}
	if e == nil {
		e = os.WriteFile(filepath.Join(dir, "input.json"), []byte(`{}`), 0600)
	}
	if e == nil {
		e = os.WriteFile(filepath.Join(dir, "result.json"), nil, 0600)
	}
	if e == nil {
		e = s.upload(active, c.TestRunner, dir, "writable", false, a.BlobPath)
	}
	r := boundedexec.Result{ExitCode: -1}
	if e == nil {
		var available bool
		available, e = c.TestRunner.Executable(active, c.Config.Test.Command[0], c.TestRunner.Root()+"/workspace")
		if e == nil && !available {
			e = model.Err("RUNNER_UNAVAILABLE", "protected test executable unavailable")
		}
	}
	if e == nil {
		args := append([]string{"sh", "-c", `cd "$1" && shift && exec "$@"`, "scp-test", c.TestRunner.Root() + "/workspace"}, c.Config.Test.Command...)
		remaining := max(1, lease-time.Since(start).Milliseconds())
		r, e = c.TestRunner.Run(active, "scp", args, nil, nil, time.Duration(remaining)*time.Millisecond, c.Config.Test.Output, c.Config.Test.Output)
	}
	term := c.TestRunner.Terminate(context.Background())
	if term != nil {
		e = term
	}
	var cleanupErr error
	if term == nil {
		bounds, _ := json.Marshal(c.Config.Limits)
		cleanupErr = c.TestRunner.Files(context.Background(), "discard", []string{string(bounds)}, nil, nil, c.Config.Limits.Stdout)
		if cleanupErr != nil {
			e = cleanupErr
		}
	}
	outcome := "FAIL"
	if r.TimedOut || active.Err() == context.DeadlineExceeded {
		outcome = "TIMEOUT"
	}
	if e == nil && r.Started && r.ExitCode == 0 && !r.TimedOut && !r.Canceled {
		outcome = "PASS"
	}
	if active.Err() == context.DeadlineExceeded && term == nil && cleanupErr == nil && model.Exit(model.Code(e)) < 5 {
		e = nil
		outcome = "TIMEOUT"
	}
	if e != nil {
		if e == nil {
			e = model.Err("RUNNER_UNAVAILABLE", "protected test executable unavailable")
		}
		settle := c.Store.Update(func(st *model.State) error {
			if se := ledger.Settle(st, id, time.Since(start).Milliseconds(), term != nil); se != nil {
				return se
			}
			st.Touch(p.TaskID)
			return nil
		})
		if settle != nil {
			return settle
		}
		if model.Code(e) == "INTERNAL_ERROR" {
			e = model.Err("RUNNER_UNAVAILABLE", "protected runner: %v", e)
		}
		return e
	}
	stdout, stderr, e := worker.Logs(filepath.Join(dir, "logs"), r)
	if e != nil {
		return e
	}
	var exit *int
	if r.Started {
		code := r.ExitCode
		exit = &code
	}
	result := model.TestResult{ArtifactID: a.ID, Outcome: outcome, Stdout: stdout, Stderr: stderr, ExitCode: exit, Created: model.Now()}
	return c.FinishTest(p, id, result, time.Since(start).Milliseconds(), outcome == "TIMEOUT")
}
func (s *Scheduler) promote(ctx context.Context, p model.Step) error {
	c := s.Core
	state, e := c.Read()
	if e != nil {
		return e
	}
	a, t := state.Artifacts[p.TargetID], state.Tasks[p.TaskID]
	if a == nil || state.Tests[a.ID] == nil || state.Tests[a.ID].Outcome != "PASS" || state.Reviews[a.ID] == nil || state.Reviews[a.ID].Verdict != "APPROVE" {
		return model.Err("CORE_INCONSISTENT", "promotion gate conjunction failed")
	}
	dir, e := s.temp("promotion-" + model.ID())
	if e != nil {
		return e
	}
	tree := filepath.Join(dir, "tree")
	if e = os.Mkdir(tree, 0700); e != nil {
		return artifact.StorageError("promotion tree", e)
	}
	if e = artifact.Restore(*a, tree, c.Config.Limits); e != nil {
		return e
	}
	sha, e := c.Git.Construct(ctx, *t, *a, tree)
	if model.Code(e) == "PRECONDITION_CHANGED" {
		return c.EndPromotion(p, "", "PRECONDITION_CHANGED")
	}
	if e != nil {
		return e
	}
	j := &model.Journal{ID: model.ID(), TaskID: t.ID, ArtifactID: a.ID, Ref: t.RepoRef, OldSHA: a.BaseSHA, NewSHA: sha, State: "PREPARED", Created: model.Now()}
	if e = c.PreparePromotion(j); e != nil {
		return e
	}

	if e = c.Git.CAS(ctx, t.RepoPath, t.RepoRef, j.OldSHA, j.NewSHA); e != nil {
		if model.Code(e) == "PRECONDITION_CHANGED" {
			if e = c.Store.Update(func(st *model.State) error { st.Journals[j.ID].State = "CONFLICT"; return nil }); e != nil {
				return e
			}
			return c.EndPromotion(p, "", "PRECONDITION_CHANGED")
		}
		return e
	}
	return c.ApplyPromotion(j.ID)
}
