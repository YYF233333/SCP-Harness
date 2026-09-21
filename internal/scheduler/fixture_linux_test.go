//go:build linux

package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/core"
	"scp-harness/internal/wsl"
)

var localFixtureExecutable string

func TestMain(m *testing.M) {
	if os.Getenv("SCP_TEST_CRASH_PHASE") != "" {
		os.Exit(m.Run())
	}
	dir, e := os.MkdirTemp("", "scp-fixture-*")
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	localFixtureExecutable = filepath.Join(dir, "fake-worker")
	r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{"go", "build", "-o", localFixtureExecutable, "../../testdata/workers/main.go"}, Timeout: 2 * time.Minute, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
	code := 1
	if e != nil || r.ExitCode != 0 {
		fmt.Fprintf(os.Stderr, "build local fixture: %v %s\n", e, r.Stderr)
	} else {
		code = m.Run()
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

func fixtureWorker(t *testing.T) string {
	t.Helper()
	return localFixtureExecutable
}

func prepareIntegration(t *testing.T, c *core.Core) {
	t.Helper()
	if e := c.Runner.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for _, runner := range []wsl.Runner{c.Runner, c.TestRunner} {
			if e := runner.Terminate(context.Background()); e != nil {
				t.Error(e)
			}
			if e := runner.Files(context.Background(), "discard", nil, nil, nil, 4096); e != nil {
				t.Error(e)
			}
		}
	})
}
