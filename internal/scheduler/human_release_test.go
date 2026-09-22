//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"scp-harness/internal/core"
)

// Uses Run, persisted SQLite, real process execution and the production runner.
// Windows release runs this explicitly on SCP-Worker and SCP-Test.
func TestHumanOptionReleaseIntegration(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	task, e := c.CreateTask(context.Background(), "human release", repo, "refs/heads/main", c.Operator().ID, 8400000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	var id string
	for i := 0; i < 100; i++ {
		o, e := c.Propose(task.ID, fmt.Sprint("candidate ", i), "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.Allocate(o.ID, 30000); e != nil {
			t.Fatal(e)
		}
		id = o.ID
	}
	before := state(t, c)
	assertIdleRun(t, engine)
	if len(state(t, c).Attempts) != len(before.Attempts) || fixtureGit(t, repo, "rev-parse", "HEAD") != task.SHA || fixtureGit(t, repo, "status", "--porcelain") != "" {
		t.Fatal("H1: funding started work")
	}
	t.Log("H1 PASS: 100 funded Options, real scheduler polling, zero mutation Attempts; repository unchanged")
	// Existing funded state without Pending must stay idle after reopening/recover.
	if e = c.Store.Close(); e != nil {
		t.Fatal(e)
	}
	c, e = core.Open(c.Config, false)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Store.Close()
	engine = New(c)
	if _, e = engine.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	assertIdleRun(t, engine)
	if len(state(t, c).Attempts) != len(before.Attempts) {
		t.Fatal("recover invented authorization")
	}
	// A zero-funded Option can discuss against root resources.
	unfunded, e := c.Propose(task.ID, "unfunded discussion", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.DiscussOption(unfunded.ID, "explain the risk"); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	thread, e := c.OptionThread(unfunded.ID)
	if e != nil || len(thread.Messages) != 2 || thread.Messages[1].Type != "discussion.reply" {
		t.Fatal("H2: reply", e)
	}
	s := state(t, c)
	if len(s.Artifacts) != 0 || len(s.Pending) != 0 || s.Options[unfunded.ID].Remaining != 0 || fixtureGit(t, repo, "rev-parse", "HEAD") != task.SHA {
		t.Fatal("discussion changed repository/Option")
	}
	for _, a := range s.Attempts {
		if a.Operation == "discussion" && (a.AnchorID != task.ID || a.ArtifactID != nil) {
			t.Fatal("discussion anchor/artifact")
		}
	}
	t.Log("H2 PASS: readonly worker replied; Task root charged; no Artifact or release")
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "workspace-writer"}
	for cycle := 0; cycle < 2; cycle++ {
		if _, e = c.ReleaseOption(id); e != nil {
			t.Fatal(e)
		}
		if _, e = c.DiscussOption(id, "queued alongside Change"); e != nil {
			t.Fatal(e)
		}
		step(t, engine) // explicit discussion gets the next execution boundary
		for i := 0; i < 3; i++ {
			step(t, engine)
		}
		s = state(t, c)
		ch := changeFor(s, task.ID)
		if ch.Stage != "AWAIT_PROMOTION" || fixtureGit(t, repo, "rev-parse", "HEAD") != ch.BaseSHA {
			t.Fatal("release granted promotion authority")
		}
		authorizePromotion(t, c, task.ID)
		step(t, engine)
		s = state(t, c)
		if ch = changeFor(s, task.ID); ch.State != "DONE" || s.Options[id].Status != "OPEN" || s.Options[id].Remaining <= 0 {
			t.Fatal("explicit promotion failed")
		}
		count := len(s.Attempts)
		assertIdleRun(t, engine)
		if len(state(t, c).Attempts) != count {
			t.Fatal("second cycle auto-started")
		}
	}

	t.Log("H3/H4 PASS: each explicit release ran one mutation/test/review/promotion chain; no automatic second cycle")
}

func assertIdleRun(t *testing.T, engine *Scheduler) {
	t.Helper()
	before := state(t, engine.Core)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reason, e := engine.Run(ctx)
	if e != nil || reason != "SIGNAL" {
		t.Fatal("idle scheduler", reason, e)
	}
	after := state(t, engine.Core)
	if len(after.Attempts) != len(before.Attempts) || len(after.Pending) != 0 || after.Slot.State != "IDLE" {
		t.Fatal("idle scheduler created work")
	}
}
