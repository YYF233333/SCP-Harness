//go:build windows && release && codex_integration

package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scp-harness/internal/model"
)

// Explicit opt-in: incurs real model calls, uses the installed opaque executable,
// and runs through Core materialization, termination, capture and result parsing.
func TestCodexExecutionBoundary(t *testing.T) {
	c, repo := integrationCore(t, "success-worker")
	for i := range c.Config.Workers {
		c.Config.Workers[i].Command = []string{"/opt/scp-workers/codex-worker"}
		c.Config.Workers[i].Timeout = 600000
	}
	c.Config.Exploration.N = 2
	c.Config.Test.Command = []string{"python3", "-B", "-m", "unittest", "-v"}
	probe, e := os.ReadFile("../../testdata/workers/execution-boundary.py")
	if e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string]string{
		"greeting.py":       "def greet(name):\n    return 'TODO'\n",
		"test_greeting.py":  "import unittest\nfrom greeting import greet\nclass GreetingTest(unittest.TestCase):\n    def test_named(self): self.assertEqual(greet(' Ada '), 'Hello, Ada!')\n    def test_blank(self): self.assertEqual(greet('  '), 'Hello, world!')\n",
		"boundary_probe.py": string(probe),
		"AGENTS.md":         "Keep tests and boundary_probe.py unchanged. Use Python -B and TMPDIR for build output.\n",
	} {
		if e = os.WriteFile(filepath.Join(repo, name), []byte(data), 0644); e != nil {
			t.Fatal(e)
		}
	}
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@local", "commit", "-m", "boundary fixture")
	objective := `CONTEXT-boundary-v1. Implement greet(name) by stripping whitespace and returning 'Hello, <name>!' or 'Hello, world!' for blank input.
For option_generation, propose exactly one direction: direct whitespace stripping with a fallback name. For every option_generation, merge_judge and merge_synth operation, inspect the readonly source and run python3 -B boundary_probe.py, verifying SCP_READONLY_OK before your answer.
For mutation, implement greeting.py, run python3 -B -m unittest -v, compile greeting.py with py_compile output under TMPDIR, and run python3 -B boundary_probe.py. Keep the fixture tests and probe unchanged; submit PROMOTE_FINAL when they pass.
For discussion, read the human question and source, run python3 -B boundary_probe.py and verify SCP_READONLY_OK, then answer with reasons, risks and recommendation.
For review, read every candidate file and CI evidence under context/ci.result/ including result.json, stdout.log and stderr.log, run python3 -B -m unittest -v, and run python3 -B boundary_probe.py. The probe is an authorized OS boundary test: it must actively attempt opening input/context/candidate paths for write and observe EACCES. It is not a request to implement changes. Verify its SCP_READONLY_OK output before giving a verdict.`
	task, e := c.CreateTask(context.Background(), objective, repo, "refs/heads/main", c.Operator().ID, 8400000)
	if e != nil {
		t.Fatal(e)
	}
	engine := New(c)
	// Two fresh generations, a semantic partition, and its synthesis.
	for _, expected := range []string{"option_generation", "option_generation", "merge_judge", "merge_synth"} {
		step(t, engine)
		checkCodexAttempt(t, engine, expected, "SCP_READONLY_OK {")
	}
	option, e := c.Propose(task.ID, "Implement the direct greeting function and the requested boundary evidence.", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(option.ID, 2400000); e != nil {
		t.Fatal(e)
	}
	assertIdleRun(t, engine)
	if fixtureGit(t, repo, "rev-parse", "HEAD") != task.SHA {
		t.Fatal("H1 authoritative repo changed")
	}
	t.Log("H1 PASS: funded Option stayed idle with real scheduler")
	if _, e = c.DiscussOption(option.ID, "Why direct whitespace stripping? Read source and run the readonly boundary probe; summarize risks and your recommendation without changing files."); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	checkCodexAttempt(t, engine, "discussion", "SCP_READONLY_OK {")
	thread, e := c.OptionThread(option.ID)
	if e != nil || len(thread.Messages) != 2 || thread.Messages[1].Type != "discussion.reply" {
		t.Fatal("real Codex discussion reply", e)
	}
	if s := state(t, c); len(s.Artifacts) != 0 || len(s.Pending) != 0 || fixtureGit(t, repo, "rev-parse", "HEAD") != task.SHA {
		t.Fatal("H2 discussion changed authority")
	}
	t.Log("H2 PASS: real Codex discussion reply; readonly OS boundary; no Artifact; no release")
	if _, e = c.ReleaseOption(option.ID); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	checkCodexAttempt(t, engine, "mutation", "")
	s := state(t, c)
	pending := nextChangeStep(s, task.ID)
	if pending == nil || pending.Operation != "ci" {
		t.Fatal("mutation did not produce a candidate")
	}
	candidate := s.Artifacts[pending.TargetID]
	var boundary struct {
		Network int    `json:"network_http"`
		Mode    string `json:"mode"`
		Errno   string `json:"errno"`
	}
	if e = json.Unmarshal([]byte(artifactText(t, candidate, "boundary.json")), &boundary); e != nil {
		t.Fatal(e)
	}
	if boundary.Network != 200 || boundary.Mode != "writable" || boundary.Errno != "EACCES" {
		t.Fatalf("mutation boundary: %+v", boundary)
	}
	if artifactText(t, candidate, "boundary_probe.py") != string(probe) {
		t.Fatal("model modified boundary probe")
	}
	blob, e := os.ReadFile(candidate.BlobPath)
	if e != nil {
		t.Fatal(e)
	}
	step(t, engine) // Real protected tests in SCP-Test.
	if state(t, c).LatestCI(candidate.ID).Status != "PASS" {
		t.Fatal("protected test failed")
	}
	step(t, engine) // Independent model in root-owned readonly candidate.
	checkCodexAttempt(t, engine, "review", "SCP_READONLY_OK {")
	s = state(t, c)
	if s.Reviews[candidate.ID] == nil || s.Reviews[candidate.ID].Verdict != "APPROVE" {
		t.Fatal("real review did not approve", s.Reviews[candidate.ID])
	}
	if len(s.Artifacts) != 1 {
		t.Fatal("review entered mutation capture")
	}
	after, e := os.ReadFile(candidate.BlobPath)
	if e != nil || string(blob) != string(after) {
		t.Fatal("candidate Artifact changed", e)
	}
	authorizePromotion(t, c, task.ID)
	step(t, engine) // Explicitly authorized production CAS.
	firstSHA := state(t, c).Tasks[task.ID].SHA
	if firstSHA == task.SHA {
		t.Fatal("H3 first promotion missing")
	}
	assertIdleRun(t, engine)
	t.Log("H3 PASS: explicit host release started the real mutation chain")
	if _, e = c.ReleaseOption(option.ID); e != nil {
		t.Fatal(e)
	}
	step(t, engine)
	checkCodexAttempt(t, engine, "mutation", "")
	if p := nextChangeStep(state(t, c), task.ID); p == nil || p.Operation != "ci" {
		t.Fatal("second cycle did not submit")
	}
	step(t, engine)
	step(t, engine)
	checkCodexAttempt(t, engine, "review", "SCP_READONLY_OK {")
	if p := nextChangeStep(state(t, c), task.ID); p == nil || p.Operation != "await_promotion" {
		t.Fatal("second cycle not approved")
	}
	authorizePromotion(t, c, task.ID)
	step(t, engine)
	if s := state(t, c); s.Tasks[task.ID].SHA == firstSHA || s.Options[option.ID].Status != "OPEN" || s.Options[option.ID].Remaining <= 0 {
		t.Fatal("second promotion")
	}
	assertIdleRun(t, engine)
	t.Log("H4 PASS: one release per chain; two real promotions; no automatic second cycle")
}

func checkCodexAttempt(t *testing.T, engine *Scheduler, operation, evidence string) {
	t.Helper()
	var latest *model.Attempt
	for _, attempt := range state(t, engine.Core).Attempts {
		if latest == nil || attempt.Started > latest.Started {
			latest = attempt
		}
	}
	if latest == nil || latest.Operation != operation || latest.Status != "RETURNED" {
		t.Fatalf("expected %s RETURNED: %+v", operation, latest)
	}
	log, e := os.ReadFile(latest.Stderr)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(log), evidence) {
		t.Fatalf("%s missing evidence %q: %s", operation, evidence, log)
	}
	if !strings.Contains(string(log), "danger-full-access") {
		t.Fatalf("%s did not report full access: %s", operation, log)
	}
	if !strings.Contains(string(log), "approval: never") {
		t.Fatalf("%s did not disable approvals: %s", operation, log)
	}
	if strings.Contains(string(log), "bwrap:") {
		t.Fatal("provider sandbox used")
	}
	if dir := os.Getenv("SCP_CODEX_EVIDENCE"); dir != "" {
		if e = os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, operation+"-"+latest.ID+".log"), log, 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("%s %s: RETURNED; full access; no approval; %s", operation, latest.ID, evidence)
}
