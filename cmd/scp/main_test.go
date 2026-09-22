package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/gitrepo"
	"scp-harness/internal/model"
)

func keys(t *testing.T, v any, expected string) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not object: %#v", v)
	}
	got := []string{}
	for k := range m {
		got = append(got, k)
	}
	want := strings.Fields(expected)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("projection keys %v != %v", got, want)
	}
}

func TestVersionAndGlobalConfig(t *testing.T) {
	t.Setenv("SCP_CONFIG", filepath.Join(t.TempDir(), "missing.json"))
	var out, errs bytes.Buffer
	if code := run([]string{"--version"}, &out, &errs); code != 0 || out.String() != "SCP Harness v"+strings.TrimSpace(version)+"\n" {
		t.Fatalf("version requires no config: %d %s %s", code, &out, &errs)
	}
	out.Reset()
	if code := run([]string{"--json", "--version"}, &out, &errs); code != 0 {
		t.Fatal(code, &errs)
	}
	var envelope struct {
		OK      bool              `json:"ok"`
		Command string            `json:"command"`
		Data    map[string]string `json:"data"`
	}
	if e := json.Unmarshal(out.Bytes(), &envelope); e != nil || !envelope.OK || envelope.Command != "version" || envelope.Data["version"] != strings.TrimSpace(version) {
		t.Fatal("version envelope", envelope, e)
	}
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cfg.Database, cfg.Artifacts = filepath.Join(dir, "state.db"), filepath.Join(dir, "artifacts")
	for id, path := range cfg.RoleCards {
		cfg.RoleCards[id] = filepath.Join(root, path)
	}
	data, e := json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "scp.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SCP_CONFIG", path)
	t.Chdir(t.TempDir())
	out.Reset()
	if code := run([]string{"init"}, &out, &errs); code != 0 {
		t.Fatal("global config not loaded from unrelated directory", code, &errs)
	}
	if _, e = os.Stat(cfg.Database); e != nil {
		t.Fatal(e)
	}
	if code := run([]string{"--config", "missing.json", "status"}, &out, &errs); code != 2 {
		t.Fatal("explicit config must override global config", code)
	}
}

func TestCLIJSONFrozenProjectionsAndRestart(t *testing.T) {
	root, e := filepath.Abs(filepath.Join("..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(filepath.Join(root, "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cfg.Database = filepath.Join(dir, "core.db")
	cfg.Artifacts = filepath.Join(dir, "artifacts")
	for id, p := range cfg.RoleCards {
		cfg.RoleCards[id] = filepath.Join(root, p)
	}
	path := filepath.Join(dir, "scp.json")
	b, _ := json.Marshal(cfg)
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	call := func(expectedExit int, args ...string) map[string]any {
		t.Helper()
		var out, errs bytes.Buffer
		code := run(append([]string{"--config", path, "--json"}, args...), &out, &errs)
		if code != expectedExit {
			t.Fatalf("%v exit %d: %s %s", args, code, out.String(), errs.String())
		}
		var envelope map[string]any
		d := json.NewDecoder(&out)
		if e = d.Decode(&envelope); e != nil {
			t.Fatal(e)
		}
		if strings.TrimSpace(out.String()) != "" {
			t.Fatal("multiple stdout objects/logs")
		}
		if expectedExit == 0 {
			keys(t, envelope, "ok command data")
			return envelope["data"].(map[string]any)
		}
		keys(t, envelope, "ok command error")
		keys(t, envelope["error"], "code message")
		return envelope["error"].(map[string]any)
	}
	keys(t, call(0, "init"), "schema_version database artifact_store")
	repo := filepath.Join(dir, "repo")
	if e = os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) {
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
	git("-c", "user.name=fixture", "-c", "user.email=fixture@local", "commit", "-m", "base")
	taskKeys := "id objective repo_path repo_ref responsible_actor_id status current_authoritative_sha state_revision resources created_at updated_at"
	optionKeys := "id task_id text status resource_parent_id remaining_wall_ms created_against_repo_sha created_against_state_revision created_by_actor created_at"
	task := call(0, "task", "create", "--objective", "golden", "--repo", repo, "--ref", "refs/heads/main", "--responsible-actor", cfg.Operator, "--wall-ms", "10000")
	keys(t, task, taskKeys)
	keys(t, task["resources"], "total_minted_wall_ms remaining_wall_ms outstanding_lease_wall_ms total_charged_wall_ms retired_wall_ms")
	taskID := task["id"].(string)
	subdir := filepath.Join(repo, "nested")
	if e = os.Mkdir(subdir, 0700); e != nil {
		t.Fatal(e)
	}
	if value := call(3, "task", "create", "--objective", "duplicate", "--repo", subdir, "--ref", "refs/heads/main", "--responsible-actor", cfg.Operator, "--wall-ms", "100"); value["code"] != "PRECONDITION_FAILED" {
		t.Fatal("repository alias bypassed active binding")
	}
	keys(t, call(0, "task", "show", taskID), taskKeys)
	keys(t, call(0, "task", "extend", taskID, "--wall-ms", "1"), taskKeys)
	option := call(0, "option", "propose", "--task", taskID, "--text", "parent")
	keys(t, option, optionKeys)
	id := option["id"].(string)
	keys(t, call(0, "option", "allocate", id, "--wall-ms", "5000"), optionKeys)
	keys(t, call(0, "option", "show", id), optionKeys)
	zero := call(0, "option", "refine", id, "--text", "zero child")
	keys(t, zero, optionKeys)
	if zero["remaining_wall_ms"] != float64(0) || call(0, "option", "show", id)["remaining_wall_ms"] != float64(5000) {
		t.Fatal("default refine transferred resources")
	}
	claimKeys := "id task_id subject_type subject_id claim_type payload_json issuer_actor_id created_against_repo_sha created_against_state_revision created_at"
	keys(t, call(0, "option", "comment", id, "--text", "why?"), claimKeys)
	thread := call(0, "option", "thread", id)
	keys(t, thread, "option messages")
	keys(t, thread["option"], optionKeys)
	keys(t, thread["messages"].([]any)[0], claimKeys)
	var human, humanErr bytes.Buffer
	if run([]string{"--config", path, "option", "thread", id}, &human, &humanErr) != 0 || !strings.Contains(human.String(), "] O5-1:\nwhy?\n") {
		t.Fatal("human thread", human.String(), humanErr.String())
	}
	if value := call(3, "option", "release", id); value["code"] != "INVALID_STATE" {
		t.Fatal(value)
	}
	if value := call(2, "option", "discuss", id); value["code"] != "USAGE_ERROR" {
		t.Fatal(value)
	}
	refined := call(0, "option", "refine", id, "--text", "child", "--transfer-wall-ms", "100")
	keys(t, refined, optionKeys)
	spec := filepath.Join(dir, "split.json")
	if e = os.WriteFile(spec, []byte(`{"children":[{"text":"a","wall_ms":100},{"text":"b","wall_ms":200}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	split := call(0, "option", "split", id, "--spec", spec)
	keys(t, split, "parent children")
	keys(t, split["parent"], optionKeys)
	children := split["children"].([]any)
	for _, o := range children {
		keys(t, o, optionKeys)
	}
	merge := core.MergeSpec{Text: "joined", Participants: []core.Participant{{ID: children[0].(map[string]any)["id"].(string), Wall: 50}, {ID: children[1].(map[string]any)["id"].(string), Wall: 100}}}
	data, _ := json.Marshal(merge)
	if e = os.WriteFile(spec, data, 0600); e != nil {
		t.Fatal(e)
	}
	keys(t, call(0, "option", "merge", "--task", taskID, "--spec", spec), optionKeys)
	claim := call(0, "claim", "create", "--task", taskID, "--subject-type", "OPTION", "--subject-id", id, "--type", "resource.propose")
	keys(t, claim, "id task_id subject_type subject_id claim_type payload_json issuer_actor_id created_against_repo_sha created_against_state_revision created_at")
	c, e := core.Open(cfg, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Store.Update(func(s *model.State) error { s.Exploration[taskID].Done = true; return nil }); e != nil {
		t.Fatal(e)
	}
	keys(t, call(0, "option", "release", id), optionKeys)
	if value := call(4, "option", "discuss", id, "--text", "blocked"); value["code"] != "BLOCKED" {
		t.Fatal(value)
	}
	// End this queued chain explicitly, then queue one bounded discussion.
	p := model.Step{TaskID: taskID, OptionID: id, Operation: "mutation", TargetType: "OPTION", TargetID: id}
	if e = c.EndPromotion(p, "", "test chain ended"); e != nil {
		t.Fatal(e)
	}
	discussion := call(0, "option", "discuss", id, "--text", "explain")
	keys(t, discussion, "comment queued")
	keys(t, discussion["comment"], claimKeys)
	if discussion["queued"] != true {
		t.Fatal("queued must be true")
	}
	if value := call(4, "option", "release", id); value["code"] != "BLOCKED" {
		t.Fatal(value)
	}
	if e = c.EndPromotion(p, "", "test discussion ended"); e != nil {
		t.Fatal(e)
	}
	attemptID, artifactID := model.ID(), model.ID()
	blob := make([]byte, 1024)
	blobPath := filepath.Join(dir, "blob.tar")
	if e = os.WriteFile(blobPath, blob, 0600); e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(blob)
	e = c.Store.Update(func(s *model.State) error {
		now := model.Now()
		s.Attempts[attemptID] = &model.Attempt{ID: attemptID, TaskID: taskID, Operation: "mutation", TargetType: "OPTION", TargetID: id, Actor: cfg.Operator, Profile: "operator", AnchorType: "OPTION", AnchorID: id, Lease: 1, SHA: s.Tasks[taskID].SHA, Started: now, Status: "RETURNED", Ended: &now}
		s.Artifacts[artifactID] = &model.Artifact{ID: artifactID, TaskID: taskID, Anchor: id, AttemptID: attemptID, BlobPath: blobPath, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(blob)), BaseSHA: s.Tasks[taskID].SHA, Created: now}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Block("WORKER_UNAVAILABLE", taskID, "operator", "", "fixture diagnostic"); e != nil {
		t.Fatal(e)
	}
	s, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	blockerID := ""
	for id := range s.Blockers {
		blockerID = id
	}
	c.Store.Close()
	keys(t, call(0, "attempt", "show", attemptID), "id task_id operation target_type target_id actor_instance worker_profile resource_anchor_type resource_anchor_id lease_wall_ms created_against_repo_sha created_against_state_revision started_at ended_at status exit_code termination_reason stdout_path stderr_path produced_artifact_id")
	keys(t, call(0, "artifact", "show", artifactID), "id task_id semantic_anchor_option_id source_attempt_id blob_path sha256 size_bytes base_repo_sha created_at")
	keys(t, call(0, "artifact", "export", artifactID, "--out", filepath.Join(dir, "export.tar")), "artifact_id out sha256 size_bytes")
	for _, name := range []string{"option", "attempt", "artifact", "claim"} {
		keys(t, call(0, name, "list", "--task", taskID), "items")
	}
	keys(t, call(0, "blocker", "list", "--unresolved"), "items")
	keys(t, call(0, "blocker", "resolve", blockerID), "id kind scope subject_id message created_at resolved_at")
	keys(t, call(0, "status"), "fail_stop execution_slot tasks running_attempt unresolved_blockers")
	keys(t, call(0, "option", "close", id), optionKeys)
	for _, action := range []string{"suspend", "resume", "close"} {
		keys(t, call(0, "task", action, taskID), taskKeys)
	}
	c, e = core.Open(cfg, false)
	if e != nil {
		t.Fatal(e)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	stopped, e := execute(canceled, c, "run", nil)
	c.Store.Close()
	if e != nil {
		t.Fatal(e)
	}
	valueBytes, _ := json.Marshal(stopped)
	var stopValue map[string]any
	json.Unmarshal(valueBytes, &stopValue)
	keys(t, stopValue, "stop_reason")
	if stopValue["stop_reason"] != "SIGNAL" {
		t.Fatal("run stop projection")
	}
	if value := call(3, "task", "resume", taskID); value["code"] != "INVALID_STATE" {
		t.Fatal(value)
	}
	if value := call(3, "option", "show", "missing"); value["code"] != "NOT_FOUND" {
		t.Fatal(value)
	}
	if value := call(2, "status", "--unknown"); value["code"] != "USAGE_ERROR" {
		t.Fatal(value)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"--config", filepath.Join(dir, "missing.json"), "--json", "init"}, &out, &errs); code != 2 {
		t.Fatal("init silently manufactured config")
	}
}
func TestStableErrorExitCodes(t *testing.T) {
	groups := map[int][]string{1: {"INTERNAL_ERROR"}, 2: {"USAGE_ERROR", "INVALID_CONFIG", "INVALID_JSON", "SCHEMA_INVALID"}, 3: {"NOT_FOUND", "CAPABILITY_DENIED", "PRECONDITION_FAILED", "INVALID_STATE", "INSUFFICIENT_RESOURCE", "LIMIT_EXCEEDED", "PRECONDITION_CHANGED"}, 4: {"WORKER_UNAVAILABLE", "RUNNER_UNAVAILABLE", "REPOSITORY_UNAVAILABLE", "BLOCKED"}, 5: {"STORAGE_FAILURE", "CORE_INCONSISTENT"}}
	for exit, codes := range groups {
		for _, code := range codes {
			if model.Exit(code) != exit {
				t.Errorf("%s exit mapping", code)
			}
		}
	}
}
