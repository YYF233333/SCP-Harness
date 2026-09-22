package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
)

func TestObservationCLIGolden(t *testing.T) {
	root, _ := filepath.Abs("../..")
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cfg.Database, cfg.Artifacts = filepath.Join(dir, "state.db"), filepath.Join(dir, "artifacts")
	for id, path := range cfg.RoleCards {
		cfg.RoleCards[id] = filepath.Join(root, path)
	}
	b, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "scp.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := core.Open(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Store.Close()
	stdout, stderr := filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log")
	if e = os.WriteFile(stdout, []byte("A\nB\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(stderr, []byte("error\n"), 0600); e != nil {
		t.Fatal(e)
	}
	e = c.Store.Update(func(s *model.State) error {
		s.Tasks["task"] = &model.Task{ID: "task", Status: "ACTIVE", RepoPath: dir, RepoRef: "refs/heads/main", SHA: strings.Repeat("a", 40), Resources: model.Resources{Minted: 1}}
		s.RepositoryIdentities["task"] = dir
		s.Accounts["task"] = &model.Account{TaskID: "task", Remaining: 1}
		s.Exploration["task"] = &model.Exploration{Done: true}
		s.Attempts["attempt"] = &model.Attempt{ID: "attempt", TaskID: "task", Operation: "discussion", Status: "RETURNED", AnchorID: "task", TargetType: "TASK", TargetID: "task", Stdout: stdout, Stderr: stderr}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	before, e := c.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	beforeBytes, _ := json.Marshal(before)
	var out, errout bytes.Buffer
	if exit := run([]string{"--config", path, "attempt", "watch", "attempt"}, &out, &errout); exit != 0 || out.String() != "[stdout] A\n[stdout] B\n[stderr] error\nAttempt attempt RETURNED\n" {
		t.Fatalf("watch golden: %d %s %s", exit, out.String(), errout.String())
	}
	for _, tc := range []struct {
		args []string
		exit int
		code string
	}{
		{[]string{"attempt", "watch", "attempt"}, 2, "USAGE_ERROR"},
		{[]string{"attempt", "diff", "attempt"}, 3, "INVALID_STATE"},
		{[]string{"attempt", "diff", "missing"}, 3, "NOT_FOUND"},
		{[]string{"attempt", "diff"}, 2, "USAGE_ERROR"},
		{[]string{"attempt", "diff", "attempt", "--follow"}, 2, "USAGE_ERROR"},
	} {
		out.Reset()
		errout.Reset()
		exit := run(append([]string{"--config", path, "--json"}, tc.args...), &out, &errout)
		var envelope map[string]any
		if exit != tc.exit || json.Unmarshal(out.Bytes(), &envelope) != nil {
			t.Fatal(tc.args, exit, out.String(), errout.String())
		}
		keys(t, envelope, "ok command error")
		keys(t, envelope["error"], "code message")
		if envelope["error"].(map[string]any)["code"] != tc.code {
			t.Fatal(envelope)
		}
	}
	after, e := c.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	afterBytes, _ := json.Marshal(after)
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("CLI observation wrote state")
	}
	// A broken authority read must not use the controller's emergency blocker.
	if _, e = c.Store.DB.Exec(`UPDATE core_state SET body='"broken"' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	errout.Reset()
	if exit := run([]string{"--config", path, "--json", "attempt", "diff", "attempt"}, &out, &errout); exit != 5 {
		t.Fatal(exit, out.String())
	}
	var blockers int
	if e = c.Store.DB.QueryRow("SELECT count(*) FROM runtime_blockers").Scan(&blockers); e != nil || blockers != 0 {
		t.Fatal("observation failure wrote blocker", e, blockers)
	}
}
