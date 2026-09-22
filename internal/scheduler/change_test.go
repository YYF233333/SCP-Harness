//go:build linux || (windows && release)

package scheduler

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
)

func TestV2PromotionAuthorityRestartAndStandaloneCI(t *testing.T) {
	engine, task, _, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	step(t, engine)
	step(t, engine)
	s := state(t, c)
	ch := s.Changes[a.ChangeID]
	if ch.Stage != "AWAIT_PROMOTION" || fixtureGit(t, task.RepoPath, "rev-parse", task.RepoRef) != task.SHA {
		t.Fatal("release or review promoted implicitly")
	}
	before := len(s.Attempts)
	r, e := c.QueueCI(a.ID)
	if e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	if _, e = c.ChangeAction(context.Background(), ch.ID, "promote"); model.Code(e) != "PRECONDITION_FAILED" {
		t.Fatal("new CI evidence bypassed review", e)
	}
	s = state(t, c)
	if len(s.Attempts) != before || s.CIRuns[r.ID].Status != "PASS" || s.Changes[ch.ID].Stage != "AWAIT_PROMOTION" {
		t.Fatal("standalone CI became agent/promotion")
	}
	if _, e = c.ChangeAction(context.Background(), ch.ID, "retry-review"); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	if state(t, c).Reviews[a.ID].CIRunID != r.ID {
		t.Fatal("review ignored latest CI evidence")
	}
	card := c.Operator()
	denied := card
	denied.Capabilities = nil
	c.Config.Cards[c.Config.Operator] = denied
	if _, e = c.ChangeAction(context.Background(), ch.ID, "promote"); model.Code(e) != "CAPABILITY_DENIED" {
		t.Fatal(e)
	}
	c.Config.Cards[c.Config.Operator] = card
	assertIdleRun(t, engine)
	if e = c.Store.Close(); e != nil {
		t.Fatal(e)
	}
	c, e = core.Open(c.Config, false)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Store.Close()
	engine = New(c)
	assertIdleRun(t, engine)
	s = state(t, c)
	if len(s.Changes) != 1 || s.Changes[ch.ID].ArtifactID != a.ID || s.Changes[ch.ID].Stage != "AWAIT_PROMOTION" {
		t.Fatal("restart lost development position")
	}
	authorizePromotion(t, c, task.ID)
	step(t, engine)
	if state(t, c).Changes[ch.ID].State != "DONE" || fixtureGit(t, task.RepoPath, "rev-parse", task.RepoRef) == task.SHA {
		t.Fatal("explicit CAS failed")
	}
}

func TestV2InterruptResumeControlQueueAndStaleChange(t *testing.T) {
	engine, task, option, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	ctx := context.Background()
	if _, e := c.ChangeAction(ctx, a.ChangeID, "rework"); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "pause-worker"}
	done := make(chan error, 1)
	go func() { _, e := engine.Step(ctx); done <- e }()
	var attempt *model.Attempt
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		for _, at := range state(t, c).Attempts {
			if at.ChangeID == a.ChangeID && at.Active() {
				data, _ := os.ReadFile(at.Stdout)
				if strings.Contains(string(data), "pause workspace ready") {
					attempt = at
				}
			}
		}
		if attempt != nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if attempt == nil {
		t.Fatal("worker did not reach capture point")
	}
	if _, e := c.DiscussOption(option.ID, "queued while mutation executes"); e != nil {
		t.Fatal(e)
	}
	other, e := c.Propose(task.ID, "second development", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(other.ID, 30000); e != nil {
		t.Fatal(e)
	}
	second, e := c.ReleaseOption(other.ID)
	if e != nil {
		t.Fatal(e)
	}
	s := state(t, c)
	if s.Changes[second.ID].State != "QUEUED" || len(s.Requests) != 1 {
		t.Fatal("control plane blocked")
	}
	if _, e = New(c).Step(ctx); model.Code(e) != "BLOCKED" {
		t.Fatal("second scheduler entered execution slot", e)
	}
	interrupted, e := c.Interrupt(ctx, attempt.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	ch := s.Changes[a.ChangeID]
	if interrupted.Status != "INTERRUPTED" || interrupted.ArtifactID == nil || ch.State != "PAUSED" || ch.ArtifactID != *interrupted.ArtifactID || s.Slot.State != "IDLE" {
		t.Fatal("interrupt lost Change/Artifact")
	}
	if artifactText(t, s.Artifacts[ch.ArtifactID], "pause-marker.txt") != "preserved before pause\n" {
		t.Fatal("pause capture")
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "resume-worker"}
	if _, e = c.ChangeAction(ctx, ch.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	step(t, engine) // explicitly queued discussion has priority
	s = state(t, c)
	if len(s.Requests) != 0 || s.Changes[ch.ID].Stage != "MUTATION" {
		t.Fatal("control request did not get next boundary")
	}
	step(t, engine)
	s = state(t, c)
	resumed := s.Artifacts[s.Changes[ch.ID].ArtifactID]
	if artifactText(t, resumed, "resumed.txt") != "continued\n" || s.Attempts[resumed.AttemptID].TargetID != *interrupted.ArtifactID {
		t.Fatal("resume used baseline")
	}
	step(t, engine)
	step(t, engine)
	if _, e = c.ChangeAction(ctx, ch.ID, "promote"); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	// B was released on the old base; A's CAS makes it stale without a worker.
	count := len(state(t, c).Attempts)
	if ran, e := engine.Step(ctx); e != nil || ran {
		t.Fatal("stale queued Change executed", e)
	}
	s = state(t, c)
	if s.Changes[second.ID].State != "STALE" || len(s.Attempts) != count {
		t.Fatal("stale base not detected")
	}
}

func TestV2CIEvidenceAndRepeatedTimeout(t *testing.T) {
	t.Run("evidence", func(t *testing.T) {
		t.Setenv("SCP_WORKSPACE", "/wrong-host-workspace")
		t.Setenv("SCP_INPUT", "/wrong-host-input")
		t.Setenv("WSLENV", "SCP_WORKSPACE:SCP_INPUT:"+os.Getenv("WSLENV"))
		engine, _, _, a := candidate(t, "evidence-reviewer")
		c := engine.Core
		c.Config.Test.Command = []string{fixtureWorker(t), "protected-test", "fail"}
		step(t, engine)
		var watched bytes.Buffer
		if e := c.WatchCI(context.Background(), state(t, c).Changes[a.ChangeID].CIRunID, &watched); e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(watched.String(), "CI failure detail on stdout") || !strings.Contains(watched.String(), "CI failure detail on stderr") {
			t.Fatal("CI watch missing logs", watched.String())
		}
		step(t, engine)
		ch := state(t, c).Changes[a.ChangeID]
		if ch.Stage != "MUTATION" || ch.State != "QUEUED" {
			t.Fatal("review could not read actual CI logs")
		}
		c.Config.Workers[0].Command = []string{fixtureWorker(t), "evidence-rework"}
		step(t, engine)
		s := state(t, c)
		if artifactText(t, s.Artifacts[s.Changes[ch.ID].ArtifactID], "reworked.txt") != "evidence read\n" {
			t.Fatal("rework evidence missing")
		}
	})
	t.Run("timeout_circuit", func(t *testing.T) {
		engine, _, _, a := candidate(t, "fake-reviewer-reject")
		c := engine.Core
		c.Config.Test.Command = []string{fixtureWorker(t), "protected-test", "timeout"}
		c.Config.Test.Timeout = 2000
		step(t, engine)
		step(t, engine)
		step(t, engine)
		step(t, engine)
		s := state(t, c)
		ch := s.Changes[a.ChangeID]
		if ch.State != "BLOCKED" || ch.Reason != "REPEATED_CI_TIMEOUT" || ch.TimeoutStreak != 2 || len(s.CIRuns) != 2 || len(s.Blockers) != 0 {
			t.Fatalf("timeout fuse: %+v", ch)
		}
		count := len(s.Attempts)
		if ran, e := engine.Step(context.Background()); ran || e != nil || len(state(t, c).Attempts) != count {
			t.Fatal("automatic timeout loop", e)
		}
		for _, r := range s.CIRuns {
			if r.Status != "TIMEOUT" || r.Timeout != 2000 {
				t.Fatal("timeout provenance")
			}
			if _, e := os.Stat(r.Stdout); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := c.ChangeAction(context.Background(), ch.ID, "retry-ci"); e != nil {
			t.Fatal(e)
		}
		c.Config.Test.Command = []string{fixtureWorker(t), "protected-test"}
		c.Config.Test.Timeout = 60000
		step(t, engine)
		if ch = state(t, c).Changes[ch.ID]; ch.TimeoutStreak != 0 || ch.Stage != "REVIEW" {
			t.Fatal("non-timeout failed to break streak")
		}
	})
}

func TestV2SuspendResumeBaselineAndConfigProvenance(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_sha", true: "changed_sha"}[drift], func(t *testing.T) {
			engine, task, _, a := candidate(t, "fake-reviewer-approve")
			c := engine.Core
			if _, e := c.Lifecycle(context.Background(), task.ID, "suspend"); e != nil {
				t.Fatal(e)
			}
			if drift {
				tree := fixtureGit(t, task.RepoPath, "rev-parse", task.SHA+"^{tree}")
				sha := fixtureGit(t, task.RepoPath, "-c", "user.name=fixture", "-c", "user.email=fixture@local", "commit-tree", tree, "-p", task.SHA, "-m", "external")
				fixtureGit(t, task.RepoPath, "update-ref", task.RepoRef, sha, task.SHA)
			}
			if _, e := c.Lifecycle(context.Background(), task.ID, "resume"); e != nil {
				t.Fatal(e)
			}
			ch := state(t, c).Changes[a.ChangeID]
			if drift {
				if ch.State != "STALE" {
					t.Fatal("suspend/resume transplanted baseline")
				}
			} else {
				if ch.State != "PAUSED" || ch.ArtifactID != a.ID {
					t.Fatal("suspend lost Artifact")
				}
				if _, e := c.ChangeAction(context.Background(), ch.ID, "resume"); e != nil {
					t.Fatal(e)
				}
				step(t, engine)
			}
		})
	}
	// Config fingerprints include actual commands and role cards, independent of host paths in logs.
	engine, _, _, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	before := c.Config.Hash()
	step(t, engine)
	r := state(t, c).CIRuns[state(t, c).Changes[a.ChangeID].CIRunID]
	if r.ConfigHash != before || r.Timeout != c.Config.Test.Timeout || len(r.Command) == 0 {
		t.Fatal("CI effective config missing")
	}
	path := saveConfig(t, c)
	loaded, e := config.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if loaded.Path != filepath.Clean(path) {
		t.Fatal("config path provenance")
	}
}

func TestV2SchedulerFreezesConfigUntilRestart(t *testing.T) {
	engine, _, _, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	step(t, engine)
	step(t, engine)
	original := c.Config.Test.Timeout
	originalCommand := append([]string{}, c.Config.Test.Command...)
	run := func(mutate bool) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, e := engine.Run(ctx); done <- e }()
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			s := state(t, c)
			if s.SchedulerConfigHash == c.Config.Hash() {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		// Wait for this Run's lock as well; prior scheduler metadata may have the same hash.
		for time.Now().Before(deadline) {
			if _, e := os.Stat(filepath.Join(filepath.Dir(c.Config.Database), "run.lock", "owner")); e == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if mutate {
			c.Config.Test.Timeout = original / 2
			c.Config.Test.Command[0] = "/not-installed"
		}
		r, e := c.QueueCI(a.ID)
		if e != nil {
			t.Fatal(e)
		}
		var result *model.CIRun
		for time.Now().Before(deadline) {
			result = state(t, c).CIRuns[r.ID]
			if result.Ended != "" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		cancel()
		if e = <-done; e != nil {
			t.Fatal(e)
		}
		c.Config.Test.Command = append([]string{}, originalCommand...)
		expected := original / 2
		if mutate {
			expected = original
		}
		if result.Status != "PASS" || result.Timeout != expected {
			t.Fatalf("effective config changed during Run: %+v", result)
		}
	}
	// Clear metadata so the first wait proves startup, not a previous stored snapshot.
	if e := c.Store.Update(func(s *model.State) error { s.SchedulerConfigHash = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	run(true)
	run(false)
}
