//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
)

func saveConfig(t *testing.T, c *core.Core) string {
	t.Helper()
	copy := *c.Config
	copy.RoleCards = map[string]string{}
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	for id, p := range c.Config.RoleCards {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		copy.RoleCards[id] = p
	}
	data, e := json.Marshal(copy)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(filepath.Dir(copy.Database), "scp.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

// An independent OS process exits between the same production journal methods
// used by Scheduler, or is killed while a real configured WSL worker runs.
func TestRecoveryProcessHelper(t *testing.T) {
	phase := os.Getenv("SCP_TEST_CRASH_PHASE")
	if phase == "" {
		return
	}
	cfg, e := config.Load(os.Getenv("SCP_TEST_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := core.Open(cfg, false)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	ctx := context.Background()
	if phase == "running" {
		_, e = engine.Step(ctx)
		if e != nil {
			t.Fatal(e)
		}
		t.Fatal("blocking worker unexpectedly returned")
	}
	p, e := c.Next(engine.Owner)
	if e != nil || p == nil || p.Operation != "promotion" {
		t.Fatalf("promotion pending: %v", e)
	}
	s, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	a, task := s.Artifacts[p.TargetID], s.Tasks[p.TaskID]
	dir, e := engine.temp("journal-crash-" + model.ID())
	if e != nil {
		t.Fatal(e)
	}
	tree := filepath.Join(dir, "tree")
	if e = os.Mkdir(tree, 0700); e != nil {
		t.Fatal(e)
	}
	if e = artifact.Restore(*a, tree, cfg.Limits); e != nil {
		t.Fatal(e)
	}
	sha, e := c.Git.Construct(ctx, *task, *a, tree)
	if e != nil {
		t.Fatal(e)
	}
	j := &model.Journal{ID: model.ID(), TaskID: task.ID, ArtifactID: a.ID, Ref: task.RepoRef, OldSHA: a.BaseSHA, NewSHA: sha, State: "PREPARED", Created: model.Now()}
	if e = c.PreparePromotion(j); e != nil {
		t.Fatal(e)
	}
	if phase == "before_cas" {
		os.Exit(91)
	}
	if e = c.Git.CAS(ctx, task.RepoPath, task.RepoRef, j.OldSHA, j.NewSHA); e != nil {
		t.Fatal(e)
	}
	os.Exit(92)
}
func crashCommand(t *testing.T, path, phase string) boundedexec.Command {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return boundedexec.Command{Argv: []string{exe, "-test.run=^TestRecoveryProcessHelper$", "-test.v"}, Env: []string{"SCP_TEST_CRASH_PHASE=" + phase, "SCP_TEST_CONFIG=" + path}, Timeout: 60 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20}
}
func TestPromotionJournalActualProcessCrashAndRecovery(t *testing.T) {
	for _, phase := range []string{"before_cas", "after_cas", "conflict"} {
		t.Run(phase, func(t *testing.T) {
			engine, task, _, a := candidate(t, "fake-reviewer-approve")
			c := engine.Core
			step(t, engine)
			step(t, engine)
			authorizePromotion(t, c, task.ID)
			if e := c.Release(engine.Owner); e != nil {
				t.Fatal(e)
			}
			path := saveConfig(t, c)
			childPhase := phase
			if phase == "conflict" {
				childPhase = "before_cas"
			}
			r, e := boundedexec.Run(context.Background(), crashCommand(t, path, childPhase))
			if e != nil || r.ExitCode != 91 && r.ExitCode != 92 {
				t.Fatalf("crash helper: %v %s %s", e, r.Stdout, r.Stderr)
			}
			s := state(t, c)
			var j *model.Journal
			for _, v := range s.Journals {
				j = v
			}
			if j == nil || j.State != "PREPARED" {
				t.Fatal("journal not durable before crash")
			}
			if phase == "conflict" {
				tree := fixtureGit(t, task.RepoPath, "rev-parse", task.SHA+"^{tree}")
				other := fixtureGit(t, task.RepoPath, "-c", "user.name=external", "-c", "user.email=external@local", "commit-tree", tree, "-p", task.SHA, "-m", "drift")
				fixtureGit(t, task.RepoPath, "update-ref", task.RepoRef, other, task.SHA)
			}
			recovered, e := New(c).Recover(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			s = state(t, c)
			if recovered.Journals != 1 || s.Slot.State != "IDLE" || s.Pending[task.ID] != nil {
				t.Fatal("promotion recovery not terminal")
			}
			if phase == "conflict" {
				if s.Journals[j.ID].State != "CONFLICT" || len(s.Blockers) != 0 {
					t.Fatal("recovery conflict misclassified")
				}
			} else if phase == "before_cas" {
				if s.Journals[j.ID].State != "NOT_APPLIED" || fixtureGit(t, task.RepoPath, "rev-parse", task.RepoRef) != j.OldSHA || changeFor(s, task.ID).Stage != "AWAIT_PROMOTION" {
					t.Fatal("recovery promoted without renewed authority")
				}
			} else {
				if s.Journals[j.ID].State != "APPLIED" || s.Tasks[task.ID].SHA != j.NewSHA {
					t.Fatal("recovery did not reconcile applied ref")
				}
				assertTree(t, c, task.RepoPath, j.NewSHA, map[string]string{"README.md": "base\n", "written.txt": "workspace write\n"})
			}
			if s.Artifacts[a.ID] == nil {
				t.Fatal("recovery lost Artifact")
			}
			again, e := New(c).Recover(context.Background())
			if e != nil || again.Journals != 0 {
				t.Fatal("journal recovery not idempotent")
			}
		})
	}
}
func testCoreCrashRecovery(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	task, e := c.CreateTask(context.Background(), "crash recovery", repo, "refs/heads/main", c.Operator().ID, 1500000)
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
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "vorton"}
	path := saveConfig(t, c)
	ctx, kill := context.WithCancel(context.Background())
	defer kill()
	done := make(chan boundedexec.Result, 1)
	go func() { r, _ := boundedexec.Run(ctx, crashCommand(t, path, "running")); done <- r }()
	deadline := time.Now().Add(30 * time.Second)
	var attempt *model.Attempt
	for time.Now().Before(deadline) {
		s := state(t, c)
		for _, a := range s.Attempts {
			if a.AnchorID == o.ID && a.Status == "RUNNING" {
				attempt = a
			}
		}
		if attempt != nil {
			r, e := c.Runner.Control(context.Background(), []string{"test", "-f", c.Runner.Root() + "/workspace/bad.txt"}, nil, nil, 128)
			if e == nil && r.ExitCode == 0 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if attempt == nil {
		t.Fatal("Core child did not run real worker")
	}
	kill()
	<-done
	report, e := New(c).Recover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	s := state(t, c)
	a := s.Attempts[attempt.ID]
	if len(report.Attempts) != 1 || a.Status != "CRASHED" || a.ArtifactID == nil || s.Accounts[o.ID].Remaining != 120000-a.Lease || s.Options[o.ID].Status != "OPEN" || nextChangeStep(s, task.ID) != nil {
		t.Fatalf("crash recovery state: %+v", a)
	}
	if artifactText(t, s.Artifacts[*a.ArtifactID], "bad.txt") != "interrupted\n" {
		t.Fatal("crashed workspace not captured")
	}
	if s.Tasks[task.ID].Resources.Outstanding != 0 {
		t.Fatal("crash retained lease")
	}
}
