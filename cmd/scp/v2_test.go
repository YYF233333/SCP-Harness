package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
)

func TestV2HelpAndValidationBeforeStorage(t *testing.T) {
	t.Setenv("SCP_CONFIG", filepath.Join(t.TempDir(), "missing", "config.json"))
	for _, args := range [][]string{{"--help"}, {"help"}, {"task", "--help"}, {"option", "--help"}, {"change", "--help"}, {"ci", "--help"}, {"attempt", "--help"}, {"change", "promote", "--help"}, {"ci", "run", "--help"}} {
		var out, errs bytes.Buffer
		if code := run(args, &out, &errs); code != 0 || !strings.Contains(out.String(), "authority") {
			t.Fatalf("help %v: %d %s", args, code, &errs)
		}
	}
	for _, args := range [][]string{{"status", "--bad"}, {"option", "allocate", "abc", "--wall-ms", "no"}, {"option", "allocate", "abc", "--wall-ms", "-1"}, {"change", "list", "--state", "UNKNOWN"}, {"ci", "run"}, {"task", "revise", "abc", "--objective", ""}, {"change", "watch", "abc", "--bad"}} {
		var out, errs bytes.Buffer
		if code := run(append([]string{"--json"}, args...), &out, &errs); code != 2 || !strings.Contains(out.String(), "USAGE_ERROR") || strings.Contains(out.String(), "STORAGE_FAILURE") {
			t.Fatalf("validation %v: %d %s %s", args, code, &out, &errs)
		}
	}
}

func TestV2ShortIDsAndConfigShow(t *testing.T) {
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cfg.Database = filepath.Join(dir, "state.db")
	cfg.Artifacts = filepath.Join(dir, "artifacts")
	for id, path := range cfg.RoleCards {
		cfg.RoleCards[id] = filepath.Join(root, path)
	}
	path := filepath.Join(dir, "config.json")
	write := func() {
		data, _ := json.Marshal(cfg)
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	cfg, e = config.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	c, e := core.Open(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Store.Close()
	id := "abcdef11111111111111111111111111"
	if e = c.Store.Update(func(s *model.State) error {
		s.Tasks[id] = &model.Task{ID: id, Objective: "ids", Status: "ACTIVE", SHA: strings.Repeat("a", 40), Resources: model.Resources{Minted: 1500000}}
		s.Accounts[id] = &model.Account{TaskID: id, Remaining: 1500000}
		s.RepositoryIdentities[id] = filepath.Join(dir, "repo")
		s.Exploration[id] = &model.Exploration{Done: true}
		s.SchedulerConfigHash = cfg.Hash()
		s.SchedulerDiskHash = cfg.DiskHash
		s.SchedulerStarted = model.Now()
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ReviseTask(id, "revised"); e != nil {
		t.Fatal(e)
	}
	call := func(expected int, args ...string) string {
		t.Helper()
		var out, errs bytes.Buffer
		if code := run(append([]string{"--config", path, "--json"}, args...), &out, &errs); code != expected {
			t.Fatalf("CLI %v: %d %s %s", args, code, &out, &errs)
		}
		return out.String()
	}
	if !strings.Contains(call(0, "task", "show", "abcdef1"), "revised") {
		t.Fatal("short ID did not resolve")
	}
	if !strings.Contains(call(3, "task", "show", "absent"), "NOT_FOUND") {
		t.Fatal("missing prefix")
	}
	if e = c.Store.Update(func(s *model.State) error {
		second := "abcdef22222222222222222222222222"
		s.Tasks[second] = &model.Task{ID: second, Objective: "other", Status: "SUSPENDED", Resources: model.Resources{Minted: 1}}
		s.Accounts[second] = &model.Account{TaskID: second, Remaining: 1}
		s.Exploration[second] = &model.Exploration{Done: true}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(call(3, "task", "show", "abcdef"), "AMBIGUOUS_ID") {
		t.Fatal("ambiguous prefix selected a Task")
	}
	unchanged := call(0, "config", "show")
	var configView map[string]any
	if e = json.Unmarshal([]byte(unchanged), &configView); e != nil {
		t.Fatal(e)
	}
	hashes := configView["data"].(map[string]any)
	if hashes["disk_config_hash"] != hashes["scheduler_effective_config_hash"] {
		t.Fatal("unchanged configuration hashes cannot be compared", hashes)
	}
	if !strings.Contains(unchanged, `"restart_required":false`) {
		t.Fatal("unchanged config mismatch")
	}
	cfg.Test.Timeout /= 2
	write()
	if !strings.Contains(call(0, "config", "show"), `"restart_required":true`) {
		t.Fatal("disk/effective mismatch not visible")
	}
	if _, e = execute(context.Background(), c, "change.list", nil); e != nil {
		t.Fatal(e)
	}
}
