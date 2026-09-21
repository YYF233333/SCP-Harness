package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
	"scp-harness/internal/store"
	"scp-harness/internal/worker"
)

func TestR1bCancellationCannotBeTakenOver(t *testing.T) {
	for _, action := range []string{"option_close", "option_fulfilled"} {
		t.Run(action, func(t *testing.T) {
			c, taskID, optionID := claimFixture(t, "claim.publish", "option.complete", "task.suspend", "process.execute", "sandbox.write", "repository.read")
			owner := model.ID()
			p, e := c.Next(owner)
			if e != nil || p == nil {
				t.Fatalf("next: %v", e)
			}
			a, e := c.PrepareAttempt(*p, owner)
			if e != nil {
				t.Fatal(e)
			}
			if e = c.requestCancellation(taskID); e != nil {
				t.Fatal(e)
			}
			if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Status: "INTERRUPTED", Elapsed: 1}); e != nil {
				t.Fatal(e)
			}
			// A controller died after cancellation/settlement, before its final
			// transaction. Another completion cannot take over its protection.
			if action == "option_close" {
				_, e = c.CloseOption(context.Background(), optionID)
			} else {
				_, e = c.CreateClaim(context.Background(), taskID, "OPTION", optionID, "fulfilled", json.RawMessage(`{}`))
			}
			if model.Code(e) != "BLOCKED" {
				t.Fatalf("competing completion took over cancellation: %v", e)
			}
			s, e := c.Read()
			if e != nil || !s.Cancellations[taskID] || s.Options[optionID].Status != "OPEN" {
				t.Fatalf("cancellation/Option changed: %v", e)
			}
		})
	}
}

func controlAttempt(t *testing.T, c *Core) (*model.Step, *model.Attempt, string) {
	t.Helper()
	owner := model.ID()
	p, e := c.Next(owner)
	if e != nil || p == nil {
		t.Fatalf("next: %v", e)
	}
	a, e := c.PrepareAttempt(*p, owner)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.MarkRunning(a.ID); e != nil {
		t.Fatal(e)
	}
	return p, a, owner
}

func TestR1bControlInterleavings(t *testing.T) {
	for _, action := range []string{"option_close", "option_fulfilled"} {
		t.Run(action, func(t *testing.T) {
			c, taskID, optionID := claimFixture(t, "claim.publish", "resource.propose", "option.complete", "task.suspend", "process.execute", "sandbox.write", "repository.read")
			p, a, owner := controlAttempt(t, c)
			_, unlock, e := c.beginControl(taskID)
			if e != nil {
				t.Fatal(e)
			}
			defer unlock()
			compete := func() {
				t.Helper()
				before, e := c.Read()
				if e != nil {
					t.Fatal(e)
				}
				if action == "option_close" {
					_, e = c.CloseOption(context.Background(), optionID)
				} else {
					_, e = c.CreateClaim(context.Background(), taskID, "OPTION", optionID, "fulfilled", json.RawMessage(`{}`))
				}
				if model.Code(e) != "BLOCKED" {
					t.Fatalf("competing control was not BLOCKED: %v", e)
				}
				after, e := c.Read()
				if e != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("blocked control changed state: %v", e)
				}
			}
			// Exact production phases of suspend. The three boundaries are
			// deterministic: before cancel, before settlement, before final commit.
			compete()
			if e = c.requestCancellation(taskID); e != nil {
				t.Fatal(e)
			}
			compete()
			if _, e = c.PrepareAttempt(*p, owner); model.Code(e) != "BLOCKED" {
				t.Fatalf("canceled admission created an Attempt: %v", e)
			}
			if _, e = c.Next(model.ID()); model.Code(e) != "BLOCKED" {
				t.Fatalf("new owner entered before settlement: %v", e)
			}
			// This must commit while the control gate is held (no SQLite write
			// transaction may be held across the cancellation wait).
			if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Status: "INTERRUPTED", Elapsed: 7}); e != nil {
				t.Fatal(e)
			}
			compete()
			for _, typ := range []string{"note", "resource.propose"} {
				if _, e = c.CreateClaim(context.Background(), taskID, "TASK", taskID, typ, json.RawMessage(`{}`)); e != nil {
					t.Fatal(e)
				}
			}
			if next, e := c.Next(owner); e != nil || next != nil {
				t.Fatalf("new Attempt admitted before suspend commit: %+v %v", next, e)
			}
			if _, e = c.finishLifecycle(taskID, "suspend", "", ""); e != nil {
				t.Fatal(e)
			}
			s, e := c.Read()
			if e != nil || s.Tasks[taskID].Status != "SUSPENDED" || !store.TaskQuiescent(s, taskID) || s.Cancellations[taskID] || s.Options[optionID].Status != "OPEN" || len(s.Attempts) != 1 || s.Attempts[a.ID].Status != "INTERRUPTED" {
				t.Fatalf("suspend state/slot/lease invariant: %v", e)
			}
		})
	}
}

func TestR1bTaskControlIdentityAndEntrypoints(t *testing.T) {
	c, taskID, optionID := claimFixture(t, "claim.publish", "option.complete", "task.complete", "task.suspend", "task.resume")
	unlock, e := c.LockTaskControl(taskID)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	for _, action := range []string{"suspend", "resume", "close"} {
		if _, e = c.Lifecycle(context.Background(), taskID, action); model.Code(e) != "BLOCKED" {
			t.Fatalf("%s bypassed Task control gate: %v", action, e)
		}
	}
	if _, e = c.CloseOption(context.Background(), optionID); model.Code(e) != "BLOCKED" {
		t.Fatalf("option close bypassed Task gate: %v", e)
	}
	for kind, id := range map[string]string{"TASK": taskID, "OPTION": optionID} {
		if _, e = c.CreateClaim(context.Background(), taskID, kind, id, "fulfilled", json.RawMessage(`{}`)); model.Code(e) != "BLOCKED" {
			t.Fatalf("%s completion bypassed Task gate: %v", kind, e)
		}
	}
	// A hard link is a different path spelling with the same physical database.
	alias := filepath.Join(t.TempDir(), "database-alias.db")
	if e = os.Link(c.Config.Database, alias); e != nil {
		t.Fatal(e)
	}
	copy, cfg := *c, *c.Config
	cfg.Database = alias
	copy.Config = &cfg
	if release, e := copy.LockTaskControl(taskID); model.Code(e) != "BLOCKED" {
		if release != nil {
			release()
		}
		t.Fatalf("database alias bypassed control gate: %v", e)
	}
	otherTask, e := c.LockTaskControl(model.ID())
	if e != nil {
		t.Fatalf("gate incorrectly spans other Tasks: %v", e)
	}
	otherTask()
	other, _, _ := claimFixture(t)
	otherDatabase, e := other.LockTaskControl(taskID)
	if e != nil {
		t.Fatalf("gate incorrectly spans other databases: %v", e)
	}
	otherDatabase()
}

func TestR1bCancellationErrorKeepsProtection(t *testing.T) {
	c, taskID, _ := claimFixture(t, "task.suspend", "process.execute", "sandbox.write", "repository.read")
	p, a, _ := controlAttempt(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Lifecycle(ctx, taskID, "suspend"); model.Code(e) != "PRECONDITION_FAILED" {
		t.Fatalf("canceled control wait: %v", e)
	}
	unlock, e := c.LockTaskControl(taskID)
	if e != nil {
		t.Fatalf("error leaked control handle: %v", e)
	}
	unlock()
	if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Status: "INTERRUPTED", Elapsed: 1}); e != nil {
		t.Fatal(e)
	}
	s, e := c.Read()
	if e != nil || !s.Cancellations[taskID] || s.Tasks[taskID].Status != "ACTIVE" || !store.TaskQuiescent(s, taskID) {
		t.Fatalf("error/settlement cleared cancellation: %v", e)
	}
	if next, e := c.Next(model.ID()); e != nil || next != nil {
		t.Fatalf("error unprotected scheduling: %+v %v", next, e)
	}
}

func TestR1bWorkerCompletionDoesNotBlockSettlement(t *testing.T) {
	for _, kind := range []string{"TASK", "OPTION"} {
		for _, held := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/free", true: "/held"}[held], func(t *testing.T) {
				c, taskID, optionID := claimFixture(t, "claim.publish", "task.complete", "option.complete", "process.execute", "sandbox.write", "repository.read")
				p, a, _ := controlAttempt(t, c)
				if held {
					unlock, e := c.LockTaskControl(taskID)
					if e != nil {
						t.Fatal(e)
					}
					defer unlock()
				}
				id := optionID
				if kind == "TASK" {
					id = taskID
				}
				v := Completion{AttemptID: a.ID, Step: *p, Status: "RETURNED", Valid: true, Elapsed: 7, Visible: map[string]bool{kind + ":" + id: true}, Result: worker.Result{Disposition: "DROP_FINAL", Claims: []worker.ClaimRequest{{SubjectType: kind, SubjectID: id, Type: "fulfilled", Payload: json.RawMessage(`{}`)}, {SubjectType: kind, SubjectID: id, Type: "note", Payload: json.RawMessage(`{}`)}}}}
				if e := c.Complete(v); e != nil {
					t.Fatal(e)
				}
				s, e := c.Read()
				if e != nil || !store.TaskQuiescent(s, taskID) || s.Attempts[a.ID].Status != "RETURNED" {
					t.Fatalf("control gate prevented settlement: %v", e)
				}
				if held {
					if s.Tasks[taskID].Status != "ACTIVE" || s.Options[optionID].Status != "OPEN" || len(s.Claims) != 1 {
						t.Fatal("competing worker Claim changed control state")
					}
					blocked := false
					for _, event := range s.Events {
						blocked = blocked || event.Code == "BLOCKED"
					}
					if !blocked {
						t.Fatal("worker completion rejection was not recorded")
					}
				} else if kind == "TASK" && s.Tasks[taskID].Status != "CLOSED" || !held && kind == "OPTION" && s.Options[optionID].Status != "CLOSED" {
					t.Fatal("uncontended completion lost its existing semantics")
				}
			})
		}
	}
}

func TestR1bInactiveTaskInvariant(t *testing.T) {
	for _, activity := range []string{"attempt", "lease", "slot"} {
		t.Run(activity, func(t *testing.T) {
			c, taskID, optionID := claimFixture(t, "task.suspend", "process.execute", "sandbox.write", "repository.read")
			if activity == "attempt" {
				owner := model.ID()
				p, e := c.Next(owner)
				if e != nil || p == nil {
					t.Fatalf("next: %v", e)
				}
				if _, e = c.PrepareAttempt(*p, owner); e != nil {
					t.Fatal(e)
				}
			} else if e := c.Store.Update(func(s *model.State) error {
				if activity == "lease" {
					return ledger.Reserve(s, model.ID(), optionID, 10)
				}
				s.Slot = model.Slot{State: "BUSY"}
				s.SlotTask, s.SlotOwner = taskID, model.ID()
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			for _, status := range []string{"SUSPENDED", "CLOSED"} {
				s, e := c.Read()
				if e != nil {
					t.Fatal(e)
				}
				s.Tasks[taskID].Status = status
				if status == "CLOSED" {
					// Retire remaining resource so the slot-only case cannot pass
					// merely because the older ledger invariant rejects balances.
					for id, account := range s.Accounts {
						s.Tasks[taskID].Resources.Retired += account.Remaining
						account.Remaining = 0
						if option := s.Options[id]; option != nil {
							option.Remaining = 0
						}
					}
					s.Tasks[taskID].Resources.Remaining = 0
				}
				if model.Code(store.Validate(s)) != "CORE_INCONSISTENT" {
					t.Errorf("%s accepted with %s", status, activity)
				}
			}
			if _, e := c.finishLifecycle(taskID, "suspend", "", ""); model.Code(e) != "BLOCKED" {
				t.Fatalf("suspend final transaction did not refuse %s: %v", activity, e)
			}
		})
	}
}
