//go:build linux || (windows && release)

package scheduler

import (
	"context"
	"fmt"
	"testing"

	"scp-harness/internal/config"
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
	x, e := cfgRunner.Core.TestRunner.Control(context.Background(), []string{"test", "!", "-e", cfgRunner.Core.TestRunner.Root()}, nil, nil, 128)
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
				task, e := c.CreateTask(context.Background(), "excess transient input", repo, "refs/heads/main", c.Operator().ID, 1400000)
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
				if _, e = c.ReleaseOption(o.ID); e != nil {
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
					r, err := c.Runner.Control(context.Background(), []string{"test", "!", "-e", c.Runner.Root()}, nil, nil, 128)
					if err != nil || r.ExitCode != 0 {
						t.Fatalf("worker recovery cleanup: %v", err)
					}
				}
				if _, e = c.Lifecycle(context.Background(), task.ID, "suspend"); e != nil {
					t.Fatal(e)
				}
				other, e := c.CreateTask(context.Background(), "normal subsequent task", repo, "refs/heads/main", c.Operator().ID, 1320000)
				if e != nil {
					t.Fatal(e)
				}
				step(t, engine)
				c.Config.Workers[0].Command = []string{fixtureWorker(t), "success-worker"}
				fresh, e := c.Propose(other.ID, "normal workspace", "")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = c.Allocate(fresh.ID, 60000); e != nil {
					t.Fatal(e)
				}
				if _, e = c.ReleaseOption(fresh.ID); e != nil {
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
			// The protected copy lives in the test environment. Discarding it
			// must leave the worker's submitted workspace intact.
			r, e := c.Runner.Control(context.Background(), []string{"test", "-f", c.Runner.Root() + "/workspace/README.md"}, nil, nil, 128)
			if e != nil || r.ExitCode != 0 {
				t.Fatalf("protected test reused the worker environment: %v", e)
			}
			s := state(t, c)
			if s.LatestCI(submitted.ID) == nil || s.LatestCI(submitted.ID).Status != "PASS" || nextChangeStep(s, task.ID).Operation != "review" || c.Config.Limits != limits {
				t.Fatal("test finalization or configured limits changed")
			}
			if s.Artifacts[submitted.ID].SHA256 != submitted.SHA256 || len(s.Artifacts) != 1 {
				t.Fatal("test copy contaminated immutable Artifact")
			}
		})
	}
}
