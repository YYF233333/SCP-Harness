//go:build windows && release

package scheduler

import (
	"context"
	"testing"

	"scp-harness/internal/core"
)

func fixtureWorker(t *testing.T) string {
	t.Helper()
	return "/opt/scp-workers/fake-worker"
}

func prepareIntegration(t *testing.T, c *core.Core) {
	t.Helper()
	if e := c.Runner.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := c.TestRunner.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
}
