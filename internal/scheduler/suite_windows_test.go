//go:build windows && release

package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"scp-harness/internal/model"
	"scp-harness/internal/wsl"
)

func TestFrozenVortonA10(t *testing.T) { testFrozenVortonA10(t) }

func TestRealWorkerLifecycle(t *testing.T) { testRealWorkerLifecycle(t) }

func TestRunningSchedulerSuspendSerializationSignalAndStaleLock(t *testing.T) {
	testRunningSchedulerControl(t)
}

func TestR1bControlExitRequiresExplicitRecovery(t *testing.T) { testControlExitRecovery(t) }

func TestActualCoreCrashCapturesWorkerAndChargesFullLease(t *testing.T) { testCoreCrashRecovery(t) }

func TestActualInteropAndAutomountIsolation(t *testing.T) {
	c, _ := integrationCore(t, "success-worker")
	marker := "/tmp/scp-environment-" + model.ID()
	for _, runner := range []wsl.Runner{c.Runner, c.TestRunner} {
		r, e := runner.Control(context.Background(), []string{"sh", "-c", `printf '%s' "$1" > "$2"`, "marker", fmt.Sprint(runner.Protected), marker}, nil, nil, 1024)
		if e != nil || r.ExitCode != 0 {
			t.Fatal("environment marker", e)
		}
		t.Cleanup(func() { _, _ = runner.Control(context.Background(), []string{"rm", "-f", marker}, nil, nil, 1024) })
	}
	for _, runner := range []wsl.Runner{c.Runner, c.TestRunner} {
		r, e := runner.Control(context.Background(), []string{"cat", marker}, nil, nil, 1024)
		if e != nil || r.ExitCode != 0 || string(r.Stdout) != fmt.Sprint(runner.Protected) {
			t.Fatal("worker and protected test share a filesystem", e)
		}
		pe, e := os.Open(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
		if e != nil {
			t.Fatal(e)
		}
		r, e = runner.Control(context.Background(), []string{"sh", "-c", "cat > /opt/scp-workers/interop-probe.exe && chmod 755 /opt/scp-workers/interop-probe.exe"}, pe, nil, 1024)
		pe.Close()
		if e != nil || r.ExitCode != 0 {
			t.Fatal("interop probe transfer", e)
		}
		r, e = runner.Run(context.Background(), "scp", []string{"/opt/scp-workers/interop-probe.exe", "/c", "exit", "0"}, nil, nil, 5*time.Second, 65536, 65536)
		if e == nil && r.ExitCode == 0 {
			t.Fatal("Windows interop is enabled")
		}
		r, e = runner.Control(context.Background(), []string{"test", "!", "-d", "/mnt/c/Windows"}, nil, nil, 1024)
		if e != nil || r.ExitCode != 0 {
			t.Fatal("Windows host filesystem visible", e)
		}
	}
}
