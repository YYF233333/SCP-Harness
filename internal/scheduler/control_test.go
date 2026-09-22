//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scp-harness/internal/model"
)

func testRunningSchedulerControl(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	task, e := c.CreateTask(context.Background(), "control plane", repo, "refs/heads/main", c.Operator().ID, 120000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	o, e := c.Propose(task.ID, "bad-worker", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 80000); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "vorton"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		reason, e := engine.Run(ctx)
		if e == nil && reason != "SIGNAL" {
			e = model.Err("INTERNAL_ERROR", "unexpected run stop %s", reason)
		}
		done <- e
	}()
	deadline := time.Now().Add(20 * time.Second)
	id := ""
	for time.Now().Before(deadline) {
		for _, a := range state(t, c).Attempts {
			if a.AnchorID == o.ID && a.Status == "RUNNING" {
				id = a.ID
			}
		}
		if id != "" {
			r, e := c.Runner.Control(context.Background(), []string{"test", "-f", c.Runner.Root() + "/workspace/bad.txt"}, nil, nil, 1024)
			if e == nil && r.ExitCode == 0 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("worker never ran")
	}
	if _, e = New(c).Step(context.Background()); model.Code(e) != "BLOCKED" {
		t.Fatal("second execution owner admitted")
	}
	if _, e = New(c).Recover(context.Background()); model.Code(e) != "BLOCKED" {
		t.Fatal("recovery overlapped live scheduler")
	}
	if _, e = c.Lifecycle(context.Background(), task.ID, "suspend"); e != nil {
		t.Fatal(e)
	}
	s := state(t, c)
	if s.Attempts[id].Status != "INTERRUPTED" || s.Attempts[id].ArtifactID == nil || s.Tasks[task.ID].Status != "SUSPENDED" || s.Pending[task.ID] != nil || s.Tasks[task.ID].Resources.Outstanding != 0 {
		t.Fatal("suspend did not settle and preserve interrupted work")
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	lock := filepath.Join(filepath.Dir(c.Config.Database), "run.lock")
	if _, e = os.Stat(lock); !os.IsNotExist(e) {
		t.Fatal("normal signal left scheduler lock")
	}
	if e = os.Mkdir(lock, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(lock, "owner"), []byte("2147483646"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = New(c).Run(context.Background()); model.Code(e) != "BLOCKED" {
		t.Fatal("stale lock auto-removed by run")
	}
	report, e := New(c).Recover(context.Background())
	if e != nil || !report.Stale {
		t.Fatalf("explicit recovery stale lock: %v", e)
	}
}
