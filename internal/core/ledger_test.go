package core

import (
	"context"
	"encoding/json"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
)

func TestRandomResourceOperationsConserve(t *testing.T) {
	c, taskID, _ := claimFixture(t, "option.propose", "option.allocate", "option.refine", "option.split", "option.merge", "option.complete", "task.extend", "task.complete")
	rng := rand.New(rand.NewSource(93021))
	ctx := context.Background()
	for i := 0; i < 500; i++ {
		s, e := c.Read()
		if e != nil {
			t.Fatal(e)
		}
		open := []string{}
		for id, o := range s.Options {
			if o.Status == "OPEN" {
				open = append(open, id)
			}
		}
		sort.Strings(open)
		if len(open) == 0 {
			if _, e = c.Propose(taskID, "new route", ""); e != nil {
				t.Fatal(e)
			}
			continue
		}
		id := open[rng.Intn(len(open))]
		n := int64(rng.Intn(301))
		before := s.Tasks[taskID].Resources.Minted
		switch rng.Intn(7) {
		case 0:
			_, e = c.Allocate(id, n+1)
		case 1:
			_, e = c.Split(id, []Child{{Text: "refined", Wall: n}}, true)
		case 2:
			_, e = c.Split(id, []Child{{Text: "left", Wall: n}, {Text: "right", Wall: n}}, false)
		case 3:
			if len(open) > 1 {
				other := open[rng.Intn(len(open))]
				_, e = c.Merge(taskID, MergeSpec{Text: "merged", Participants: []Participant{{ID: id, Wall: n}, {ID: other, Wall: n}}})
			}
		case 4:
			_, e = c.CloseOption(ctx, id)
		case 5:
			leaseID := model.ID()
			e = c.Store.Update(func(st *model.State) error {
				if n == 0 {
					return nil
				}
				if e := ledger.Reserve(st, leaseID, id, n); e != nil {
					return e
				}
				return ledger.Settle(st, leaseID, n/3, rng.Intn(5) == 0)
			})
		case 6:
			_, e = c.Propose(taskID, "new route", "")
		}
		if e != nil && model.Exit(model.Code(e)) != 3 {
			t.Fatalf("iteration %d: %v", i, e)
		}
		after, readErr := c.Read()
		if readErr != nil {
			t.Fatal(readErr)
		}
		r := after.Tasks[taskID].Resources
		if r.Minted != before || r.Minted != r.Remaining+r.Outstanding+r.Charged+r.Retired {
			t.Fatalf("conservation at %d: %+v", i, r)
		}
		for _, a := range after.Accounts {
			if a.Remaining < 0 {
				t.Fatal("negative balance")
			}
		}
		if e != nil {
			beforeBytes, _ := json.Marshal(s)
			afterBytes, _ := json.Marshal(after)
			if string(beforeBytes) != string(afterBytes) {
				t.Fatalf("failed operation partially committed at %d", i)
			}
		}
	}
	if _, e := c.Lifecycle(ctx, taskID, "close"); e != nil {
		t.Fatal(e)
	}
	s, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	r := s.Tasks[taskID].Resources
	if r.Minted != r.Charged+r.Retired || r.Outstanding != 0 || r.Remaining != 0 {
		t.Fatal("Task close conservation")
	}
	if _, e = c.Extend(taskID, 1); model.Code(e) != "INVALID_STATE" {
		t.Fatal("closed Task minted")
	}
}
func TestImmutableObjectsAndExplicitMergeLCA(t *testing.T) {
	c, taskID, parent := claimFixture(t, "option.propose", "option.split", "option.merge")
	children, e := c.Split(parent, []Child{{Text: "one", Wall: 1000}, {Text: "two", Wall: 1000}}, false)
	if e != nil {
		t.Fatal(e)
	}
	a, b := children.Children[0], children.Children[1]
	merged, e := c.Merge(taskID, MergeSpec{Text: "joined", Participants: []Participant{{ID: a.ID, Wall: 100}, {ID: b.ID, Wall: 200}}})
	if e != nil {
		t.Fatal(e)
	}
	if merged.Parent != parent || merged.Remaining != 300 {
		t.Fatal("merge did not use resource-tree LCA and explicit transfers")
	}
	before, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	e = c.Store.Update(func(s *model.State) error { s.Options[parent].Text = "rewritten"; return nil })
	if model.Code(e) != "CORE_INCONSISTENT" {
		t.Fatal("immutable Option update allowed")
	}
	after, e := c.Read()
	if e != nil {
		t.Fatal(e)
	}
	if !after.FailStop() {
		t.Fatal("synthetic invariant failure did not fail-stop")
	}
	after.Blockers = before.Blockers
	if !reflect.DeepEqual(before, after) {
		t.Fatal("immutable update partially committed")
	}
}
