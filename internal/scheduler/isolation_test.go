//go:build linux || (windows && release)

package scheduler

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/gitrepo"
	"scp-harness/internal/model"
)

func Test10000CommitSyntheticHistory(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	var history bytes.Buffer
	base := fixtureGit(t, repo, "rev-parse", "HEAD")
	for i := 1; i < 10000; i++ {
		fmt.Fprintf(&history, "commit refs/heads/main\nmark :%d\ncommitter Fixture <fixture@local> %d +0000\ndata 1\nx\n", i, 1000000000+i)
		if i == 1 {
			fmt.Fprintf(&history, "from %s\n", base)
		} else {
			fmt.Fprintf(&history, "from :%d\n", i-1)
		}
		fmt.Fprint(&history, "\n")
	}
	fmt.Fprint(&history, "done\n")
	r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{gitrepo.Executable(), "-C", repo, "fast-import", "--quiet"}, Stdin: &history, Timeout: 60 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
	if e != nil || r.ExitCode != 0 {
		t.Fatalf("history fixture: %v %s", e, r.Stderr)
	}
	task, e := c.CreateTask(context.Background(), "history isolation", repo, "refs/heads/main", c.Operator().ID, 120000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	o, e := c.Propose(task.ID, "isolated Git", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 60000); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "malicious-git-worker"}
	card := c.Config.Cards["operator"]
	card.Context = append(card.Context, "repository.snapshot")
	c.Config.Cards["operator"] = card
	c.Git.Calls = map[string]int{}
	step(t, engine)
	if c.Git.Calls["resolve_ref"] > 1 || c.Git.Calls["export_attr_inspection"] != 1 || c.Git.Calls["export_tree"] != 1 || c.Git.Calls["synthetic_git"] > 4 {
		t.Fatalf("startup Git calls %+v", c.Git.Calls)
	}
	s := state(t, c)
	var a *model.Artifact
	for _, v := range s.Artifacts {
		a = v
	}
	if a == nil || artifactText(t, a, "git-count.txt") != "1\n" {
		t.Fatal("synthetic history length")
	}
	f, e := os.Open(a.BlobPath)
	if e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(f)
	for {
		h, e := tr.Next()
		if e != nil {
			break
		}
		if strings.HasPrefix(h.Name, ".git/") {
			t.Fatal("synthetic object database captured")
		}
	}
	f.Close()

}
func TestUnauthorizedReviewAndOneTimeIndependentExploration(t *testing.T) {
	engine, task, _, a := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	step(t, engine)
	card := c.Config.Cards["reviewer"]
	caps := []config.Capability{}
	for _, cap := range card.Capabilities {
		if cap.Name != "review.decide" {
			caps = append(caps, cap)
		}
	}
	card.Capabilities = caps
	c.Config.Cards["reviewer"] = card
	step(t, engine)
	s := state(t, c)
	if s.Reviews[a.ID].Verdict != "REJECT" || s.Pending[task.ID].Operation != "mutation" {
		t.Fatal("unauthorized review acquired authority")
	}
	if e := c.Release(engine.Owner); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Lifecycle(context.Background(), task.ID, "suspend"); e != nil {
		t.Fatal(e)
	}
	c.Config.Exploration.N = 2
	c.Config.Workers[2].Command = []string{fixtureWorker(t), "generate-one-worker"}
	second, e := c.CreateTask(context.Background(), "two fresh explorers", task.RepoPath, task.RepoRef, c.Operator().ID, 120000)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		step(t, engine)
	}
	s = state(t, c)
	if !s.Exploration[second.ID].Done || s.Exploration[second.ID].Count != 2 || len(s.Exploration[second.ID].Batch) != 2 {
		t.Fatal("fresh exploration count/dedup")
	}
	before := len(s.Attempts)
	if ran, e := New(c).Step(context.Background()); e != nil || ran || len(state(t, c).Attempts) != before {
		t.Fatal("initial exploration repeated after scheduler restart")
	}
}
func TestExecutableModeSurvivesCaptureTestReviewAndPromotion(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	task, e := c.CreateTask(context.Background(), "executable mode", repo, "refs/heads/main", c.Operator().ID, 120000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	o, e := c.Propose(task.ID, "script", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 60000); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{fixtureWorker(t), "executable-worker"}
	c.Config.Workers[1].Command = []string{fixtureWorker(t), "fake-reviewer-approve"}
	c.Config.Test.Command = []string{"./run.sh"}
	for i := 0; i < 4; i++ {
		step(t, engine)
	}
	s := state(t, c)
	if s.Tasks[task.ID].SHA == task.SHA {
		t.Fatal("executable script failed protected test/promotion")
	}
	info := fixtureGit(t, repo, "ls-tree", s.Tasks[task.ID].SHA, "run.sh")
	if !strings.HasPrefix(info, "100755 ") {
		t.Fatalf("promoted executable mode: %s", info)
	}
}
