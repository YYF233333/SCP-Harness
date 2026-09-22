//go:build linux || (windows && release)

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
	"scp-harness/internal/store"
)

func testCrossProcessControl(t *testing.T) {
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	executable := acceptanceExecutable(t, root, dir)
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.Database, cfg.Artifacts = filepath.Join(dir, "state.db"), filepath.Join(dir, "artifacts")
	for id, path := range cfg.RoleCards {
		cfg.RoleCards[id] = filepath.Join(root, path)
	}
	data, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	cfgPath := filepath.Join(dir, "scp.json")
	if e = os.WriteFile(cfgPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := core.Open(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Store.Close() })
	taskID := model.ID()
	// Seed a valid Task without invoking Git/WSL: the concurrent CLI processes
	// operate the real SQLite database and Core control/settlement transactions.
	if e = c.Store.Update(func(s *model.State) error {
		s.Tasks[taskID] = &model.Task{ID: taskID, Status: "ACTIVE", RepoPath: filepath.Join(dir, "repo"), RepoRef: "refs/heads/main", SHA: strings.Repeat("a", 40), Resources: model.Resources{Minted: 1210000}, Created: model.Now()}
		s.RepositoryIdentities[taskID] = filepath.Join(dir, "repo")
		s.Accounts[taskID] = &model.Account{TaskID: taskID, Remaining: 1210000}
		s.Exploration[taskID] = &model.Exploration{Done: true}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	o, e := c.Propose(taskID, "controlled route", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 5000); e != nil {
		t.Fatal(e)
	}
	owner := model.ID()
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	p, e := c.Next(owner)
	if e != nil || p == nil {
		t.Fatalf("next: %v", e)
	}
	a, e := c.PrepareAttempt(*p, owner)
	if e != nil {
		t.Fatal(e)
	}
	command := func(args ...string) boundedexec.Command {
		return boundedexec.Command{Argv: append([]string{executable, "--config", cfgPath, "--json"}, args...), Timeout: 30 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20}
	}
	type outcome struct {
		result boundedexec.Result
		err    error
	}
	ctx, kill := context.WithCancel(context.Background())
	done := make(chan outcome, 1)
	joined := false
	t.Cleanup(func() {
		kill()
		if !joined {
			<-done
		}
	})
	go func() {
		r, e := boundedexec.Run(ctx, command("task", "suspend", taskID))
		done <- outcome{r, e}
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		s, e := c.Read()
		if e != nil {
			t.Fatal(e)
		}
		if s.Cancellations[taskID] {
			break // Durable transaction is the barrier, never a guessed sleep.
		}
		select {
		case r := <-done:
			joined = true
			t.Fatalf("control returned before settlement: %+v", r)
		case <-deadline.C:
			t.Fatal("CLI never requested cancellation")
		case <-tick.C:
		}
	}
	// Inspect the OS gate itself from another process; the durable flag alone
	// must not mask a accidentally process-local mutex implementation.
	if release, e := c.LockTaskControl(taskID); model.Code(e) != "BLOCKED" {
		if release != nil {
			release()
		}
		t.Fatalf("live CLI did not hold the cross-process Task gate: %v", e)
	}
	for _, args := range [][]string{
		{"task", "suspend", taskID}, {"task", "resume", taskID}, {"task", "close", taskID}, {"option", "close", o.ID},
		{"claim", "create", "--task", taskID, "--subject-type", "OPTION", "--subject-id", o.ID, "--type", "fulfilled"},
		{"claim", "create", "--task", taskID, "--subject-type", "TASK", "--subject-id", taskID, "--type", "fulfilled"},
	} {
		r, e := boundedexec.Run(context.Background(), command(args...))
		var envelope struct {
			OK    bool
			Error struct{ Code string }
		}
		if e != nil || r.ExitCode != 4 || json.Unmarshal(r.Stdout, &envelope) != nil || envelope.OK || envelope.Error.Code != "BLOCKED" {
			t.Fatalf("cross-process contender %v: %v %+v %s %s", args, e, r, r.Stdout, r.Stderr)
		}
	}
	for _, typ := range []string{"note", "resource.propose"} {
		r, e := boundedexec.Run(context.Background(), command("claim", "create", "--task", taskID, "--subject-type", "TASK", "--subject-id", taskID, "--type", typ))
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("informational Claim blocked on control gate: %v %s", e, r.Stdout)
		}
	}
	if e = c.Complete(core.Completion{AttemptID: a.ID, Step: *p, Status: "INTERRUPTED", Elapsed: 7}); e != nil {
		t.Fatalf("live CLI gate prevented settlement: %v", e)
	}
	r := <-done
	joined = true
	if r.err != nil || r.result.ExitCode != 0 {
		t.Fatalf("suspend did not finish after settlement: %v %s %s", r.err, r.result.Stdout, r.result.Stderr)
	}
	s, e := c.Read()
	if e != nil || s.Tasks[taskID].Status != "SUSPENDED" || !store.TaskQuiescent(s, taskID) || s.Cancellations[taskID] || len(s.Attempts) != 1 || s.Options[o.ID].Status != "OPEN" || len(s.Claims) != 2 {
		t.Fatalf("cross-process lifecycle invariant: %v", e)
	}
	t.Logf("Cross-process controls verified with actual executable: %s", executable)
}
