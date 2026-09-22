//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"os"
	"testing"

	"scp-harness/internal/core"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
)

func candidate(t *testing.T, reviewMode string) (*Scheduler, *model.Task, *model.Option, *model.Artifact) {
	t.Helper()
	c, repo := integrationCore(t, "success-worker")
	ctx := context.Background()
	task, e := c.CreateTask(ctx, "transition", repo, "refs/heads/main", c.Operator().ID, 1500000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	o, e := c.Propose(task.ID, "route", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 120000); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "workspace-writer"}
	c.Config.Workers[1].Command = []string{fixtureWorker(t), reviewMode}
	step(t, engine)
	s := state(t, c)
	p := nextChangeStep(s, task.ID)
	if p == nil || p.Operation != "ci" {
		t.Fatal("candidate not pending test")
	}
	return engine, task, o, s.Artifacts[p.TargetID]
}
func TestProtectedTestCannotBeOverriddenAndReviewerReadonly(t *testing.T) {
	for _, kind := range []string{"fail", "timeout", "write"} {
		t.Run(kind, func(t *testing.T) {
			engine, task, o, a := candidate(t, "readonly-reviewer")
			c := engine.Core
			c.Config.Test.Command = []string{fixtureWorker(t), "protected-test", kind}
			if kind == "timeout" {
				c.Config.Test.Timeout = 2000
			}
			step(t, engine)
			step(t, engine)
			s := state(t, c)
			p := nextChangeStep(s, task.ID)
			if s.Reviews[a.ID].Verdict != "APPROVE" {
				t.Fatal("readonly reviewer failed")
			}
			if kind == "write" {
				if p.Operation != "await_promotion" {
					t.Fatal("valid CI not awaiting promotion")
				}
				authorizePromotion(t, c, task.ID)
				step(t, engine)
				s = state(t, c)
				assertTree(t, c, task.RepoPath, s.Tasks[task.ID].SHA, map[string]string{"README.md": "base\n", "written.txt": "workspace write\n"})
				if s.Options[o.ID].Status != "OPEN" {
					t.Fatal("promotion completed Option")
				}
			} else {
				if changeFor(s, task.ID).State != "BLOCKED" || changeFor(s, task.ID).ArtifactID != a.ID || s.Tasks[task.ID].SHA != task.SHA {
					t.Fatal("reviewer overrode protected test failure")
				}
				if len(s.Blockers) != 0 {
					t.Fatal("normal test failure became blocker")
				}
			}
			if artifactText(t, a, "README.md") != "base\n" {
				t.Fatal("reviewer changed immutable Artifact")
			}
		})
	}
}
func TestInvalidReviewReworksSameImmutableArtifact(t *testing.T) {
	engine, task, _, a := candidate(t, "invalid-json-worker")
	step(t, engine)
	step(t, engine)
	s := state(t, engine.Core)
	ch := changeFor(s, task.ID)
	if ch.State != "BLOCKED" || ch.ArtifactID != a.ID || s.Reviews[a.ID] != nil || len(s.Journals) != 0 {
		t.Fatal("invalid review must stop without invented verdict")
	}
	if _, e := engine.Core.ChangeAction(context.Background(), ch.ID, "rework"); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	s = state(t, engine.Core)
	if len(s.Artifacts) != 2 {
		t.Fatal("rework reused Artifact")
	}
	if artifactText(t, a, "written.txt") != "workspace write\n" {
		t.Fatal("rejected Artifact changed")
	}
}
func TestMissingAndCrashedReviewAreSyntheticRejects(t *testing.T) {
	for _, mode := range []string{"missing-result-worker", "exit-127-worker"} {
		t.Run(mode, func(t *testing.T) {
			engine, task, _, a := candidate(t, mode)
			step(t, engine)
			step(t, engine)
			s := state(t, engine.Core)
			if s.Reviews[a.ID] != nil || changeFor(s, task.ID).State != "BLOCKED" || changeFor(s, task.ID).ArtifactID != a.ID || len(s.Journals) != 0 {
				t.Fatal("absent review did not produce conservative rework")
			}
		})
	}
}
func TestBlockedPendingTestResumesAndResourcePauseKeepsTarget(t *testing.T) {
	engine, task, o, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	c.Config.Test.Command = []string{"/not-installed"}
	_, e := engine.Step(context.Background())
	if model.Code(e) != "RUNNER_UNAVAILABLE" {
		t.Fatalf("missing protected executable: %v", e)
	}
	s := state(t, c)
	if changeFor(s, task.ID).ArtifactID != a.ID || changeFor(s, task.ID).Stage != "CI" || s.Slot.State != "IDLE" {
		t.Fatal("lost blocked test target")
	}
	if len(s.Blockers) != 1 {
		t.Fatal("expected one protected runner blocker")
	}
	for _, b := range s.Blockers {
		if b.Kind != "RUNNER_UNAVAILABLE" || b.Scope != "RUNNER" || b.Subject == nil || *b.Subject != c.Config.WSL.TestDistro {
			t.Fatalf("protected runner blocker has wrong identity: %+v", b)
		}
	}
	ran, e := engine.Step(context.Background())
	if e != nil || ran {
		t.Fatal("automatic blocker retry")
	}
	// A different Task can still explore on the healthy worker distro.
	repo := t.TempDir()
	fixtureGit(t, repo, "init", "-b", "main")
	fixtureGit(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@local", "commit", "--allow-empty", "-m", "base")
	other, e := c.CreateTask(context.Background(), "independent worker exploration", repo, "refs/heads/main", c.Operator().ID, 1260000)
	if e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	s = state(t, c)
	if !s.Exploration[other.ID].Done || changeFor(s, task.ID).Stage != "CI" || changeFor(s, task.ID).ArtifactID != a.ID || len(s.CIRuns) != 1 {
		t.Fatal("runner blocker affected the independent Task or retried the protected test")
	}
	c.Config.Test.Command = []string{fixtureWorker(t), "protected-test"}
	if ran, e = engine.Step(context.Background()); e != nil || ran {
		t.Fatal("repaired runner retried before explicit resolve")
	}
	for _, b := range s.Blockers {
		if _, e = c.ResolveBlocker(b.ID); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = c.ChangeAction(context.Background(), a.ChangeID, "resume"); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	// Control edits on other Options remain available while a Change is runnable.
	s = state(t, c)
	balance := s.Accounts[o.ID].Remaining
	otherOption, e := c.Propose(task.ID, "control edit", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Split(otherOption.ID, []core.Child{{Text: "holding"}, {Text: "zero"}}, false); e != nil {
		t.Fatal(e)
	}
	// A settled lease can exhaust a chain's budget. Only allocation resumes it.
	if e = c.Store.Update(func(s *model.State) error {
		id := model.ID()
		if e := ledger.Reserve(s, id, o.ID, balance); e != nil {
			return e
		}
		return ledger.Settle(s, id, balance, false)
	}); e != nil {
		t.Fatal(e)
	}
	if ran, e = engine.Step(context.Background()); e != nil || ran {
		t.Fatal("unfunded review ran", e)
	}
	s = state(t, c)
	if changeFor(s, task.ID).Stage != "REVIEW" || changeFor(s, task.ID).ArtifactID != a.ID {
		t.Fatal("paused target changed")
	}
	if _, e = c.Allocate(o.ID, 60000); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ChangeAction(context.Background(), a.ChangeID, "resume"); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	s = state(t, c)
	if nextChangeStep(s, task.ID).Operation != "await_promotion" || nextChangeStep(s, task.ID).TargetID != a.ID {
		t.Fatal("review did not resume exact target")
	}
	authorizePromotion(t, c, task.ID)
	step(t, engine)
}
func TestRepositoryDriftEndsChainWithoutBlocker(t *testing.T) {
	engine, task, o, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	step(t, engine)
	step(t, engine)
	authorizePromotion(t, c, task.ID)
	tree := fixtureGit(t, task.RepoPath, "rev-parse", task.SHA+"^{tree}")
	external := fixtureGit(t, task.RepoPath, "-c", "user.name=external", "-c", "user.email=external@local", "commit-tree", tree, "-p", task.SHA, "-m", "external")
	fixtureGit(t, task.RepoPath, "update-ref", task.RepoRef, external, task.SHA)
	step(t, engine)
	s := state(t, c)
	if nextChangeStep(s, task.ID) != nil || len(s.Blockers) != 0 || s.Artifacts[a.ID] == nil {
		t.Fatal("drift became infrastructure/semantic failure")
	}
	if current := fixtureGit(t, task.RepoPath, "rev-parse", task.RepoRef); current != external {
		t.Fatal("drift overwritten")
	}
	if ran, e := engine.Step(context.Background()); e != nil || ran {
		t.Fatal("drift auto-retried", e)
	}
	if _, e := c.ChangeAction(context.Background(), a.ChangeID, "abort"); e != nil {
		t.Fatal(e)
	}
	if _, e := c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	s = state(t, c)
	if s.Tasks[task.ID].SHA != external {
		t.Fatal("future work did not observe current authority")
	}
	for _, art := range s.Artifacts {
		if art.ID != a.ID && art.BaseSHA != external {
			t.Fatal("future mutation started from stale authority")
		}
	}
}
func TestPersistentInfrastructureAndFailStop(t *testing.T) {
	t.Run("repository", func(t *testing.T) {
		engine, task, _, _ := candidate(t, "fake-reviewer-approve")
		c := engine.Core
		step(t, engine)
		if e := os.Rename(task.RepoPath, task.RepoPath+".offline"); e != nil {
			t.Fatal(e)
		}
		_, e := engine.Step(context.Background())
		if model.Code(e) != "REPOSITORY_UNAVAILABLE" {
			t.Fatalf("missing repo: %v", e)
		}
		s := state(t, c)
		if changeFor(s, task.ID).Stage != "REVIEW" || len(s.Blockers) != 1 {
			t.Fatal("repository blocker not persistent")
		}
		if e = os.Rename(task.RepoPath+".offline", task.RepoPath); e != nil {
			t.Fatal(e)
		}
		ran, e := engine.Step(context.Background())
		if ran || e != nil {
			t.Fatal("repo auto-retried without explicit resolve")
		}
		for _, b := range s.Blockers {
			if _, e = c.ResolveBlocker(b.ID); e != nil {
				t.Fatal(e)
			}
		}
		step(t, engine)
	})
	t.Run("runner_start_and_terminate", func(t *testing.T) {
		engine, task, _, _ := candidate(t, "fake-reviewer-approve")
		original := os.Getenv("PATH")
		if e := os.Setenv("PATH", t.TempDir()); e != nil {
			t.Fatal(e)
		}
		_, e := engine.Step(context.Background())
		if re := os.Setenv("PATH", original); re != nil {
			t.Fatal(re)
		}
		if model.Code(e) != "RUNNER_UNAVAILABLE" {
			t.Fatalf("runner mechanism failure: %v", e)
		}
		s := state(t, engine.Core)
		if changeFor(s, task.ID).Stage != "CI" || !s.Blocked(task.ID, "", engine.Core.Config.WSL.TestDistro) {
			t.Fatal("runner blocker")
		}
	})
	t.Run("artifact_storage", func(t *testing.T) {
		engine, task, _, a := candidate(t, "fake-reviewer-approve")
		if e := os.Rename(a.BlobPath, a.BlobPath+".offline"); e != nil {
			t.Fatal(e)
		}
		_, e := engine.Step(context.Background())
		if model.Code(e) != "STORAGE_FAILURE" {
			t.Fatalf("artifact I/O: %v", e)
		}
		s := state(t, engine.Core)
		if !s.FailStop() || s.Tasks[task.ID].Status != "ACTIVE" {
			t.Fatal("artifact failure did not globally stop")
		}
	})
	t.Run("SQLite_durable_write", func(t *testing.T) {
		c, repo := integrationCore(t, "success-worker")
		task, e := c.CreateTask(context.Background(), "storage", repo, "refs/heads/main", c.Operator().ID, 1210000)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.Store.DB.Exec("CREATE TRIGGER force_write_failure BEFORE UPDATE ON core_state BEGIN SELECT RAISE(FAIL, 'injected durable write failure'); END"); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Extend(task.ID, 1); model.Code(e) != "STORAGE_FAILURE" {
			t.Fatalf("SQLite failure: %v", e)
		}
		s := state(t, c)
		if !s.FailStop() || s.Tasks[task.ID].Resources.Minted != 1210000 {
			t.Fatal("SQLite fail-stop/atomicity")
		}
		if _, e = c.Store.DB.Exec("DROP TRIGGER force_write_failure"); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Extend(task.ID, 1); model.Code(e) != "BLOCKED" {
			t.Fatal("repair implicitly cleared global blocker")
		}
		for _, b := range s.Blockers {
			if _, e = c.ResolveBlocker(b.ID); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = c.Extend(task.ID, 1); e != nil {
			t.Fatal(e)
		}
	})
}
