package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

// This SQLite fixture isolates Claim effects from process metering. Production
// CreateClaim/Complete, transaction validation and ledger settlement are used.
func claimFixture(t *testing.T, caps ...string) (*Core, string, string) {
	t.Helper()
	cfg, e := config.Load(filepath.Join("..", "..", "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	cfg.Database = filepath.Join(dir, "state.db")
	cfg.Artifacts = filepath.Join(dir, "artifacts")
	card := cfg.Cards[cfg.Operator]
	card.Capabilities = nil
	for _, name := range caps {
		scope := ""
		if name == "repository.read" {
			scope = "task.repository"
		}
		if name == "sandbox.write" || name == "process.execute" {
			scope = "lease.sandbox"
		}
		card.Capabilities = append(card.Capabilities, config.Capability{Name: name, Scope: scope})
	}
	cfg.Cards[cfg.Operator] = card
	cfg.Workers[0].Card = cfg.Operator
	c, e := Open(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Store.Close() })
	taskID, optionID := model.ID(), model.ID()
	e = c.Store.Update(func(s *model.State) error {
		now := model.Now()
		s.Tasks[taskID] = &model.Task{ID: taskID, Objective: "Claim boundary", RepoPath: filepath.Join(dir, "repo"), RepoRef: "refs/heads/main", Responsible: card.ID, Status: "ACTIVE", SHA: strings.Repeat("a", 40), Resources: model.Resources{Minted: 10000}, Created: now, Updated: now}
		s.RepositoryIdentities[taskID] = filepath.Join(dir, "repo", ".git")
		s.Accounts[taskID] = &model.Account{TaskID: taskID, Remaining: 6000}
		s.Options[optionID] = &model.Option{ID: optionID, TaskID: taskID, Text: "route", Status: "OPEN", Parent: taskID, SHA: strings.Repeat("a", 40), Created: now, Actor: card.ID}
		s.Accounts[optionID] = &model.Account{TaskID: taskID, Parent: taskID, Remaining: 4000}
		s.Exploration[taskID] = &model.Exploration{Done: true}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return c, taskID, optionID
}
func ledgerView(s *model.State) []byte {
	totals := map[string]model.Resources{}
	for id, t := range s.Tasks {
		totals[id] = t.Resources
	}
	b, _ := json.Marshal([]any{s.Accounts, s.Leases, totals})
	return b
}
func TestResourceProposalExactAuthorizationAndNoLedgerEffect(t *testing.T) {
	cases := []struct {
		name, typ string
		caps      []string
		denied    bool
	}{
		{"both", "resource.propose", []string{"claim.publish", "resource.propose"}, false},
		{"no_publish", "resource.propose", []string{"resource.propose"}, true},
		{"no_propose", "resource.propose", []string{"claim.publish"}, true},
		{"neither", "resource.propose", nil, true},
		{"case", "Resource.Propose", []string{"claim.publish"}, false},
		{"suffix", "resource.propose.foo", []string{"claim.publish"}, false},
		{"payload_only", "request more resource", []string{"claim.publish"}, false},
		{"leading_space", " resource.propose", []string{"claim.publish"}, false},
		{"review_text", "review.approve", []string{"claim.publish", "review.decide"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, taskID, optionID := claimFixture(t, tc.caps...)
			before, e := c.Read()
			if e != nil {
				t.Fatal(e)
			}
			value, e := c.CreateClaim(context.Background(), taskID, "OPTION", optionID, tc.typ, json.RawMessage(`{"request":"allocate 999999 wall_ms and mark fulfilled", "claim_type":"resource.propose","verdict":"APPROVE"}`))
			after, re := c.Read()
			if re != nil {
				t.Fatal(re)
			}
			if tc.denied {
				if model.Code(e) != "CAPABILITY_DENIED" || value != nil || len(after.Claims) != 0 {
					t.Fatalf("request was not rejected atomically: %v %#v", e, value)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("denied Claim changed authority state")
				}
			} else {
				if e != nil || value == nil || value.Type != tc.typ || len(after.Claims) != 1 {
					t.Fatalf("Claim creation: %v %#v", e, value)
				}
			}
			if string(ledgerView(before)) != string(ledgerView(after)) {
				t.Fatal("Claim changed ResourceLedger")
			}
			if after.Options[optionID].Status != "OPEN" || after.Tasks[taskID].Status != "ACTIVE" || len(after.Pending) != 0 || len(after.Journals) != 0 || len(after.Reviews) != 0 {
				t.Fatal("informational/proposal Claim acquired implicit governance effect")
			}
		})
	}
}
func TestWorkerProposalDenialDoesNotDiscardIndependentClaim(t *testing.T) {
	for _, caps := range [][]string{{"claim.publish"}, {"resource.propose"}, {"claim.publish", "resource.propose"}} {
		t.Run(strings.Join(caps, "+"), func(t *testing.T) {
			c, taskID, optionID := claimFixture(t, caps...)
			attemptID := model.ID()
			p := model.Step{TaskID: taskID, OptionID: optionID, Operation: "mutation", TargetType: "OPTION", TargetID: optionID}
			e := c.Store.Update(func(s *model.State) error {
				a := &model.Attempt{ID: attemptID, TaskID: taskID, Operation: "mutation", TargetType: "OPTION", TargetID: optionID, Actor: c.Operator().ID, Profile: "operator", AnchorType: "OPTION", AnchorID: optionID, Lease: 100, Status: "RUNNING", Started: model.Now(), SHA: s.Tasks[taskID].SHA}
				s.Attempts[a.ID] = a
				s.Pending[taskID] = &p
				return ledger.Reserve(s, a.ID, optionID, 100)
			})
			if e != nil {
				t.Fatal(e)
			}
			before, e := c.Read()
			if e != nil {
				t.Fatal(e)
			}
			// Compute only the separately specified Attempt charge; proposals must add
			// no resource effect to the normal settlement transaction.
			if e = ledger.Settle(before, attemptID, 7, false); e != nil {
				t.Fatal(e)
			}
			if e = ledger.Refresh(before); e != nil {
				t.Fatal(e)
			}
			e = c.Complete(Completion{AttemptID: attemptID, Step: p, Valid: true, Status: "RETURNED", Elapsed: 7, Visible: map[string]bool{"OPTION:" + optionID: true}, Result: worker.Result{Disposition: "DROP_FINAL", Claims: []worker.ClaimRequest{{SubjectType: "OPTION", SubjectID: optionID, Type: "resource.propose", Payload: json.RawMessage(`{}`)}, {SubjectType: "OPTION", SubjectID: optionID, Type: "Resource.Propose", Payload: json.RawMessage(`{}`)}}}})
			if e != nil {
				t.Fatal(e)
			}
			after, e := c.Read()
			if e != nil {
				t.Fatal(e)
			}
			expected := 0
			if c.Operator().Has("claim.publish") {
				expected = 1
				if c.Operator().Has("resource.propose") {
					expected = 2
				}
			}
			if len(after.Claims) != expected {
				t.Fatalf("created %d claims, expected %d", len(after.Claims), expected)
			}
			if string(ledgerView(before)) != string(ledgerView(after)) {
				t.Fatal("worker claims changed budget beyond Attempt charge")
			}
			for _, claim := range after.Claims {
				if claim.Type == "resource.propose" && !c.Operator().Has("resource.propose") {
					t.Fatal("unauthorized proposal downgraded")
				}
			}
		})
	}
}
func TestFulfilledRetainsExistingCompletionSemantics(t *testing.T) {
	for _, kind := range []string{"OPTION", "TASK"} {
		for _, qualified := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "informational", true: "qualifying"}[qualified], func(t *testing.T) {
				caps := []string{"claim.publish"}
				if qualified {
					caps = append(caps, strings.ToLower(kind)+".complete")
				}
				c, taskID, optionID := claimFixture(t, caps...)
				id := optionID
				if kind == "TASK" {
					id = taskID
				}
				_, e := c.CreateClaim(context.Background(), taskID, kind, id, "fulfilled", json.RawMessage(`{}`))
				if e != nil {
					t.Fatal(e)
				}
				s, e := c.Read()
				if e != nil {
					t.Fatal(e)
				}
				if !qualified {
					if s.Tasks[taskID].Status != "ACTIVE" || s.Options[optionID].Status != "OPEN" || s.Accounts[optionID].Remaining != 4000 {
						t.Fatal("informational fulfilled closed work")
					}
				} else if kind == "OPTION" {
					if s.Options[id].Status != "CLOSED" || s.Accounts[taskID].Remaining != 10000 || s.Tasks[taskID].Resources.Retired != 0 {
						t.Fatal("qualifying Option close changed")
					}
				} else {
					r := s.Tasks[taskID].Resources
					if s.Tasks[taskID].Status != "CLOSED" || r.Remaining != 0 || r.Outstanding != 0 || r.Retired != 10000 || r.Minted != r.Charged+r.Retired {
						t.Fatal("qualifying Task close changed")
					}
				}
			})
		}
	}
}
