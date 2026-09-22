package ledger

import (
	"math"
	"scp-harness/internal/model"
)

func Add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || b > math.MaxInt64-a {
		return 0, model.Err("PRECONDITION_FAILED", "negative or overflowing wall_ms")
	}
	return a + b, nil
}

const MinRootAfterTransferMS int64 = 1200000

func Move(s *model.State, from, to string, n int64) error {
	a, b := s.Accounts[from], s.Accounts[to]
	if a == nil || b == nil || a.TaskID != b.TaskID || from == to || n < 0 {
		return model.Err("PRECONDITION_FAILED", "invalid resource transfer")
	}
	if a.Parent == "" && n > 0 && a.Remaining-n < MinRootAfterTransferMS {
		return model.Err("INSUFFICIENT_RESOURCE", "Task root transfer must retain %d ms", MinRootAfterTransferMS)
	}
	if a.Remaining < n {
		return model.Err("INSUFFICIENT_RESOURCE", "account %s has %d; requested %d", from, a.Remaining, n)
	}
	v, e := Add(b.Remaining, n)
	if e != nil {
		return e
	}
	a.Remaining -= n
	b.Remaining = v
	return nil
}
func Reserve(s *model.State, id, anchor string, n int64) error {
	a := s.Accounts[anchor]
	if a == nil || n <= 0 || s.Leases[id] != nil {
		return model.Err("PRECONDITION_FAILED", "invalid lease")
	}
	if a.Remaining < n {
		return model.Err("INSUFFICIENT_RESOURCE", "lease exceeds remaining resource")
	}
	a.Remaining -= n
	s.Leases[id] = &model.Lease{TaskID: a.TaskID, Anchor: anchor, Amount: n}
	return nil
}
func Settle(s *model.State, id string, elapsed int64, uncertain bool) error {
	l := s.Leases[id]
	if l == nil {
		return model.Err("CORE_INCONSISTENT", "missing outstanding lease %s", id)
	}
	if elapsed < 0 {
		return model.Err("CORE_INCONSISTENT", "negative elapsed time")
	}
	charge := min(elapsed, l.Amount)
	if uncertain {
		charge = l.Amount
	}
	s.Accounts[l.Anchor].Remaining += l.Amount - charge
	s.Tasks[l.TaskID].Resources.Charged += charge
	delete(s.Leases, id)
	return nil
}
func LCA(s *model.State, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", model.Err("PRECONDITION_FAILED", "empty participants")
	}
	first := s.Accounts[ids[0]]
	if first == nil {
		return "", model.Err("NOT_FOUND", "resource account")
	}
	chains := []map[string]bool{}
	for _, id := range ids {
		a := s.Accounts[id]
		if a == nil || a.TaskID != first.TaskID {
			return "", model.Err("PRECONDITION_FAILED", "participants must share Task")
		}
		seen := map[string]bool{}
		for id != "" {
			if seen[id] || s.Accounts[id] == nil {
				return "", model.Err("CORE_INCONSISTENT", "resource ancestry cycle/dangling parent")
			}
			seen[id] = true
			id = s.Accounts[id].Parent
		}
		chains = append(chains, seen)
	}
	for id := ids[0]; id != ""; id = s.Accounts[id].Parent {
		all := true
		for _, c := range chains {
			all = all && c[id]
		}
		if all {
			return id, nil
		}
	}
	return "", model.Err("CORE_INCONSISTENT", "no resource LCA")
}
func Refresh(s *model.State) error {
	for _, t := range s.Tasks {
		t.Resources.Remaining = 0
		t.Resources.Outstanding = 0
	}
	for id, a := range s.Accounts {
		t := s.Tasks[a.TaskID]
		if t == nil {
			return model.Err("CORE_INCONSISTENT", "account has missing task")
		}
		n, e := Add(t.Resources.Remaining, a.Remaining)
		if e != nil {
			return model.Err("CORE_INCONSISTENT", "invalid balances")
		}
		t.Resources.Remaining = n
		if o := s.Options[id]; o != nil {
			o.Remaining = a.Remaining
		}
	}
	for _, l := range s.Leases {
		t := s.Tasks[l.TaskID]
		if t == nil || l.Amount <= 0 || s.Accounts[l.Anchor] == nil || s.Accounts[l.Anchor].TaskID != l.TaskID {
			return model.Err("CORE_INCONSISTENT", "invalid lease anchor")
		}
		v, e := Add(t.Resources.Outstanding, l.Amount)
		if e != nil {
			return model.Err("CORE_INCONSISTENT", "outstanding overflow")
		}
		t.Resources.Outstanding = v
	}
	for _, t := range s.Tasks {
		r := t.Resources
		sum, e := Add(r.Remaining, r.Outstanding)
		if e == nil {
			sum, e = Add(sum, r.Charged)
		}
		if e == nil {
			sum, e = Add(sum, r.Retired)
		}
		if e != nil || sum != r.Minted || r.Minted <= 0 {
			return model.Err("CORE_INCONSISTENT", "resource conservation failed for %s", t.ID)
		}
		if t.Status == "CLOSED" && (r.Remaining != 0 || r.Outstanding != 0) {
			return model.Err("CORE_INCONSISTENT", "closed task has live resources")
		}
	}
	return nil
}

// Allocate fills a deficit along the existing ancestry, atomically in the caller transaction.
func Allocate(s *model.State, to string, n int64) error {
	a := s.Accounts[to]
	if a == nil || a.Parent == "" {
		return model.Err("PRECONDITION_FAILED", "allocation target must be an Option")
	}
	p := s.Accounts[a.Parent]
	if p.Remaining < n && p.Parent != "" {
		if e := Allocate(s, a.Parent, n-p.Remaining); e != nil {
			return e
		}
	}
	return Move(s, a.Parent, to, n)
}
