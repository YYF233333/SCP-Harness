package scheduler

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
)

func fixtureGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: append([]string{"git.exe", "-C", repo}, args...), Timeout: 60 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
	if e != nil || r.ExitCode != 0 {
		t.Fatalf("fixture Git %v: %v %s", args, e, r.Stderr)
	}
	return strings.TrimSpace(string(r.Stdout))
}
func integrationCore(t *testing.T, mode string) (*core.Core, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Fatal("Windows 11 + dedicated SCP-Worker WSL2 integration is mandatory; not run on this host")
	}
	cfg, e := config.Load(filepath.Join("..", "..", "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cfg.Database = filepath.Join(dir, "state.db")
	cfg.Artifacts = filepath.Join(dir, "artifacts")
	cfg.Test.Command = []string{"/opt/scp-workers/fake-worker", "protected-test"}
	for i := range cfg.Workers {
		cfg.Workers[i].Command = []string{"/opt/scp-workers/fake-worker", mode}
	}
	c, e := core.Open(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Store.Close() })
	if e = c.Runner.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
	repo := filepath.Join(dir, "repo")
	if e = os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	fixtureGit(t, repo, "init", "-b", "main")
	if e = os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0644); e != nil {
		t.Fatal(e)
	}
	fixtureGit(t, repo, "add", "README.md")
	fixtureGit(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@local", "commit", "-m", "base")
	return c, repo
}
func state(t *testing.T, c *core.Core) *model.State {
	t.Helper()
	s, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func step(t *testing.T, s *Scheduler) {
	t.Helper()
	ran, e := s.Step(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !ran {
		t.Fatal("expected runnable step")
	}
}
func optionText(t *testing.T, c *core.Core, task, text string) *model.Option {
	t.Helper()
	for _, o := range state(t, c).Options {
		if o.TaskID == task && o.Text == text {
			return o
		}
	}
	t.Fatalf("Option %q not found", text)
	return nil
}
func artifactText(t *testing.T, a *model.Artifact, name string) string {
	t.Helper()
	f, e := os.Open(a.BlobPath)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	r := tar.NewReader(f)
	for {
		h, e := r.Next()
		if e == io.EOF {
			t.Fatalf("%s absent from Artifact", name)
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Name == name {
			b, e := io.ReadAll(r)
			if e != nil {
				t.Fatal(e)
			}
			return string(b)
		}
	}
}
func assertTree(t *testing.T, c *core.Core, repo, sha string, expected map[string]string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tree")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := c.Git.Export(context.Background(), repo, sha, root); e != nil {
		t.Fatal(e)
	}
	actual := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			r, _ := filepath.Rel(root, p)
			b, e := os.ReadFile(p)
			if e != nil {
				return e
			}
			actual[filepath.ToSlash(r)] = string(b)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) {
		t.Fatalf("tree mismatch: %s != %s", a, b)
	}
}

func TestFrozenVortonA10(t *testing.T) {
	c, repo := integrationCore(t, "vorton")
	ctx := context.Background()
	engine := New(c)
	t1, e := c.CreateTask(ctx, "build an agent-native language", repo, "refs/heads/main", c.Operator().ID, 600000)
	if e != nil {
		t.Fatal(e)
	}
	r0 := t1.SHA
	s := state(t, c)
	if t1.Status != "ACTIVE" || s.Accounts[t1.ID].Remaining != 600000 || t1.Resources.Minted != 600000 || s.Exploration[t1.ID].Done {
		t.Fatal("CP0")
	}
	t.Log("CP0 PASS")
	for i := 0; i < 3; i++ {
		step(t, engine)
	}
	s = state(t, c)
	oa := optionText(t, c, t1.ID, "benchmark-first")
	ob := optionText(t, c, t1.ID, "prototype-first-a")
	oc := optionText(t, c, t1.ID, "prototype-first-b")
	om := optionText(t, c, t1.ID, "prototype-first")
	if len(s.Options) != 4 || !s.Exploration[t1.ID].Done {
		t.Fatal("CP1 initial exploration")
	}
	parents := []string{}
	for _, edge := range s.Edges {
		if edge.Child == om.ID {
			parents = append(parents, edge.Parent)
		}
	}
	if len(parents) != 2 || parents[0] != ob.ID || parents[1] != oc.ID {
		t.Fatal("CP1 dedup lineage")
	}
	for _, o := range s.Options {
		if o.Remaining != 0 {
			t.Fatal("dedup minted allocation")
		}
	}
	if s.Accounts[t1.ID].Remaining != 600000-s.Tasks[t1.ID].Resources.Charged {
		t.Fatal("CP1 metering")
	}
	t.Log("CP1 PASS")
	beforeRoot := s.Accounts[t1.ID].Remaining
	if _, e = c.Allocate(om.ID, 240000); e != nil {
		t.Fatal(e)
	}
	// Scheduling may persist the funded OM target between the two authorized
	// transfers, before any lease/execution. This uses ordinary production Next.
	next, e := c.Next(engine.Owner)
	if e != nil || next == nil || next.OptionID != om.ID {
		t.Fatalf("CP2 select OM: %v", e)
	}
	if _, e = c.Allocate(oa.ID, 120000); e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	if s.Accounts[om.ID].Remaining != 240000 || s.Accounts[oa.ID].Remaining != 120000 || s.Accounts[t1.ID].Remaining != beforeRoot-360000 {
		t.Fatal("CP2 exact transfers")
	}
	t.Log("CP2 PASS")
	for i := 0; i < 8; i++ {
		step(t, engine)
	}
	s = state(t, c)
	if s.Pending[t1.ID] != nil || s.Tasks[t1.ID].SHA == r0 || s.Options[om.ID].Status != "OPEN" {
		t.Fatal("CP3 promotion chain")
	}
	r1 := s.Tasks[t1.ID].SHA
	arts := []*model.Artifact{}
	for _, a := range s.Artifacts {
		if a.Anchor == om.ID {
			arts = append(arts, a)
		}
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Created < arts[j].Created })
	if len(arts) != 3 {
		t.Fatalf("CP3 expected A1,A2,A3; got %d", len(arts))
	}
	for i, text := range []string{"one\n", "two\n", "fixed\n"} {
		if artifactText(t, arts[i], "stage.txt") != text {
			t.Fatal("CP3 immutable contents")
		}
		if arts[i].BaseSHA != r0 {
			t.Fatal("CP3 base provenance")
		}
	}
	if s.Tests[arts[1].ID].Outcome != "PASS" || s.Tests[arts[2].ID].Outcome != "PASS" || s.Reviews[arts[1].ID].Verdict != "REJECT" || s.Reviews[arts[2].ID].Verdict != "APPROVE" {
		t.Fatal("CP3 review sequence")
	}
	if len(s.Reviews[arts[1].ID].Findings) != 1 || s.Reviews[arts[1].ID].Findings[0] != "missing-final-fix" {
		t.Fatal("CP3 frozen finding")
	}
	for _, a := range s.Attempts {
		if a.AnchorID == om.ID && a.Operation == "mutation" && a.TargetType == "ARTIFACT" {
			if a.TargetID != arts[0].ID && a.TargetID != arts[1].ID {
				t.Fatal("wrong continuation/rework target")
			}
		}
	}
	assertTree(t, c, repo, r1, map[string]string{"README.md": "base\n", "stage.txt": "fixed\n"})
	t.Log("CP3 PASS")
	remaining := s.Accounts[om.ID].Remaining
	root := s.Accounts[t1.ID].Remaining
	if _, e = c.CloseOption(ctx, om.ID); e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	if s.Options[om.ID].Status != "CLOSED" || s.Accounts[om.ID].Remaining != 0 || s.Accounts[t1.ID].Remaining != root+remaining || s.Tasks[t1.ID].Resources.Retired != 0 {
		t.Fatal("CP4")
	}
	t.Log("CP4 PASS")
	split, e := c.Split(oa.ID, []core.Child{{Text: "bad-worker", Wall: 40000}, {Text: "spare", Wall: 30000}}, false)
	if e != nil {
		t.Fatal(e)
	}
	bad, spare := split.Children[0], split.Children[1]
	if split.Parent.Remaining != 50000 || bad.Remaining != 40000 || spare.Remaining != 30000 || split.Parent.Status != "OPEN" {
		t.Fatal("CP5")
	}
	t.Log("CP5 PASS")
	// OA may take its normal RR turn after CP5; its missing final result has no
	// effect. The frozen bad-worker attempt is interrupted only after both child
	// generations really exist inside the dedicated distro.
	for {
		next, e = c.Next(engine.Owner)
		if e != nil || next == nil {
			t.Fatalf("CP6 next: %v", e)
		}
		if next.OptionID == bad.ID {
			break
		}
		step(t, engine)
	}
	done := make(chan error, 1)
	go func() { _, e := engine.Step(ctx); done <- e }()
	deadline := time.Now().Add(25 * time.Second)
	attemptID := ""
	pids := ""
	for time.Now().Before(deadline) {
		s = state(t, c)
		for _, a := range s.Attempts {
			if a.AnchorID == bad.ID && a.Status == "RUNNING" {
				attemptID = a.ID
			}
		}
		if attemptID != "" {
			r, e := c.Runner.Control(ctx, []string{"cat", "/tmp/scp-bad-pids-" + attemptID}, nil, nil, 4096)
			if e == nil && r.ExitCode == 0 && len(strings.Fields(string(r.Stdout))) == 3 {
				pids = string(r.Stdout)
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	if attemptID == "" || pids == "" {
		t.Fatal("CP6 real child/grandchild did not appear")
	}
	interrupted, e := c.Interrupt(ctx, attemptID)
	if e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if interrupted.Status != "INTERRUPTED" || interrupted.ArtifactID == nil {
		t.Fatal("CP6 terminal/capture")
	}
	s = state(t, c)
	if s.Options[bad.ID].Status != "OPEN" || s.Pending[t1.ID] != nil || artifactText(t, s.Artifacts[*interrupted.ArtifactID], "bad.txt") != "interrupted\n" {
		t.Fatal("CP6 semantics")
	}
	r, e := c.Runner.Control(ctx, []string{"sh", "-c", "pgrep -f '/opt/scp-workers/fake-worker forever' | grep -v $$ || true"}, nil, nil, 4096)
	if e != nil || strings.TrimSpace(string(r.Stdout)) != "" {
		t.Fatalf("CP6 descendants survived: %v %s", e, r.Stdout)
	}
	t.Log("CP6 PASS")
	if _, e = c.Lifecycle(ctx, t1.ID, "suspend"); e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	frozen, _ := json.Marshal(s.Options)
	if s.Tasks[t1.ID].Status != "SUSPENDED" || s.Pending[t1.ID] != nil {
		t.Fatal("CP7")
	}
	t.Log("CP7 PASS")
	t2, e := c.CreateTask(ctx, "emergency hotfix", repo, "refs/heads/main", c.Operator().ID, 120000)
	if e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	oe := optionText(t, c, t2.ID, "emergency-hotfix")
	if _, e = c.Allocate(oe.ID, 80000); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 4; i++ {
		step(t, engine)
	}
	s = state(t, c)
	r2 := s.Tasks[t2.ID].SHA
	if r2 == r1 || s.Options[oe.ID].Status != "OPEN" {
		t.Fatal("CP8 promotion")
	}
	for _, a := range s.Attempts {
		if a.TaskID == t2.ID && (a.Operation == "merge_judge" || a.Operation == "merge_synth") {
			t.Fatal("CP8 single-option dedup ran")
		}
	}
	assertTree(t, c, repo, r2, map[string]string{"README.md": "base\n", "stage.txt": "fixed\n", "hotfix.txt": "emergency\n"})
	if _, e = c.CloseOption(ctx, oe.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Lifecycle(ctx, t2.ID, "close"); e != nil {
		t.Fatal(e)
	}
	t.Log("CP8 PASS")
	if _, e = c.Lifecycle(ctx, t1.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	if s.Tasks[t1.ID].SHA != r2 {
		t.Fatal("CP9 resume SHA")
	}
	old := map[string]*model.Option{}
	for id, o := range s.Options {
		if o.TaskID == t1.ID {
			old[id] = o
		}
	}
	retained, _ := json.Marshal(old)
	if string(retained) != string(frozen) {
		t.Fatal("CP9 historical provenance rewritten")
	}
	if _, e = c.Lifecycle(ctx, t1.ID, "close"); e != nil {
		t.Fatal(e)
	}
	s = state(t, c)
	for _, id := range []string{t1.ID, t2.ID} {
		task := s.Tasks[id]
		r := task.Resources
		if task.Status != "CLOSED" || r.Remaining != 0 || r.Outstanding != 0 || r.Minted != r.Charged+r.Retired {
			t.Fatal("CP9 terminal conservation")
		}
	}
	if s.Tasks[t1.ID].Resources.Minted != 600000 || s.Tasks[t2.ID].Resources.Minted != 120000 {
		t.Fatal("implicit mint")
	}
	if s.Slot.State != "IDLE" {
		t.Fatal("execution slot retained")
	}
	assertTree(t, c, repo, r2, map[string]string{"README.md": "base\n", "stage.txt": "fixed\n", "hotfix.txt": "emergency\n"})
	t.Log("CP9 PASS")
	for _, a := range s.Artifacts {
		dst := filepath.Join(t.TempDir(), "restored")
		if e = os.Mkdir(dst, 0700); e != nil {
			t.Fatal(e)
		}
		if e = artifact.Restore(*a, dst, c.Config.Limits); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("frozen Vorton complete, real charges: T1=%d T2=%d", s.Tasks[t1.ID].Resources.Charged, s.Tasks[t2.ID].Resources.Charged)
}

func TestRealWorkerLifecycle(t *testing.T) {
	for _, tc := range []struct {
		mode, status string
		artifact     bool
	}{{"success-worker", "RETURNED", true}, {"crash-worker", "CRASHED", true}, {"exit-127-worker", "CRASHED", true}, {"timeout-worker", "TIMED_OUT", true}, {"missing-result-worker", "RETURNED", true}, {"invalid-json-worker", "RETURNED", true}, {"huge-output-worker", "RETURNED", true}, {"worker-unavailable", "TERMINATED", true}, {"special-worker", "RETURNED", false}, {"malicious-git-worker", "RETURNED", true}} {
		t.Run(tc.mode, func(t *testing.T) {
			c, repo := integrationCore(t, "success-worker")
			task, e := c.CreateTask(context.Background(), tc.mode, repo, "refs/heads/main", c.Operator().ID, 120000)
			if e != nil {
				t.Fatal(e)
			}
			engine := New(c)
			step(t, engine)
			o, e := c.Propose(task.ID, "route", "")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.Allocate(o.ID, 60000); e != nil {
				t.Fatal(e)
			}
			c.Config.Workers[0].Command = []string{"/opt/scp-workers/fake-worker", tc.mode}
			if tc.mode == "timeout-worker" {
				c.Config.Workers[0].Timeout = 3000
			}
			_, e = engine.Step(context.Background())
			if tc.mode == "worker-unavailable" {
				if model.Code(e) != "WORKER_UNAVAILABLE" {
					t.Fatalf("unavailability: %v", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			s := state(t, c)
			var a *model.Attempt
			for _, v := range s.Attempts {
				if v.AnchorID == o.ID {
					a = v
				}
			}
			if a == nil || a.Status != tc.status || (a.ArtifactID != nil) != tc.artifact {
				t.Fatalf("Attempt: %+v expected %s Artifact=%v", a, tc.status, tc.artifact)
			}
			if s.Options[o.ID].Status != "OPEN" {
				t.Fatal("worker exit closed Option")
			}
			for _, p := range []string{a.Stdout, a.Stderr} {
				info, e := os.Stat(p)
				if e != nil {
					t.Fatal(e)
				}
				if info.Size() > c.Config.Limits.Stdout {
					t.Fatal("unbounded process log")
				}
			}
			if tc.mode == "worker-unavailable" {
				count := len(s.Attempts)
				ran, e := engine.Step(context.Background())
				if e != nil || ran || len(state(t, c).Attempts) != count {
					t.Fatal("blocked profile auto-retried")
				}
				for _, b := range s.Blockers {
					if b.Kind != "WORKER_UNAVAILABLE" || b.Scope != "WORKER_PROFILE" {
						t.Fatal("wrong blocker")
					}
					c.Config.Workers[0].Command = []string{"/opt/scp-workers/fake-worker", "success-worker"}
					if _, e = c.ResolveBlocker(b.ID); e != nil {
						t.Fatal(e)
					}
				}
				step(t, engine)
			}
			t.Log(fmt.Sprintf("%s real lifecycle PASS", tc.mode))
		})
	}
}
