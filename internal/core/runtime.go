package core

import (
	"encoding/json"
	"os"
	"sort"

	"scp-harness/internal/config"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

func (c *Core) Anchor(s *model.State, p *model.Step) (string, error) {
	if p.Operation == "merge_judge" || p.Operation == "merge_synth" {
		return ledger.LCA(s, p.Participants)
	}
	if p.TargetType == "ARTIFACT" {
		a := s.Artifacts[p.TargetID]
		if a == nil {
			return "", model.Err("CORE_INCONSISTENT", "pending Artifact missing")
		}
		return a.Anchor, nil
	}
	if p.TargetType == "OPTION" {
		return p.TargetID, nil
	}
	return p.TaskID, nil
}
func (c *Core) runnable(s *model.State, p *model.Step) bool {
	t := s.Tasks[p.TaskID]
	if t == nil || t.Status != "ACTIVE" || s.Cancellations[p.TaskID] || s.FailStop() {
		return false
	}
	if p.OptionID != "" && (s.Options[p.OptionID] == nil || s.Options[p.OptionID].Status != "OPEN") {
		return false
	}
	profile := ""
	if p.Operation != "protected_test" && p.Operation != "promotion" {
		profile = c.Config.Profile(p.Operation).ID
	}
	if s.Blocked(p.TaskID, profile) {
		return false
	}
	anchor, e := c.Anchor(s, p)
	return e == nil && s.Accounts[anchor] != nil && s.Accounts[anchor].Remaining > 0
}

// Next persists the exact target before preparation. An unavailable next step
// keeps this record; it cannot be replaced with an authoritative-state mutation.
func (c *Core) Next(owner string) (*model.Step, error) {
	var next *model.Step
	e := c.Update(func(s *model.State) error {
		if s.Slot.State == "BUSY" && s.SlotOwner != owner {
			return model.Err("BLOCKED", "global execution slot is busy")
		}
		tasks := SortedTasks(s)
		if s.Slot.State == "BUSY" {
			p := s.Pending[s.SlotTask]
			if p != nil && c.runnable(s, p) {
				next = p
				return nil
			}
			release(s)
		}
		for _, t := range tasks {
			if p := s.Pending[t.ID]; p != nil && c.runnable(s, p) {
				next = p
				break
			}
		}
		if next == nil {
			for _, t := range tasks {
				if t.Status != "ACTIVE" || s.Cancellations[t.ID] || s.Pending[t.ID] != nil {
					continue
				}
				x := s.Exploration[t.ID]
				if x == nil || x.Done {
					continue
				}
				p := explorationStep(t.ID, x, c.Config.Exploration.N)
				if p == nil {
					x.Done = true
					s.Touch(t.ID)
					continue
				}
				s.Pending[t.ID] = p
				if c.runnable(s, p) {
					next = p
					break
				}
			}
		}
		if next == nil {
			opts := []*model.Option{}
			for _, o := range s.Options {
				opts = append(opts, o)
			}
			sort.Slice(opts, func(i, j int) bool {
				if opts[i].Created == opts[j].Created {
					return opts[i].ID < opts[j].ID
				}
				return opts[i].Created < opts[j].Created
			})
			rotated := map[string]bool{}
			for _, id := range s.RoundRobin {
				rotated[id] = true
			}
			ids := []string{}
			for _, o := range opts {
				if !rotated[o.ID] {
					ids = append(ids, o.ID)
				}
			}
			ids = append(ids, s.RoundRobin...)
			for _, id := range ids {
				o := s.Options[id]
				if o == nil || s.Pending[o.TaskID] != nil || !s.Exploration[o.TaskID].Done {
					continue
				}
				p := &model.Step{TaskID: o.TaskID, OptionID: id, Operation: "mutation", TargetType: "OPTION", TargetID: id}
				if c.runnable(s, p) {
					s.Pending[o.TaskID] = p
					next = p
					break
				}
			}
		}
		if next != nil {
			s.Slot = model.Slot{State: "BUSY"}
			s.SlotTask = next.TaskID
			s.SlotOwner = owner
			s.SlotPID = os.Getpid()
			s.Audit(next.TaskID, "SLOT_ACQUIRED", next.Operation)
		}
		return nil
	})
	return next, e
}
func explorationStep(task string, x *model.Exploration, n int) *model.Step {
	p := &model.Step{TaskID: task, TargetType: "TASK", TargetID: task}
	if x.Count < n {
		p.Operation = "option_generation"
		return p
	}
	if len(x.Batch) < 2 {
		return nil
	}
	if !x.Judged {
		p.Operation = "merge_judge"
		p.TargetType = "OPTION"
		p.TargetID = x.Batch[0]
		p.Participants = append([]string{}, x.Batch...)
		return p
	}
	for x.GroupIndex < len(x.Groups) && len(x.Groups[x.GroupIndex]) == 1 {
		x.GroupIndex++
	}
	if x.GroupIndex >= len(x.Groups) {
		return nil
	}
	p.Operation = "merge_synth"
	p.Participants = append([]string{}, x.Groups[x.GroupIndex]...)
	p.TargetType = "OPTION"
	p.TargetID = p.Participants[0]
	return p
}
func release(s *model.State) {
	s.Slot = model.Slot{State: "IDLE"}
	s.SlotTask = ""
	s.SlotOwner = ""
	s.SlotPID = 0
}
func finishChain(s *model.State, p *model.Step) {
	delete(s.Pending, p.TaskID)
	if p.OptionID != "" {
		q := []string{}
		for _, id := range s.RoundRobin {
			if id != p.OptionID {
				q = append(q, id)
			}
		}
		s.RoundRobin = append(q, p.OptionID)
	}
	release(s)
}
func (c *Core) Release(owner string) error {
	return c.Store.Update(func(s *model.State) error {
		if s.SlotOwner == owner {
			release(s)
		}
		return nil
	})
}
func (c *Core) PrepareAttempt(p model.Step, owner string) (*model.Attempt, error) {
	profile := c.Config.Profile(p.Operation)
	card := c.Config.Cards[profile.Card]
	if e := card.Require("process.execute"); e != nil {
		return nil, e
	}
	if profile.Workspace == "writable" {
		if e := card.Require("sandbox.write"); e != nil {
			return nil, e
		}
	}
	if p.Operation == "review" {
		if !card.Sees("test.result") {
			return nil, model.Err("CAPABILITY_DENIED", "review must be allowed to receive protected test result")
		}
	}
	if profile.Workspace != "none" && p.TargetType != "ARTIFACT" {
		if e := card.Require("repository.read"); e != nil {
			return nil, e
		}
	}
	var out *model.Attempt
	e := c.Update(func(s *model.State) error {
		if s.SlotOwner != owner || s.Slot.State != "BUSY" {
			return model.Err("BLOCKED", "execution slot not owned")
		}
		if !c.runnable(s, &p) {
			return model.Err("BLOCKED", "step no longer runnable")
		}
		if p.TargetType == "OPTION" && (s.Options[p.TargetID] == nil || s.Options[p.TargetID].Status != "OPEN") {
			return model.Err("INVALID_STATE", "Attempt input Option is CLOSED or missing")
		}
		for _, id := range p.Participants {
			if s.Options[id] == nil || s.Options[id].Status != "OPEN" {
				return model.Err("INVALID_STATE", "merge participant is CLOSED or missing")
			}
		}
		for _, a := range s.Attempts {
			if a.Active() {
				return model.Err("CORE_INCONSISTENT", "concurrent Attempt")
			}
		}
		anchor, e := c.Anchor(s, &p)
		if e != nil {
			return e
		}
		lease := min(s.Accounts[anchor].Remaining, profile.Timeout, card.LeaseMS())
		if lease <= 0 {
			return model.Err("INSUFFICIENT_RESOURCE", "no Attempt lease")
		}
		t := s.Tasks[p.TaskID]
		kind := "OPTION"
		if anchor == p.TaskID {
			kind = "TASK"
		}
		id := model.ID()
		out = &model.Attempt{ID: id, TaskID: t.ID, Operation: p.Operation, TargetType: p.TargetType, TargetID: p.TargetID, Actor: card.ID, Profile: profile.ID, AnchorType: kind, AnchorID: anchor, Lease: lease, SHA: t.SHA, Revision: t.Revision, Started: model.Now(), Status: "PREPARING"}
		if e = ledger.Reserve(s, id, anchor, lease); e != nil {
			return e
		}
		s.Attempts[id] = out
		s.Slot.Owner = &id
		s.Touch(t.ID)
		return nil
	})
	return out, e
}
func (c *Core) MarkRunning(id string) error {
	return c.Update(func(s *model.State) error {
		a := s.Attempts[id]
		if a == nil || a.Status != "PREPARING" {
			return model.Err("CORE_INCONSISTENT", "Attempt not PREPARING")
		}
		a.Status = "RUNNING"
		s.Touch(a.TaskID)
		return nil
	})
}

type Completion struct {
	AttemptID      string
	Step           model.Step
	Artifact       *model.Artifact
	Result         worker.Result
	Valid          bool
	Status, Reason string
	ExitCode       *int
	Stdout, Stderr string
	Elapsed        int64
	Uncertain      bool
	Visible        map[string]bool
}

func (c *Core) Complete(v Completion) error {
	var unlock func()
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	return c.Store.Update(func(s *model.State) error {
		a := s.Attempts[v.AttemptID]
		if a == nil || !a.Active() {
			return model.Err("CORE_INCONSISTENT", "completion of non-active Attempt")
		}
		t := s.Tasks[a.TaskID]
		card := CardFor(c.Config, a)
		if card.ID != a.Actor {
			return model.Err("CORE_INCONSISTENT", "Attempt actor/card binding changed")
		}
		if a.Operation == "review" && !card.Has("review.decide") || a.Operation == "option_generation" && !card.Has("option.propose") || a.Operation == "merge_synth" && !card.Has("option.merge") {
			v.Valid = false
		}
		if v.Artifact != nil {
			s.Artifacts[v.Artifact.ID] = v.Artifact
			a.ArtifactID = &v.Artifact.ID
		}
		a.Stdout = v.Stdout
		a.Stderr = v.Stderr
		a.Status = v.Status
		a.ExitCode = v.ExitCode
		now := model.Now()
		a.Ended = &now
		if v.Reason != "" {
			a.Reason = &v.Reason
		}
		if e := ledger.Settle(s, a.ID, v.Elapsed, v.Uncertain); e != nil {
			return e
		}
		s.Slot.Owner = nil
		if s.Interrupts[a.ID] || s.Cancellations[a.TaskID] {
			a.Status = "INTERRUPTED"
			v.Valid = false
			delete(s.Interrupts, a.ID)
		}
		if a.Status == "TERMINATED" && model.Exit(v.Reason) >= 4 {
			if e := c.block(s, v.Reason, a.TaskID, a.Profile, "Attempt "+a.ID+": "+v.Reason); e != nil {
				return e
			}
			release(s)
			s.Touch(a.TaskID)
			return nil
		}
		ids := []string{}
		if v.Valid && a.Status == "RETURNED" {
			var controlErr error
			for _, request := range v.Result.Claims {
				if !card.Has("claim.publish") || !v.Visible[request.SubjectType+":"+request.SubjectID] {
					s.Audit(t.ID, "CAPABILITY_DENIED", "worker Claim denied")
					continue
				}
				if qualifyingClaim(card, request.SubjectType, request.Type) {
					if unlock == nil && controlErr == nil {
						unlock, controlErr = c.LockTaskControl(t.ID)
					}
					if controlErr != nil {
						// Settlement/capture must finish while a controller holds
						// its gate. Only the competing governance request fails.
						s.Audit(t.ID, "BLOCKED", "worker completion Claim: "+controlErr.Error())
						continue
					}
				}
				if _, e := addClaim(s, t, card, request.SubjectType, request.SubjectID, request.Type, request.Payload, a.SHA, a.Revision); e != nil {
					if model.Exit(model.Code(e)) == 5 {
						return e
					}
					s.Audit(t.ID, model.Code(e), e.Error())
				}
			}
			if t.Status != "CLOSED" {
				for _, request := range v.Result.Options {
					if !card.Has("option.propose") {
						s.Audit(t.ID, "CAPABILITY_DENIED", "worker Option denied")
						continue
					}
					parent := a.TaskID
					parents := []string{}
					if v.Step.OptionID != "" {
						parent = v.Step.OptionID
						parents = []string{parent}
						if s.Options[parent].Status != "OPEN" {
							s.Audit(t.ID, "INVALID_STATE", "closed semantic anchor")
							continue
						}
					}
					o := createOption(s, t, request.Text, parent, card.ID, a.SHA, a.Revision, parents, "propose")
					ids = append(ids, o.ID)
				}
			}
		} else {
			s.Audit(t.ID, "INVALID_OUTPUT", "no semantic worker requests applied")
		}
		if t.Status != "ACTIVE" || s.Cancellations[t.ID] || v.Step.OptionID != "" && s.Options[v.Step.OptionID].Status != "OPEN" {
			finishChain(s, &v.Step)
			s.Touch(t.ID)
			return nil
		}
		if a.Operation == "option_generation" || a.Operation == "merge_judge" || a.Operation == "merge_synth" {
			x := s.Exploration[t.ID]
			switch a.Operation {
			case "option_generation":
				x.Count++
				x.Batch = append(x.Batch, ids...)
			case "merge_judge":
				x.Judged = true
				if v.Valid && a.Status == "RETURNED" && worker.Partition(v.Result.Groups, v.Step.Participants) == nil {
					x.Groups = v.Result.Groups
				} else {
					x.Groups = [][]string{}
					s.Audit(t.ID, "INVALID_OUTPUT", "invalid merge partition")
				}
			case "merge_synth":
				for _, id := range v.Step.Participants {
					if s.Options[id] == nil || s.Options[id].Status != "OPEN" {
						v.Valid = false
						s.Audit(t.ID, "INVALID_STATE", "merge participant closed during Attempt")
					}
				}
				if v.Valid && a.Status == "RETURNED" && card.Has("option.merge") {
					parent, e := ledger.LCA(s, v.Step.Participants)
					if e != nil {
						return e
					}
					createOption(s, t, v.Result.Text, parent, card.ID, a.SHA, a.Revision, v.Step.Participants, "merge")
				}
				x.GroupIndex++
			}
			p := explorationStep(t.ID, x, c.Config.Exploration.N)
			if p == nil {
				x.Done = true
				finishChain(s, &v.Step)
			} else {
				s.Pending[t.ID] = p
			}
		} else if a.Status != "RETURNED" && !(a.Operation == "review" && (a.Status == "CRASHED" || a.Status == "TIMED_OUT")) {
			finishChain(s, &v.Step)
		} else if a.Operation == "mutation" {
			if !v.Valid || v.Artifact == nil || v.Result.Disposition == "DROP_FINAL" {
				finishChain(s, &v.Step)
			} else {
				p := v.Step
				p.TargetType = "ARTIFACT"
				p.TargetID = v.Artifact.ID
				p.Operation = "mutation"
				if v.Result.Disposition == "PROMOTE_FINAL" {
					p.Operation = "protected_test"
				}
				s.Pending[t.ID] = &p
			}
		} else if a.Operation == "review" {
			verdict := "REJECT"
			findings := []string{}
			if v.Valid && a.Status == "RETURNED" && card.Has("review.decide") {
				verdict = v.Result.Verdict
				findings = v.Result.Findings
			}
			s.Reviews[v.Step.TargetID] = &model.Review{ArtifactID: v.Step.TargetID, AttemptID: a.ID, Verdict: verdict, Findings: findings, Created: now}
			for _, finding := range findings {
				payload, _ := json.Marshal(map[string]string{"finding": finding})
				claim := &model.Claim{ID: model.ID(), TaskID: t.ID, SubjectType: "ARTIFACT", SubjectID: v.Step.TargetID, Type: "review.finding", Payload: payload, Issuer: card.ID, SHA: a.SHA, Revision: a.Revision, Created: now}
				s.Claims[claim.ID] = claim
			}
			p := v.Step
			p.Operation = "mutation"
			if verdict == "APPROVE" && s.Tests[p.TargetID] != nil && s.Tests[p.TargetID].Outcome == "PASS" {
				p.Operation = "promotion"
			} else if verdict == "APPROVE" {
				s.Audit(t.ID, "PROTECTED_TEST_FAILED", p.TargetID)
			}
			s.Pending[t.ID] = &p
		}
		s.Touch(t.ID)
		return nil
	})
}
func (c *Core) EndPromotion(p model.Step, sha, reason string) error {
	return c.Store.Update(func(s *model.State) error {
		if sha != "" {
			s.Tasks[p.TaskID].SHA = sha
		}
		s.Audit(p.TaskID, reason, p.TargetID)
		finishChain(s, &p)
		s.Touch(p.TaskID)
		return nil
	})
}
func (c *Core) FinishTest(p model.Step, leaseID string, result model.TestResult, elapsed int64, uncertain bool) error {
	return c.Store.Update(func(s *model.State) error {
		if e := ledger.Settle(s, leaseID, elapsed, uncertain); e != nil {
			return e
		}
		s.Tests[p.TargetID] = &result
		if s.Cancellations[p.TaskID] {
			finishChain(s, &p)
		} else {
			p.Operation = "review"
			s.Pending[p.TaskID] = &p
		}
		s.Touch(p.TaskID)
		return nil
	})
}
func (c *Core) TestLease(p model.Step, id string) (int64, error) {
	var lease int64
	e := c.Update(func(s *model.State) error {
		if !c.runnable(s, &p) {
			return model.Err("BLOCKED", "protected test no longer runnable")
		}
		anchor, e := c.Anchor(s, &p)
		if e != nil {
			return e
		}
		lease = min(s.Accounts[anchor].Remaining, c.Config.Test.Timeout)
		if e = ledger.Reserve(s, id, anchor, lease); e != nil {
			return e
		}
		s.Touch(p.TaskID)
		return nil
	})
	return lease, e
}
func CardFor(c *config.Config, a *model.Attempt) config.Card {
	for _, p := range c.Workers {
		if p.ID == a.Profile {
			return c.Cards[p.Card]
		}
	}
	panic("validated profile disappeared")
}
