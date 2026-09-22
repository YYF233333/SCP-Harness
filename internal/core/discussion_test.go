package core

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

func readState(t *testing.T, c *Core) *model.State {
	t.Helper()
	s, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func requireIdle(t *testing.T, c *Core) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if p, e := c.Next("test"); e != nil || p != nil {
			t.Fatalf("unauthorized dispatch: %+v %v", p, e)
		}
	}
	if s := readState(t, c); len(s.Pending) != 0 || s.Slot.State != "IDLE" {
		t.Fatal("idle retained pending/slot")
	}
}

func TestReleaseOnlySeedsOneChain(t *testing.T) {
	c, taskID, id := claimFixture(t, "option.propose", "option.allocate", "option.release", "claim.publish")
	for i := 0; i < 100; i++ {
		o, e := c.Propose(taskID, fmt.Sprint("candidate ", i), "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = c.Allocate(o.ID, 1); e != nil {
			t.Fatal(e)
		}
		requireIdle(t, c)
	}
	if _, e := c.CreateClaim(context.Background(), taskID, "OPTION", id, "option.release", json.RawMessage(`{"release":true}`)); e != nil {
		t.Fatal(e)
	}
	requireIdle(t, c)
	if len(readState(t, c).Attempts) != 0 {
		t.Fatal("funding/Claim created Attempt")
	}
	// Competing host releases share the existing Task gate and transaction.
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := c.ReleaseOption(id); errs <- e }()
	}
	codes := map[string]int{}
	for i := 0; i < 2; i++ {
		codes[model.Code(<-errs)]++
	}
	if codes["BLOCKED"] != 1 {
		t.Fatal("duplicate release not rejected", codes)
	}
	s := readState(t, c)
	want := &model.Step{TaskID: taskID, OptionID: id, Operation: "mutation", TargetType: "OPTION", TargetID: id}
	if len(s.Pending) != 1 || !reflect.DeepEqual(s.Pending[taskID], want) || len(s.Attempts) != 0 || s.Slot.State != "IDLE" {
		t.Fatal("release projection/side effect")
	}
	n := 0
	for _, event := range s.Events {
		if event.Code == "OPTION_RELEASED" {
			n++
		}
	}
	if n != 1 {
		t.Fatal("release audit count", n)
	}
}

func TestReleaseAndDiscussPreconditionsAreAtomic(t *testing.T) {
	for _, operation := range []string{"release", "discuss"} {
		for _, condition := range []string{"capability", "publish", "missing", "closed", "inactive", "exploration", "resource", "pending", "cancellation", "gate"} {
			t.Run(operation+"/"+condition, func(t *testing.T) {
				c, taskID, id := claimFixture(t, "option.release", "option.discuss", "claim.publish")
				code := ""
				e := c.Store.Update(func(s *model.State) error {
					switch condition {
					case "closed":
						s.Options[id].Status = "CLOSED"
						code = "INVALID_STATE"
					case "inactive":
						s.Tasks[taskID].Status = "SUSPENDED"
						code = "INVALID_STATE"
					case "exploration":
						s.Exploration[taskID].Done = false
						code = "INVALID_STATE"
					case "resource":
						anchor := id
						if operation == "discuss" {
							anchor = taskID
						}
						s.Tasks[taskID].Resources.Charged += s.Accounts[anchor].Remaining
						s.Accounts[anchor].Remaining = 0
						code = "INSUFFICIENT_RESOURCE"
					case "pending":
						s.Pending[taskID] = &model.Step{TaskID: taskID, OptionID: id, Operation: "mutation", TargetType: "OPTION", TargetID: id}
						code = "BLOCKED"
					case "cancellation":
						s.Cancellations[taskID] = true
						code = "BLOCKED"
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
				if condition == "missing" {
					id = "absent"
					code = "NOT_FOUND"
				}
				if condition == "gate" {
					unlock, e := c.LockTaskControl(taskID)
					if e != nil {
						t.Fatal(e)
					}
					defer unlock()
					code = "BLOCKED"
				}
				if condition == "capability" || condition == "publish" {
					card := c.Operator()
					card.Capabilities = nil
					if condition == "publish" {
						card.Capabilities = []config.Capability{{Name: "option.discuss"}}
					}
					c.Config.Cards[c.Config.Operator] = card
					code = "CAPABILITY_DENIED"
				}
				before := readState(t, c)
				if operation == "release" {
					_, e = c.ReleaseOption(id)
				} else {
					_, e = c.DiscussOption(id, "why?")
				}
				if model.Code(e) != code || !reflect.DeepEqual(before, readState(t, c)) {
					t.Fatalf("non-atomic %s: %v", code, e)
				}
			})
		}
	}
}

func TestAllCreationPathsRemainInert(t *testing.T) {
	c, taskID, id := claimFixture(t, "option.propose", "option.allocate", "option.refine", "option.split", "option.merge", "option.release", "claim.publish", "repository.read", "sandbox.write", "process.execute")
	a, e := c.Propose(taskID, "proposed", "")
	if e != nil {
		t.Fatal(e)
	}
	r, e := c.Split(id, []Child{{Text: "refined", Wall: 10}}, true)
	if e != nil {
		t.Fatal(e)
	}
	split, e := c.Split(id, []Child{{Text: "left", Wall: 10}, {Text: "right", Wall: 10}}, false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Merge(taskID, MergeSpec{Text: "merged", Participants: []Participant{{ID: r.Children[0].ID, Wall: 1}, {ID: split.Children[0].ID, Wall: 1}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(a.ID, 10); e != nil {
		t.Fatal(e)
	}
	requireIdle(t, c)
	// Worker proposals and synthesis use the production completion path.
	if e = c.Store.Update(func(s *model.State) error { s.Exploration[taskID] = &model.Exploration{}; return nil }); e != nil {
		t.Fatal(e)
	}
	complete := func(op string, result worker.Result) {
		t.Helper()
		p, e := c.Next("test")
		if e != nil || p == nil || p.Operation != op {
			t.Fatalf("%s: %+v %v", op, p, e)
		}
		a, e := c.PrepareAttempt(*p, "test")
		if e != nil {
			t.Fatal(e)
		}
		if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Valid: true, Status: "RETURNED", Result: result, Visible: map[string]bool{"OPTION:" + id: true}}); e != nil {
			t.Fatal(e)
		}
	}
	complete("option_generation", worker.Result{Options: []worker.OptionRequest{{Text: "generated A"}, {Text: "generated B"}}})
	batch := readState(t, c).Exploration[taskID].Batch
	complete("merge_judge", worker.Result{Groups: [][]string{batch}})
	complete("merge_synth", worker.Result{Text: "synthesized"})
	requireIdle(t, c)
	if _, e = c.ReleaseOption(id); e != nil {
		t.Fatal(e)
	}
	complete("mutation", worker.Result{Disposition: "DROP_FINAL", Options: []worker.OptionRequest{{Text: "mutation child"}}, Claims: []worker.ClaimRequest{{SubjectType: "OPTION", SubjectID: id, Type: "option.release", Payload: json.RawMessage(`{"release":true}`)}}})
	texts := map[string]bool{}
	for _, o := range readState(t, c).Options {
		texts[o.Text] = true
		if o.Remaining == 0 {
			if _, e = c.Allocate(o.ID, 1); e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, text := range []string{"proposed", "refined", "left", "right", "merged", "generated A", "generated B", "synthesized", "mutation child"} {
		if !texts[text] {
			t.Fatal("missing creation path", text)
		}
	}
	requireIdle(t, c)
}

func TestChainTerminationConsumesRelease(t *testing.T) {
	for _, outcome := range []string{"DROP_FINAL", "invalid", "CRASHED", "TIMED_OUT", "INTERRUPTED", "PRECONDITION_CHANGED", "promotion"} {
		t.Run(outcome, func(t *testing.T) {
			c, taskID, id := claimFixture(t, "option.release", "process.execute", "repository.read", "sandbox.write")
			if _, e := c.ReleaseOption(id); e != nil {
				t.Fatal(e)
			}
			p, e := c.Next("test")
			if e != nil {
				t.Fatal(e)
			}
			if outcome == "PRECONDITION_CHANGED" || outcome == "promotion" {
				e = c.EndPromotion(*p, "", outcome)
			} else {
				a, err := c.PrepareAttempt(*p, "test")
				if err != nil {
					t.Fatal(err)
				}
				v := Completion{AttemptID: a.ID, Step: *p, Status: "RETURNED", Valid: true, Result: worker.Result{Disposition: "DROP_FINAL"}}
				if outcome == "invalid" {
					v.Valid = false
				} else if outcome != "DROP_FINAL" {
					v.Status = outcome
				}
				e = c.Complete(v)
			}
			if e != nil {
				t.Fatal(e)
			}
			s := readState(t, c)
			if s.Options[id].Status != "OPEN" || s.Options[id].Remaining <= 0 || s.Pending[taskID] != nil {
				t.Fatal("termination changed Option")
			}
			requireIdle(t, c)
			if _, e = c.ReleaseOption(id); e != nil {
				t.Fatal("second explicit cycle", e)
			}
		})
	}
}

func TestCommentThreadAndDiscussionCompletion(t *testing.T) {
	c, taskID, id := claimFixture(t, "claim.publish", "option.discuss", "option.release", "option.refine", "option.split", "option.merge", "option.allocate")
	before := readState(t, c)
	comment, e := c.CommentOption(id, "why?")
	if e != nil {
		t.Fatal(e)
	}
	after := readState(t, c)
	if comment.Type != "discussion.comment" || comment.SubjectID != id || comment.SubjectType != "OPTION" || comment.Issuer != c.Operator().ID || comment.Revision != before.Tasks[taskID].Revision || len(after.Pending) != 0 || len(after.Attempts) != 0 || string(ledgerView(before)) != string(ledgerView(after)) {
		t.Fatal("comment side effect")
	}
	// Include equal timestamps and an unrelated claim to prove the tie breaker/filter.
	if e = c.Store.Update(func(s *model.State) error {
		for _, suffix := range []string{"b", "a"} {
			s.Claims[suffix] = &model.Claim{ID: suffix, TaskID: taskID, SubjectType: "OPTION", SubjectID: id, Type: "discussion.reply", Payload: json.RawMessage(`{"text":"reply"}`), Issuer: "fixture", Created: "2000"}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	thread, e := c.OptionThread(id)
	if e != nil || len(thread.Messages) != 3 || thread.Messages[0].ID != "a" || thread.Messages[1].ID != "b" {
		t.Fatal("thread order", e)
	}
	child, e := c.Split(id, []Child{{Text: "unfunded"}}, true)
	if e != nil {
		t.Fatal(e)
	}
	id = child.Children[0].ID
	before = readState(t, c)
	request, e := c.DiscussOption(id, "what about X?")
	if e != nil || !request.Queued {
		t.Fatal(e)
	}
	p, e := c.Next("test")
	if e != nil || p.Operation != "discussion" {
		t.Fatal(e)
	}
	a, e := c.PrepareAttempt(*p, "test")
	if e != nil {
		t.Fatal(e)
	}
	if a.AnchorID != taskID || a.AnchorType != "TASK" || a.TargetID != id {
		t.Fatal("discussion anchor")
	}
	if _, e = c.CommentOption(id, "additional context for next Attempt"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Split(id, []Child{{Text: "changed"}}, true); model.Code(e) != "BLOCKED" {
		t.Fatal(e)
	}
	if _, e = c.Merge(taskID, MergeSpec{Text: "merge", Participants: []Participant{{ID: id}, {ID: comment.SubjectID}}}); model.Code(e) != "BLOCKED" {
		t.Fatal(e)
	}
	if _, e = c.Allocate(id, 1); e != nil {
		t.Fatal("active allocation must remain allowed", e)
	}
	if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Valid: true, Status: "RETURNED", Elapsed: 7, Result: worker.Result{Text: "Conclusion with reasons, risks and recommendation."}}); e != nil {
		t.Fatal(e)
	}
	after = readState(t, c)
	if after.Accounts[taskID].Remaining != before.Accounts[taskID].Remaining-7 || after.Tasks[taskID].Resources.Charged != 7 || len(after.Artifacts) != 0 || after.Options[id].Text != "unfunded" {
		t.Fatal("discussion effects/ledger")
	}
	thread, e = c.OptionThread(id)
	if e != nil || len(thread.Messages) != 3 {
		t.Fatal(e)
	}
	last := thread.Messages[2]
	if last.Type != "discussion.reply" || last.Issuer != a.Actor || last.Revision != a.Revision {
		t.Fatal("reply identity")
	}
	requireIdle(t, c)
}

func TestInvalidDiscussionEndsWithoutRetry(t *testing.T) {
	cases := []string{
		`{"schema_version":0,"operation":"discussion","text":"ok","release":true}`,
		`{"schema_version":0,"operation":"discussion","text":"ok","text":"again"}`,
		`{"schema_version":0,"operation":"discussion"}`,
		`{"schema_version":0,"operation":"discussion","text":" "}`,
		`{"schema_version":0,"operation":"mutation","text":"ok"}`,
		`{"schema_version":0,"operation":"discussion","text":null}`,
		`{"schema_version":0,"operation":"discussion","text":1}`,
		"CRASHED", "TIMED_OUT", "INTERRUPTED", "denied",
	}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			c, _, id := claimFixture(t, "option.discuss", "claim.publish")
			if _, e := c.DiscussOption(id, "question"); e != nil {
				t.Fatal(e)
			}
			p, e := c.Next("test")
			if e != nil {
				t.Fatal(e)
			}
			a, e := c.PrepareAttempt(*p, "test")
			if e != nil {
				t.Fatal(e)
			}
			result, parseErr := worker.Parse([]byte(raw), "discussion", 10000)
			v := Completion{AttemptID: a.ID, Step: *p, Status: "RETURNED", Result: result, Valid: parseErr == nil}
			if raw == "CRASHED" || raw == "TIMED_OUT" || raw == "INTERRUPTED" {
				v.Status = raw
			} else if raw == "denied" {
				card := c.Config.Cards["discussion"]
				card.Capabilities = nil
				c.Config.Cards["discussion"] = card
				v.Valid = true
				v.Result.Text = "valid"
			} else if model.Code(parseErr) != "SCHEMA_INVALID" {
				t.Fatal("invalid accepted", parseErr)
			}
			if e = c.Complete(v); e != nil {
				t.Fatal(e)
			}
			if len(readState(t, c).Claims) != 1 {
				t.Fatal("failed discussion replied")
			}
			requireIdle(t, c)
		})
	}
}
