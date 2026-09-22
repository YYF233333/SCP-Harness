package core

import (
	"context"
	"reflect"
	"testing"

	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
)

func TestV2TransferFloorOnlyRestrictsTransfers(t *testing.T) {
	c, taskID, id := claimFixture(t, "option.allocate", "option.propose", "option.refine")
	if e := c.Store.Update(func(s *model.State) error {
		s.Tasks[taskID].Resources.Minted = 1500000 + 4000
		s.Accounts[taskID].Remaining = 1500000
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	before := readState(t, c)
	if _, e := c.Allocate(id, 600000); model.Code(e) != "INSUFFICIENT_RESOURCE" {
		t.Fatal("root transfer floor", e)
	}
	if !reflect.DeepEqual(before, readState(t, c)) {
		t.Fatal("rejected transfer was not atomic")
	}
	for _, n := range []int64{480000, 600000} {
		if e := c.Store.Update(func(s *model.State) error {
			lease := model.ID()
			if e := ledger.Reserve(s, lease, taskID, n); e != nil {
				return e
			}
			return ledger.Settle(s, lease, n, false)
		}); e != nil {
			t.Fatal("Task consumption must remain possible", e)
		}
	}
	if readState(t, c).Accounts[taskID].Remaining != 420000 {
		t.Fatal("root consumption floor incorrectly applied")
	}
	if e := c.Store.Update(func(s *model.State) error {
		s.Tasks[taskID].Resources.Minted += 1200000
		s.Accounts[taskID].Remaining += 1200000
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	child, e := c.Propose(taskID, "descendant", id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Allocate(child.ID, 60000); e != nil {
		t.Fatal(e)
	}
	s := readState(t, c)
	if s.Accounts[child.ID].Remaining != 60000 || s.Accounts[id].Remaining != 0 || s.Accounts[taskID].Remaining != 1564000 {
		t.Fatal("atomic ancestry transfer")
	}
}

func TestV2ObjectiveAndSupersededHistory(t *testing.T) {
	c, taskID, id := claimFixture(t, "task.revise", "option.release", "option.refine", "option.merge", "option.propose")
	ch, e := c.ReleaseOption(id)
	if e != nil {
		t.Fatal(e)
	}
	old := readState(t, c).Tasks[taskID].Objective
	if _, e = c.ReviseTask(taskID, "corrected objective"); e != nil {
		t.Fatal(e)
	}
	s := readState(t, c)
	if len(s.ObjectiveRevisions) != 1 || s.ObjectiveRevisions[0].Old != old || s.ObjectiveRevisions[0].Operator != c.Operator().ID || s.Changes[ch.ID].Objective != old {
		t.Fatal("objective provenance rewritten")
	}
	o, e := c.Propose(taskID, "new option", "")
	if e != nil {
		t.Fatal(e)
	}
	refined, e := c.Split(o.ID, []Child{{Text: "refinement"}}, true)
	if e != nil {
		t.Fatal(e)
	}
	s = readState(t, c)
	if s.Options[o.ID].Status != "CLOSED" || s.Options[o.ID].CloseReason != "SUPERSEDED" || refined.Children[0].Status != "OPEN" {
		t.Fatal("refine close reason")
	}
	other, e := c.Propose(taskID, "other", "")
	if e != nil {
		t.Fatal(e)
	}
	merged, e := c.Merge(taskID, MergeSpec{Text: "merged", Participants: []Participant{{ID: refined.Children[0].ID}, {ID: other.ID}}})
	if e != nil {
		t.Fatal(e)
	}
	s = readState(t, c)
	if s.Options[other.ID].CloseReason != "SUPERSEDED" || s.Options[refined.Children[0].ID].CloseReason != "SUPERSEDED" || merged.Status != "OPEN" {
		t.Fatal("merge close reasons")
	}
}

func TestV2CapabilityAndInvalidLifecycle(t *testing.T) {
	c, _, id := claimFixture(t, "option.release", "change.pause", "change.resume", "change.abort")
	ch, e := c.ReleaseOption(id)
	if e != nil {
		t.Fatal(e)
	}
	before := readState(t, c)
	if _, e = c.ChangeAction(context.Background(), ch.ID, "promote"); model.Code(e) != "CAPABILITY_DENIED" {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, readState(t, c)) {
		t.Fatal("denial changed state")
	}
	if _, e = c.ChangeAction(context.Background(), ch.ID, "resume"); model.Code(e) != "INVALID_STATE" {
		t.Fatal(e)
	}
	for _, action := range []string{"pause", "resume", "abort"} {
		if _, e = c.ChangeAction(context.Background(), ch.ID, action); e != nil {
			t.Fatal(action, e)
		}
	}
	if _, e = c.ChangeAction(context.Background(), ch.ID, "resume"); model.Code(e) != "INVALID_STATE" {
		t.Fatal("aborted Change resumed", e)
	}
	if _, e = c.ReleaseOption(id); e != nil {
		t.Fatal("explicit new work after abort", e)
	}
}

func TestV2RefineRunningChangeDoesNotBlockControlPlane(t *testing.T) {
	c, taskID, id := claimFixture(t, "option.release", "option.refine", "process.execute", "sandbox.write", "repository.read")
	ch, e := c.ReleaseOption(id)
	if e != nil {
		t.Fatal(e)
	}
	p, e := c.Next("owner")
	if e != nil {
		t.Fatal(e)
	}
	a, e := c.PrepareAttempt(*p, "owner")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Split(id, []Child{{Text: "successor"}}, true); e != nil {
		t.Fatal(e)
	}
	s := readState(t, c)
	if s.Changes[ch.ID].State != "RUNNING" || s.Options[id].CloseReason != "SUPERSEDED" || s.Attempts[a.ID].TaskID != taskID {
		t.Fatal("control edit altered released identity")
	}
	if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Status: "INTERRUPTED"}); e != nil {
		t.Fatal(e)
	}
	if readState(t, c).Changes[ch.ID].State != "PAUSED" {
		t.Fatal("refinement lost interrupted Change")
	}
}
