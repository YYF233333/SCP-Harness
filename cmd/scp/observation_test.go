//go:build linux || (windows && release)

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/gitrepo"
	"scp-harness/internal/model"
	"scp-harness/internal/scheduler"
)

type observationOutput struct {
	sync.Mutex
	bytes.Buffer
}

func (b *observationOutput) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *observationOutput) text() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

func observationFixture(t *testing.T, script string) (*core.Core, string, string, string) {
	t.Helper()
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	exe := acceptanceExecutable(t, root, dir)
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.Database, cfg.Artifacts = filepath.Join(dir, "state.db"), filepath.Join(dir, "artifacts")
	for id, path := range cfg.RoleCards {
		cfg.RoleCards[id] = filepath.Join(root, path)
	}
	cfg.Workers[0].Command = []string{"sh", "-c", script}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "scp.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := core.Open(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Store.Close() })
	if runtime.GOOS == "linux" {
		// Match the existing local scheduler fixture: restore directory access via
		// Core's bounded discard before testing.TempDir removes its owned fixture.
		t.Cleanup(func() {
			if e := c.Runner.Terminate(context.Background()); e != nil {
				t.Error(e)
			}
			if e := c.Runner.Files(context.Background(), "discard", nil, nil, nil, 4096); e != nil {
				t.Error(e)
			}
		})
	}
	if e = c.Runner.Check(context.Background()); e != nil {
		t.Fatal(e)
	}
	repo := filepath.Join(dir, "repo")
	if e = os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) {
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: append([]string{gitrepo.Executable(), "-C", repo}, args...), Timeout: 30 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("git fixture: %v %s", e, r.Stderr)
		}
	}
	git("init", "-b", "main")
	for _, name := range []string{"modify.txt", "delete.txt"} {
		if e = os.WriteFile(filepath.Join(repo, name), []byte("base\n"), 0644); e != nil {
			t.Fatal(e)
		}
	}
	git("add", ".")
	git("-c", "user.name=fixture", "-c", "user.email=fixture@local", "commit", "-m", "base")
	task, e := c.CreateTask(context.Background(), "observation", repo, "refs/heads/main", c.Operator().ID, 180000)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Update(func(s *model.State) error { s.Exploration[task.ID].Done = true; return nil }); e != nil {
		t.Fatal(e)
	}
	o, e := c.Propose(task.ID, "observe", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(o.ID, 120000); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReleaseOption(o.ID); e != nil {
		t.Fatal(e)
	}
	return c, path, exe, task.ID
}

func observationState(t *testing.T, c *core.Core) []byte {
	t.Helper()
	s, e := c.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(s)
	return b
}

func waitObservation(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("observation deadline")
}

// This is a real opaque delayed worker. A host test barrier only shortens the
// delay after assertions; neither watch nor diff can write or release it.
const observationWorker = `set -eu
printf 'changed\n' > modify.txt
printf 'added\n' > added.txt
rm delete.txt
printf 'A\n'
printf 'err A\n' >&2
i=0
while test ! -f "$(dirname "$SCP_INPUT")/test-finish" && test "$i" -lt 500; do sleep 0.1; i=$((i+1)); done
printf 'B\n'
printf 'err B\n' >&2
sleep 0.2
printf '%s' '{"schema_version":0,"operation":"mutation","disposition":"DROP_FINAL","claims":[],"new_options":[]}' > "$SCP_RESULT"
`

func TestLiveAttemptObservation(t *testing.T) {
	c, configPath, exe, _ := observationFixture(t, observationWorker)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	joined := false
	go func() { _, e := scheduler.New(c).Step(ctx); done <- e }()
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	id := ""
	waitObservation(t, func() bool {
		s, e := c.Store.Read()
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range s.Attempts {
			id = a.ID
			return true
		}
		return false
	})
	// Enter before logs exist whenever PREPARING is still visible.
	var first, second observationOutput
	watchCtx, stop := context.WithCancel(context.Background())
	defer stop()
	w1 := make(chan error, 1)
	go func() { w1 <- c.WatchAttempt(watchCtx, id, &first) }()
	cmd := exec.Command(exe, "--config", configPath, "attempt", "watch", id)
	cmd.Stdout, cmd.Stderr = &second, &second
	prepareObservationSignal(cmd)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	w2 := make(chan error, 1)
	go func() { w2 <- cmd.Wait() }()
	watcherJoined := false
	t.Cleanup(func() {
		if !watcherJoined {
			_ = cmd.Process.Kill()
			<-w2
		}
	})
	waitObservation(t, func() bool {
		return strings.Contains(first.text(), "[stdout] A\n") && strings.Contains(second.text(), "[stderr] err A\n")
	})
	s, e := c.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	if s.Attempts[id].Status != "RUNNING" || strings.Contains(first.text(), "[stdout] B\n") {
		t.Fatal("A was not observed live")
	}
	t.Logf("O1: Attempt %s RUNNING; both watchers received A and err A before B/terminal", id)
	before := observationState(t, c)
	// Hash every workspace file including synthetic .git; this probe never runs Git.
	fingerprint := func() string {
		r, e := c.Runner.Control(context.Background(), []string{"python3", "-c", `import hashlib,os,sys
h=hashlib.sha256()
for root,dirs,files in os.walk(sys.argv[1]):
 dirs.sort()
 for name in sorted(files):
  p=os.path.join(root,name); h.update(os.path.relpath(p,sys.argv[1]).encode()); h.update(open(p,'rb').read())
print(h.hexdigest())`, c.Runner.Root() + "/workspace"}, nil, nil, 4096)
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("fingerprint: %v %s", e, r.Stderr)
		}
		return string(r.Stdout)
	}
	workspace := fingerprint()
	for i := 0; i < 2; i++ {
		r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{exe, "--config", configPath, "attempt", "diff", id}, Timeout: 20 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("live diff: %v %s %s", e, r.Stdout, r.Stderr)
		}
		for _, want := range []string{"BEST-EFFORT OBSERVATION", "A added.txt\n", "D delete.txt\n", "M modify.txt\n", "-base\n", "+changed\n"} {
			if !strings.Contains(string(r.Stdout), want) {
				t.Fatalf("missing %q: %s", want, r.Stdout)
			}
		}
	}
	if workspace != fingerprint() {
		t.Fatal("diff changed workspace or synthetic Git")
	}
	if !bytes.Equal(before, observationState(t, c)) {
		t.Fatal("watch/diff changed authority or ledger")
	}
	t.Log("O5/O6: actual CLI reported A/M/D while RUNNING; workspace + .git bytes and complete Core state/ledger unchanged")
	jsonResult, jsonError := boundedexec.Run(context.Background(), boundedexec.Command{Argv: []string{exe, "--config", configPath, "--json", "attempt", "diff", id}, Timeout: 20 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
	var envelope map[string]any
	if jsonError != nil || jsonResult.ExitCode != 0 || json.Unmarshal(jsonResult.Stdout, &envelope) != nil {
		t.Fatalf("JSON diff: %v %s %s", jsonError, jsonResult.Stdout, jsonResult.Stderr)
	}
	keys(t, envelope, "ok command data")
	keys(t, envelope["data"], "attempt_id observation text truncated")
	if envelope["data"].(map[string]any)["observation"] != "BEST-EFFORT OBSERVATION" {
		t.Fatal(envelope)
	}
	// A real OS Ctrl+C reaches only the watcher executable.
	if e = interruptObservationWatcher(cmd); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-w2:
		watcherJoined = true
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ctrl+C did not exit watcher")
	}
	if !bytes.Equal(before, observationState(t, c)) {
		t.Fatal("Ctrl+C changed worker or authority")
	}
	t.Log("O2: actual Ctrl+C exited CLI watcher; worker RUNNING, Pending/revisions/slot/ledger unchanged")
	// Observation errors stay local, even though their ordinary controller error
	// classes would otherwise trigger a durable blocker.
	badConfig := *c.Config
	badConfig.WSL.Root = c.Runner.Root() + "/missing"
	bad := *c
	bad.Config = &badConfig
	bad.Runner.Config = &badConfig
	bad.Runner.Protected = true
	if _, e = bad.DiffAttempt(context.Background(), id); model.Code(e) != "BLOCKED" {
		t.Fatal("read failure not isolated", e)
	}
	badConfig = *c.Config
	badConfig.Limits.Stdout = 1
	bad.Runner = c.Runner
	bad.Runner.Config = &badConfig
	if _, e = bad.DiffAttempt(context.Background(), id); model.Code(e) != "LIMIT_EXCEEDED" {
		t.Fatal("output bound not enforced", e)
	}
	if !bytes.Equal(before, observationState(t, c)) {
		t.Fatal("observation failure changed authority")
	}
	t.Log("O8: runner read failure and output bound failure left complete state/ledger and worker unchanged")
	// Test harness, not either observation command, lets the delayed worker finish.
	r, e := c.Runner.Control(context.Background(), []string{"touch", c.Runner.Root() + "/test-finish"}, nil, nil, 4096)
	if e != nil || r.ExitCode != 0 {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		joined = true
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not finish")
	}
	select {
	case e = <-w1:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not end")
	}
	if !strings.Contains(first.text(), "[stdout] B\n") || !strings.Contains(first.text(), "RETURNED") {
		t.Fatal(first.text())
	}
	var replay bytes.Buffer
	if e = c.WatchAttempt(context.Background(), id, &replay); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(replay.String(), "[stdout] A\n[stdout] B\n") || !strings.Contains(replay.String(), "[stderr] err A\n[stderr] err B\n") {
		t.Fatal(replay.String())
	}
	if _, e = c.DiffAttempt(context.Background(), id); model.Code(e) != "INVALID_STATE" {
		t.Fatal("terminal diff", e)
	}
	t.Log("O3: after watcher Ctrl+C, worker emitted B, RETURNED, and terminal watch replayed both durable streams")
}

func TestObservationInterruptedLogs(t *testing.T) {
	c, _, _, _ := observationFixture(t, observationWorker)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, e := scheduler.New(c).Step(ctx); done <- e }()
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	id := ""
	waitObservation(t, func() bool {
		s, e := c.Store.Read()
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range s.Attempts {
			b, _ := os.ReadFile(a.Stdout)
			if strings.Contains(string(b), "A\n") {
				id = a.ID
				return true
			}
		}
		return false
	})
	_, e := c.Interrupt(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	e = <-done
	joined = true
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = c.WatchAttempt(context.Background(), id, &out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "INTERRUPTED") || !strings.Contains(out.String(), "[stdout] A\n") || !strings.Contains(out.String(), "[stderr] err A\n") {
		t.Fatal(out.String())
	}
	t.Log("O3: INTERRUPTED retains live stdout/stderr")
}

func TestObservationContinuationBase(t *testing.T) {
	c, _, _, _ := observationFixture(t, `printf 'first\n' > modify.txt; printf 'kept\n' > kept.txt; printf '%s' '{"schema_version":0,"operation":"mutation","disposition":"CONTINUE_FINAL","claims":[],"new_options":[]}' > "$SCP_RESULT"`)
	engine := scheduler.New(c)
	if _, e := engine.Step(context.Background()); e != nil {
		t.Fatal(e)
	}
	c.Config.Workers[0].Command = []string{"sh", "-c", `printf 'second\n' > modify.txt; printf 'ready\n'; sleep 50`}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() { _, e := engine.Step(ctx); done <- e }()
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	id := ""
	waitObservation(t, func() bool {
		s, e := c.Store.Read()
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range s.Attempts {
			b, _ := os.ReadFile(a.Stdout)
			if a.Active() && string(b) == "ready\n" {
				if a.TargetType != "ARTIFACT" {
					t.Fatal("not continuation")
				}
				id = a.ID
				return true
			}
		}
		return false
	})
	diff, e := c.DiffAttempt(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(diff.Text, "M modify.txt\n") || !strings.Contains(diff.Text, "-first\n+second\n") || strings.Contains(diff.Text, "kept.txt") || strings.Contains(diff.Text, "-base\n") {
		t.Fatal("diff used repository base instead of Attempt input", diff.Text)
	}
	if _, e = c.Interrupt(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	e = <-done
	joined = true
	if e != nil {
		t.Fatal(e)
	}
	t.Log("continuation diff uses immutable input Artifact, excluding prior Attempt changes")
}

func TestObservationReadonlyRejection(t *testing.T) {
	c, _, _, task := observationFixture(t, observationWorker)
	for _, operation := range []string{"discussion", "review", "option_generation", "merge_judge", "merge_synth"} {
		id := model.ID()
		// Terminal records allow all readonly operation types without fabricating
		// a runnable chain or creating any temporary workspace.
		e := c.Store.Update(func(s *model.State) error {
			s.Attempts[id] = &model.Attempt{ID: id, TaskID: task, Operation: operation, Status: "RETURNED", TargetType: "TASK", TargetID: task, Profile: c.Config.Profile(operation).ID, Actor: c.Config.Profile(operation).Card, AnchorType: "TASK", AnchorID: task, SHA: s.Tasks[task].SHA, Started: model.Now()}
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		before := observationState(t, c)
		_, e = c.DiffAttempt(context.Background(), id)
		if model.Code(e) != "INVALID_STATE" || !strings.Contains(fmt.Sprint(e), "Attempt has no writable workspace") {
			t.Fatal(operation, e)
		}
		if !bytes.Equal(before, observationState(t, c)) {
			t.Fatal("readonly rejection wrote state")
		}
	}
}

func TestObservationConcurrentCapture(t *testing.T) {
	script := `exec python3 -u -c 'import os,time
f=open("changing.bin","wb",buffering=0)
f.write(b"x"*1048576)
print("A",flush=True)
end=time.monotonic()+50
while time.monotonic()<end:
 f.seek(0); f.write(b"x")
'`
	c, _, _, _ := observationFixture(t, script)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() { _, e := scheduler.New(c).Step(ctx); done <- e }()
	t.Cleanup(func() {
		cancel()
		if !joined {
			<-done
		}
	})
	id := ""
	waitObservation(t, func() bool {
		s, e := c.Store.Read()
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range s.Attempts {
			b, _ := os.ReadFile(a.Stdout)
			if string(b) == "A\n" {
				id = a.ID
				return true
			}
		}
		return false
	})
	before := observationState(t, c)
	_, e := c.DiffAttempt(context.Background(), id)
	if model.Code(e) != "BLOCKED" || !strings.Contains(e.Error(), "changed during observation") {
		t.Fatal("concurrent write not rejected", e)
	}
	if !bytes.Equal(before, observationState(t, c)) {
		t.Fatal("unstable snapshot changed authority/worker")
	}
	t.Log("O8: actively rewriting worker caused BLOCKED retry; worker RUNNING, complete state/ledger unchanged")
	if _, e = c.Interrupt(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	joined = true
}
