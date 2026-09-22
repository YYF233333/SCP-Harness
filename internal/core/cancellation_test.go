package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"scp-harness/internal/model"
)

// These are the actual transactions used by Lifecycle, not a parallel model.
// Explicit interleaving fixes both race windows: before the active lease settles,
// and after settlement but before the lifecycle's final transaction. No sleeps,
// scheduling luck, test hooks or alternate authority paths are involved.
func TestR1ClaimsPreserveLifecycleCancellation(t *testing.T) {
	for _, action := range []string{"suspend", "close"} {
		for _, typ := range []string{"note", "resource.propose", "Resource.Propose", "resource.propose.foo", "fulfilled"} {
			t.Run(action+"/"+typ, func(t *testing.T) {
				c, taskID, optionID := claimFixture(t, "option.release", "claim.publish", "resource.propose", "task.suspend", "task.complete", "sandbox.write", "process.execute", "repository.read")
				owner := model.ID()
				if _, e := c.ReleaseOption(optionID); e != nil {
					t.Fatal(e)
				}
				step, e := c.Next(owner)
				if e != nil || step == nil {
					t.Fatalf("initial step: %v", e)
				}
				a, e := c.PrepareAttempt(*step, owner)
				if e != nil {
					t.Fatal(e)
				}
				if e = c.MarkRunning(a.ID); e != nil {
					t.Fatal(e)
				}
				if e = c.requestCancellation(taskID); e != nil {
					t.Fatal(e)
				}
				kind, subject := "TASK", taskID
				if typ == "fulfilled" {
					// No option.complete capability: this is an informational Claim.
					kind, subject = "OPTION", optionID
				}
				claim := func() {
					t.Helper()
					before, e := c.Read()
					if e != nil {
						t.Fatal(e)
					}
					if _, e = c.CreateClaim(context.Background(), taskID, kind, subject, typ, json.RawMessage(`{"request":"resume work"}`)); e != nil {
						t.Fatal(e)
					}
					after, e := c.Read()
					if e != nil {
						t.Fatal(e)
					}
					if !after.Cancellations[taskID] || !reflect.DeepEqual(before.Cancellations, after.Cancellations) || !reflect.DeepEqual(before.Interrupts, after.Interrupts) || !reflect.DeepEqual(before.Pending, after.Pending) {
						t.Fatal("Claim changed lifecycle cancellation control state")
					}
					if string(ledgerView(before)) != string(ledgerView(after)) {
						t.Fatal("Claim changed resource state")
					}
				}
				claim()
				if _, e = c.Next(model.ID()); model.Code(e) != "BLOCKED" {
					t.Fatalf("second owner admitted while cancellation waits: %v", e)
				}
				if e = c.Complete(Completion{AttemptID: a.ID, Step: *step, Status: "INTERRUPTED", Elapsed: 7}); e != nil {
					t.Fatal(e)
				}
				claim()
				if next, e := c.Next(owner); e != nil || next != nil {
					t.Fatalf("Attempt restarted before %s finalized: %+v %v", action, next, e)
				}
				result, e := c.finishLifecycle(taskID, action, "", "")
				if e != nil {
					t.Fatal(e)
				}
				expected := "SUSPENDED"
				if action == "close" {
					expected = "CLOSED"
				}
				s, e := c.Read()
				if e != nil {
					t.Fatal(e)
				}
				if result.Status != expected || s.Slot.State != "IDLE" || len(s.Leases) != 0 || s.Pending[taskID] != nil || s.Cancellations[taskID] || len(s.Attempts) != 1 || s.Attempts[a.ID].Status != "INTERRUPTED" {
					t.Fatal("lifecycle final state, slot and lease disagree")
				}
				r := s.Tasks[taskID].Resources
				if r.Outstanding != 0 || r.Minted != r.Remaining+r.Charged+r.Retired {
					t.Fatal("cancellation broke conservation")
				}
			})
		}
	}
}
