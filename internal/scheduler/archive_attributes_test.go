//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"scp-harness/internal/model"
)

func TestR4UnavailableSnapshotHasNoCandidateEffects(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	if e := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("README.md export-ignore\n"), 0600); e != nil {
		t.Fatal(e)
	}
	fixtureGit(t, repo, "add", ".gitattributes")
	fixtureGit(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit", "-m", "unsupported archive feature")
	task, e := c.CreateTask(context.Background(), "unsupported snapshot", repo, "refs/heads/main", c.Operator().ID, 120000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	step(t, engine)
	option, e := c.Propose(task.ID, "route", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(option.ID, 60000); e != nil {
		t.Fatal(e)
	}
	pending, e := c.Next(engine.Owner)
	if e != nil || pending == nil {
		t.Fatalf("pending mutation: %v", e)
	}
	c.Git.Calls = map[string]int{}
	_, e = engine.Step(context.Background())
	if model.Code(e) != "REPOSITORY_UNAVAILABLE" {
		t.Fatalf("unsupported input misclassified: %v", e)
	}
	s := state(t, c)
	var attempt *model.Attempt
	for _, a := range s.Attempts {
		if a.AnchorID == option.ID {
			attempt = a
		}
	}
	if attempt == nil || attempt.Status != "TERMINATED" || attempt.Reason == nil || *attempt.Reason != "REPOSITORY_UNAVAILABLE" || attempt.ExitCode != nil {
		t.Fatalf("snapshot rejection changed by later capture: %+v", attempt)
	}
	if !reflect.DeepEqual(s.Pending[task.ID], pending) || len(s.Artifacts) != 0 || len(s.Journals) != 0 || len(s.Reviews) != 0 || s.Tasks[task.ID].SHA != task.SHA || s.Options[option.ID].Status != "OPEN" {
		t.Fatal("unsupported snapshot produced candidate/semantic effects")
	}
	if c.Git.Calls["export_attr_inspection"] != 1 || c.Git.Calls["export_tree"] != 0 || c.Git.Calls["synthetic_git"] != 0 {
		t.Fatalf("rejected input crossed archive boundary: %+v", c.Git.Calls)
	}
	if !s.Blocked(task.ID, "operator", c.Config.WSL.Distro) || s.Slot.State != "IDLE" || s.Tasks[task.ID].Resources.Outstanding != 0 {
		t.Fatal("rejection did not settle and block Task")
	}
}
