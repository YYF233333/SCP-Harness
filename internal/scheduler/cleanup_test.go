package scheduler

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

func cleanupLimits(c *config.Config) {
	c.Limits.Files = 128
	c.Limits.Bytes = 131072
	c.Limits.Single = 16384
	c.Limits.Path = 128
}

func poisonScript(kind string, l config.Limits) string {
	prefix := "import os,pathlib\np=pathlib.Path(os.environ.get('SCP_WORKSPACE','.'))\n"
	switch kind {
	case "oversized_file":
		return prefix + fmt.Sprintf("with open(p/'oversized','wb') as f: f.truncate(%d)\n", 3*l.Bytes+1)
	case "many_files":
		return prefix + fmt.Sprintf("for i in range(%d): (p/('entry-'+str(i))).touch()\n", 3*l.Files+17)
	case "long_path":
		return prefix + "p=p/('a'*60)/('b'*60)/('c'*60)\np.mkdir(parents=True)\n(p/'leaf').write_text('data')\n"
	}
	panic("unknown poison fixture")
}

func noAttemptDirectory(t *testing.T, cfgRunner *Scheduler) {
	t.Helper()
	x, e := cfgRunner.Core.Runner.Control(context.Background(), []string{"test", "!", "-e", "/scp/attempt"}, nil, nil, 128)
	if e != nil || x.ExitCode != 0 {
		t.Fatalf("disposable attempt directory remains: %v %s", e, x.Stderr)
	}
}

func TestR3OversizedWorkspaceCleanupProgress(t *testing.T) {
	for _, kind := range []string{"oversized_file", "many_files", "long_path"} {
		for _, recoverFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/recover=%t", kind, recoverFirst), func(t *testing.T) {
				c, repo := integrationCore(t, "success-worker")
				cleanupLimits(c.Config)
				limits := c.Config.Limits
				task, e := c.CreateTask(context.Background(), "excess transient input", repo, "refs/heads/main", c.Operator().ID, 200000)
				if e != nil {
					t.Fatal(e)
				}
				engine := New(c)
				step(t, engine)
				o, e := c.Propose(task.ID, "poisoned workspace", "")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = c.Allocate(o.ID, 80000); e != nil {
					t.Fatal(e)
				}
				script := poisonScript(kind, limits) + "import json\nwith open(os.environ['SCP_RESULT'],'w') as f: json.dump(dict(schema_version=0,operation='mutation',disposition='DROP_FINAL',claims=[],new_options=[]),f)\n"
				c.Config.Workers[0].Command = []string{"python3", "-c", script}
				step(t, engine)
				s := state(t, c)
				for _, a := range s.Attempts {
					if a.AnchorID == o.ID && (a.ArtifactID != nil || a.Reason == nil || *a.Reason != "LIMIT_EXCEEDED") {
						t.Fatalf("normal acceptance limits weakened: %+v", a)
					}
				}
				if len(s.Artifacts) != 0 {
					t.Fatal("oversized workspace entered Artifact store")
				}
				if recoverFirst {
					if _, e = New(c).Recover(context.Background()); e != nil {
						t.Fatal(e)
					}
					noAttemptDirectory(t, engine)
				}
				if _, e = c.Lifecycle(context.Background(), task.ID, "suspend"); e != nil {
					t.Fatal(e)
				}
				other, e := c.CreateTask(context.Background(), "normal subsequent task", repo, "refs/heads/main", c.Operator().ID, 120000)
				if e != nil {
					t.Fatal(e)
				}
				step(t, engine)
				c.Config.Workers[0].Command = []string{"/opt/scp-workers/fake-worker", "success-worker"}
				fresh, e := c.Propose(other.ID, "normal workspace", "")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = c.Allocate(fresh.ID, 60000); e != nil {
					t.Fatal(e)
				}
				step(t, engine)
				s = state(t, c)
				ran := false
				for _, a := range s.Attempts {
					if a.AnchorID == fresh.ID {
						ran = true
						if a.Status != "RETURNED" || a.ArtifactID == nil || artifactText(t, s.Artifacts[*a.ArtifactID], "README.md") != "base\n" {
							t.Fatalf("fresh attempt failed: %+v", a)
						}
					}
				}
				if !ran || c.Config.Limits != limits {
					t.Fatal("fresh workspace or unchanged limits not proven")
				}
			})
		}
	}
}

func TestR3ProtectedCopyReallyDiscarded(t *testing.T) {
	for _, kind := range []string{"oversized_file", "many_files", "long_path"} {
		t.Run(kind, func(t *testing.T) {
			engine, task, _, submitted := candidate(t, "fake-reviewer-approve")
			c := engine.Core
			cleanupLimits(c.Config)
			limits := c.Config.Limits
			c.Config.Test.Command = []string{"python3", "-c", poisonScript(kind, limits)}
			step(t, engine)
			noAttemptDirectory(t, engine)
			s := state(t, c)
			if s.Tests[submitted.ID] == nil || s.Tests[submitted.ID].Outcome != "PASS" || s.Pending[task.ID].Operation != "review" || c.Config.Limits != limits {
				t.Fatal("test finalization or configured limits changed")
			}
			if s.Artifacts[submitted.ID].SHA256 != submitted.SHA256 || len(s.Artifacts) != 1 {
				t.Fatal("test copy contaminated immutable Artifact")
			}
		})
	}
}

func TestR3ProtectedCleanupFailureCannotPass(t *testing.T) {
	engine, task, _, submitted := candidate(t, "fake-reviewer-approve")
	c := engine.Core
	gate := "/tmp/scp-r3-" + model.ID()
	control := func(args ...string) {
		t.Helper()
		r, e := c.Runner.Control(context.Background(), args, nil, nil, 4096)
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("fixture control %v: %v %s", args, e, r.Stderr)
		}
	}
	control("python3", "-c", "import os,sys\nfor suffix in ('.ready','.release'): os.mkfifo(sys.argv[1]+suffix,0o666); os.chmod(sys.argv[1]+suffix,0o666)", gate)
	t.Cleanup(func() {
		_ = c.Runner.Terminate(context.Background())
		_, _ = c.Runner.Control(context.Background(), []string{"sh", "-c", "if test -e /scp/attempt/workspace/locked; then chattr -i /scp/attempt/workspace/locked; fi"}, nil, nil, 4096)
		_ = c.Runner.Files(context.Background(), "discard", nil, nil, nil, 4096)
		_, _ = c.Runner.Control(context.Background(), []string{"python3", "-c", "import os,sys\nfor suffix in ('.ready','.release'):\n try: os.unlink(sys.argv[1]+suffix)\n except FileNotFoundError: pass", gate}, nil, nil, 4096)
	})
	// Named pipes are barriers: inject the real filesystem fault only after the
	// protected copy exists, and let the test exit only after the flag is set.
	c.Config.Test.Command = []string{"python3", "-c", "import pathlib,sys\npathlib.Path('locked').write_text('disposable')\nwith open(sys.argv[1]+'.ready','wb',buffering=0) as f: f.write(b'1')\nwith open(sys.argv[1]+'.release','rb',buffering=0) as f: assert f.read(1)==b'1'", gate}
	pending := *state(t, c).Pending[task.ID]
	done := make(chan error, 1)
	go func() { _, e := engine.Step(context.Background()); done <- e }()
	control("python3", "-c", "import sys\nwith open(sys.argv[1]+'.ready','rb',buffering=0) as f: assert f.read(1)==b'1'", gate)
	control("chattr", "+i", "/scp/attempt/workspace/locked")
	control("python3", "-c", "import sys\nwith open(sys.argv[1]+'.release','wb',buffering=0) as f: f.write(b'1')", gate)
	e := <-done
	if model.Code(e) != "RUNNER_UNAVAILABLE" {
		t.Fatalf("cleanup failure was swallowed: %v", e)
	}
	s := state(t, c)
	if s.Tests[submitted.ID] != nil || !reflect.DeepEqual(s.Pending[task.ID], &pending) || s.Tasks[task.ID].Resources.Outstanding != 0 || s.Slot.State != "IDLE" {
		t.Fatal("failed cleanup published test result or lost pending step")
	}
	control("chattr", "-i", "/scp/attempt/workspace/locked")
	if _, e = New(c).Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	noAttemptDirectory(t, engine)
	for _, b := range s.Blockers {
		if b.Kind == "RUNNER_UNAVAILABLE" && b.Resolved == nil {
			if _, e = c.ResolveBlocker(b.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	c.Config.Test.Command = []string{"/opt/scp-workers/fake-worker", "protected-test"}
	step(t, engine)
	if state(t, c).Tests[submitted.ID].Outcome != "PASS" {
		t.Fatal("repaired protected runner did not resume")
	}
}
