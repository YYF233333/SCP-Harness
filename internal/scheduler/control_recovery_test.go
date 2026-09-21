//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/model"
	"scp-harness/internal/store"
)

func r1bProcess(t *testing.T, cmd boundedexec.Command) (context.CancelFunc, func() boundedexec.Result) {
	t.Helper()
	ctx, kill := context.WithCancel(context.Background())
	type outcome struct {
		result boundedexec.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, e := boundedexec.Run(ctx, cmd)
		done <- outcome{r, e}
	}()
	var once sync.Once
	var result outcome
	join := func() boundedexec.Result {
		t.Helper()
		once.Do(func() { result = <-done })
		if result.err != nil {
			t.Fatalf("process start/wait: %v %s", result.err, result.result.Stderr)
		}
		return result.result
	}
	t.Cleanup(func() { kill(); join() })
	return kill, join
}

// The barriers are durable DB state and a file written by the real WSL worker.
// Timing only bounds observation; a sleep duration is never an assertion.
func testControlExitRecovery(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	task, e := c.CreateTask(context.Background(), "R1b control crash", repo, "refs/heads/main", c.Operator().ID, 300000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	o, e := c.Propose(task.ID, "bad-worker", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 120000); e != nil {
		t.Fatal(e)
	}
	// There is no live execution owner to hide a missing recovery/control gate.
	before := state(t, c)
	unlock, e := c.LockTaskControl(task.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = New(c).Recover(context.Background())
	unlock()
	if model.Code(e) != "BLOCKED" || !reflect.DeepEqual(before, state(t, c)) {
		t.Fatalf("recovery stole a live control before cancellation: %v", e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "vorton"}
	path := saveConfig(t, c)
	killExecution, joinExecution := r1bProcess(t, crashCommand(t, path, "running"))
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var attemptID string
	for {
		for id, a := range state(t, c).Attempts {
			if a.AnchorID == o.ID && a.Status == "RUNNING" {
				attemptID = id
			}
		}
		if attemptID != "" {
			r, e := c.Runner.Control(context.Background(), []string{"test", "-f", c.Runner.Root() + "/workspace/bad.txt"}, nil, nil, 128)
			if e == nil && r.ExitCode == 0 {
				break
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("real worker did not reach cancellation fixture barrier")
		case <-tick.C:
		}
	}
	killExecution()
	if r := joinExecution(); !r.Canceled {
		t.Fatalf("execution process was not killed: %+v", r)
	}
	if boundedexec.Alive(state(t, c).SlotPID) {
		t.Fatal("execution owner is still alive")
	}
	executable := os.Getenv("SCP_ACCEPTANCE_EXE")
	if executable == "" {
		executable = filepath.Join(t.TempDir(), "scp.exe")
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{"go", "build", "-o", executable, "./cmd/scp"}, Dir: filepath.Join("..", ".."), Timeout: time.Minute, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("build CLI: %v %s", e, r.Stderr)
		}
	}
	killControl, joinControl := r1bProcess(t, boundedexec.Command{Argv: []string{executable, "--config", path, "--json", "task", "suspend", task.ID}, Timeout: 30 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
	controlDeadline := time.NewTimer(10 * time.Second)
	defer controlDeadline.Stop()
	for !state(t, c).Cancellations[task.ID] {
		select {
		case <-controlDeadline.C:
			t.Fatal("control CLI did not commit cancellation")
		case <-tick.C:
		}
	}
	if _, e = New(c).Recover(context.Background()); model.Code(e) != "BLOCKED" {
		t.Fatalf("recovery preempted live control waiting on dead execution: %v", e)
	}
	killControl()
	if r := joinControl(); !r.Canceled {
		t.Fatalf("control process was not killed: %+v", r)
	}
	s := state(t, c)
	if !s.Cancellations[task.ID] || !s.Attempts[attemptID].Active() || s.Tasks[task.ID].Status != "ACTIVE" {
		t.Fatal("process exit falsely completed suspend or removed protection")
	}
	if _, e = c.CloseOption(context.Background(), o.ID); model.Code(e) != "BLOCKED" {
		t.Fatalf("new controller took over abandoned cancellation: %v", e)
	}
	// Even a failed explicit recovery cannot release durable cancellation.
	failedCtx, cancelRecovery := context.WithCancel(context.Background())
	cancelRecovery()
	if _, e = New(c).Recover(failedCtx); e == nil || !state(t, c).Cancellations[task.ID] {
		t.Fatalf("failed recovery removed protection: %v", e)
	}
	if next, e := c.Next(model.ID()); e != nil || next != nil {
		t.Fatalf("abandoned cancellation admitted a new step: %+v %v", next, e)
	}
	report, e := New(c).Recover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	a := s.Attempts[attemptID]
	if len(report.Attempts) != 1 || report.Attempts[0] != attemptID || a.Status != "CRASHED" || a.ArtifactID == nil || s.Cancellations[task.ID] || !store.TaskQuiescent(s, task.ID) || s.Pending[task.ID] != nil || s.Tasks[task.ID].Status != "ACTIVE" || s.Accounts[o.ID].Remaining != 120000-a.Lease {
		t.Fatal("explicit recovery did not capture/settle and clear cancellation")
	}
	if artifactText(t, s.Artifacts[*a.ArtifactID], "bad.txt") != "interrupted\n" {
		t.Fatal("control crash lost worker Artifact")
	}
	for _, b := range s.Blockers {
		if b.Resolved == nil {
			if _, e = c.ResolveBlocker(b.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "success-worker"}
	step(t, engine)
	if len(state(t, c).Attempts) != len(s.Attempts)+1 {
		t.Fatal("explicit recovery did not restore dispatch")
	}
	t.Logf("Control crash/recovery verified with actual executable: %s", executable)
}

func TestR1bProtectedAdmissionChecksCancellation(t *testing.T) {
	engine, task, _, _ := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	pending := *state(t, c).Pending[task.ID]
	if pending.Operation != "protected_test" {
		t.Fatal("expected real pending protected test")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Lifecycle(ctx, task.ID, "suspend"); model.Code(e) != "PRECONDITION_FAILED" {
		t.Fatalf("control cancellation: %v", e)
	}
	before := state(t, c)
	if _, e := c.TestLease(pending, model.ID()); model.Code(e) != "BLOCKED" {
		t.Fatalf("canceled protected test reserved a lease: %v", e)
	}
	if !reflect.DeepEqual(before, state(t, c)) {
		t.Fatal("rejected test admission changed state")
	}
	if e := c.Release(engine.Owner); e != nil {
		t.Fatal(e)
	}
	if _, e := New(c).Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
}
