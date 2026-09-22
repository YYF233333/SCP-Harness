//go:build linux || (windows && release)

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/gitrepo"
)

// Final acceptance supplies the exact delivered executable. Ordinary go test
// builds the same source itself; neither path substitutes a fake CLI/Core.
func acceptanceExecutable(t *testing.T, root, dir string) string {
	t.Helper()
	executable := os.Getenv("SCP_ACCEPTANCE_EXE")
	if executable == "" {
		executable = filepath.Join(dir, "scp")
		if runtime.GOOS == "windows" {
			executable += ".exe"
		}
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{"go", "build", "-trimpath", "-buildvcs=true", "-o", executable, "./cmd/scp"}, Dir: root, Timeout: 2 * time.Minute, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("build acceptance executable: %v %s", e, r.Stderr)
		}
	}
	return executable
}

func testAcceptanceExecutable(t *testing.T) {
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	executable := acceptanceExecutable(t, root, dir)
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.Database = filepath.Join(dir, "state.db")
	cfg.Artifacts = filepath.Join(dir, "artifacts")
	for id, path := range cfg.RoleCards {
		if !filepath.IsAbs(path) {
			cfg.RoleCards[id] = filepath.Join(root, path)
		}
	}
	data, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	cfgPath := filepath.Join(dir, "scp.json")
	if e = os.WriteFile(cfgPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	call := func(args ...string) map[string]any {
		t.Helper()
		argv := append([]string{executable, "--config", cfgPath, "--json"}, args...)
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: argv, Timeout: 30 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("actual scp.exe %v: %v exit=%d %s %s", args, e, r.ExitCode, r.Stdout, r.Stderr)
		}
		var envelope map[string]any
		decoder := json.NewDecoder(bytes.NewReader(r.Stdout))
		if e = decoder.Decode(&envelope); e != nil {
			t.Fatal(e)
		}
		var extra any
		if e = decoder.Decode(&extra); e != io.EOF {
			t.Fatal("CLI emitted more than one JSON object")
		}
		if envelope["ok"] != true {
			t.Fatal(envelope)
		}
		return envelope["data"].(map[string]any)
	}
	call("init")
	repo := filepath.Join(dir, "repo")
	if e = os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) {
		t.Helper()
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: append([]string{gitrepo.Executable(), "-C", repo}, args...), Timeout: 30 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("Git fixture: %v %s", e, r.Stderr)
		}
	}
	git("init", "-b", "main")
	if e = os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0644); e != nil {
		t.Fatal(e)
	}
	git("add", "README.md")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit", "-m", "base")
	task := call("task", "create", "--objective", "binary acceptance", "--repo", repo, "--ref", "refs/heads/main", "--responsible-actor", cfg.Operator, "--wall-ms", "1210000")
	id := task["id"].(string)
	option := call("option", "propose", "--task", id, "--text", "route")
	call("option", "allocate", option["id"].(string), "--wall-ms", "1000")
	before := call("task", "show", id)["task"].(map[string]any)["resources"]
	call("claim", "create", "--task", id, "--subject-type", "TASK", "--subject-id", id, "--type", "resource.propose")
	after := call("task", "show", id)["task"].(map[string]any)["resources"]
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(after)
	if !bytes.Equal(left, right) {
		t.Fatal("actual executable proposal moved budget")
	}
	closed := call("task", "close", id)
	resources := closed["resources"].(map[string]any)
	if closed["status"] != "CLOSED" || resources["remaining_wall_ms"] != float64(0) || resources["retired_wall_ms"] != float64(1210000) {
		t.Fatal("actual executable close ledger")
	}
	status := call("status")
	if len(status["tasks"].([]any)) != 0 || status["fail_stop"] != false {
		t.Fatal("actual executable final status")
	}
	t.Logf("Verified actual executable: %s", executable)
}
