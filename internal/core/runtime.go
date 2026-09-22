package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"scp-harness/internal/config"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

func (c *Core) Anchor(s *model.State, p *model.Step) (string, error) {
	if p.Operation == "discussion" {
		return p.TaskID, nil
	}
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
	if ch := s.Changes[p.ChangeID]; ch != nil && ch.State != "QUEUED" && ch.State != "RUNNING" {
		return false
	}
	if t == nil || t.Status != "ACTIVE" || s.Cancellations[p.TaskID] || s.FailStop() {
		return false
	}
	if p.OptionID != "" && (s.Options[p.OptionID] == nil || s.Options[p.OptionID].Status != "OPEN" && (p.ChangeID == "" && p.Operation != "discussion" || s.Options[p.OptionID].CloseReason != "SUPERSEDED")) {
		return false
	}
	profile, runner := "", ""
	if p.Operation == "ci" {
		runner = c.Config.WSL.TestDistro
	} else if p.Operation != "promotion" {
		profile = c.Config.Profile(p.Operation).ID
		runner = c.Config.WSL.Distro
	}
	if s.Blocked(p.TaskID, profile, runner) {
		return false
	}
	anchor, e := c.Anchor(s, p)
	return e == nil && s.Accounts[anchor] != nil && s.Accounts[anchor].Remaining > 0
}

// Next selects one activity. Change owns lifecycle; Pending only records the selected activity.
func (c *Core) Next(owner string) (*model.Step, error) {
	var next *model.Step
	e := c.Update(func(s *model.State) error {
		if s.Slot.State != "IDLE" {
			return model.Err("BLOCKED", "global execution slot is busy")
		}
		for _, p := range s.Requests {
			if c.runnable(s, p) {
				next = p
				break
			}
		}
		changes := []*model.Change{}
		for _, ch := range s.Changes {
			if ch.State == "QUEUED" && ch.Stage != "AWAIT_PROMOTION" {
				changes = append(changes, ch)
			}
		}
		sort.Slice(changes, func(i, j int) bool {
			a, b := changes[i], changes[j]
			if a.Started != b.Started {
				return a.Started
			}
			if a.Created == b.Created {
				return a.ID < b.ID
			}
			return a.Created < b.Created
		})
		if next == nil {
			for _, ch := range changes {
				if ch.BaseSHA != s.Tasks[ch.TaskID].SHA {
					ch.Set(ch.Stage, "STALE", "BASE_CHANGED")
					continue
				}
				p := ChangeStep(ch)
				if p != nil && c.runnable(s, p) {
					next = p
					ch.Started = true
					ch.Set(ch.Stage, "RUNNING", "")
					break
				}
				if p != nil {
					anchor, e := c.Anchor(s, p)
					if e == nil && s.Accounts[anchor].Remaining <= 0 {
						ch.Set(ch.Stage, "BLOCKED", "INSUFFICIENT_RESOURCE")
					}
				}
			}
		}
		if next == nil {
			for _, t := range SortedTasks(s) {
				if t.Status != "ACTIVE" || s.Cancellations[t.ID] {
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
				if c.runnable(s, p) {
					next = p
					break
				}
			}
		}
		if next != nil {
			s.Pending[next.TaskID] = next
			s.Slot = model.Slot{State: "BUSY", Kind: next.Operation}
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
	if p := s.Pending[s.SlotTask]; p != nil {
		if ch := s.Changes[p.ChangeID]; ch != nil && ch.State == "RUNNING" {
			ch.Set(ch.Stage, "QUEUED", ch.Reason)
		}
		delete(s.Pending, s.SlotTask)
	}
	s.Slot = model.Slot{State: "IDLE"}
	s.SlotTask = ""
	s.SlotOwner = ""
	s.SlotPID = 0
}
func finishActivity(s *model.State, p *model.Step) {
	if p.RequestID != "" {
		for i, request := range s.Requests {
			if request.RequestID == p.RequestID {
				s.Requests = append(s.Requests[:i], s.Requests[i+1:]...)
				break
			}
		}
	}
	delete(s.Pending, p.TaskID)
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
	if p.Operation == "mutation" && p.TargetType == "ARTIFACT" && (!card.Sees("ci.result") || !card.Sees("review.findings")) {
		return nil, model.Err("CAPABILITY_DENIED", "rework requires CI evidence and review findings context")
	}
	if p.Operation == "review" {
		if !card.Sees("ci.result") {
			return nil, model.Err("CAPABILITY_DENIED", "review must be allowed to receive CI evidence")
		}
	}
	if profile.Workspace != "none" && p.TargetType != "ARTIFACT" {
		if e := card.Require("repository.read"); e != nil {
			return nil, e
		}
	}
	var out *model.Attempt
	e := c.Update(func(s *model.State) error {
		if (p.Operation == "mutation" || p.Operation == "review") && (p.ChangeID == "" || s.Changes[p.ChangeID] == nil) {
			return model.Err("PRECONDITION_FAILED", "agent development requires Change authority")
		}
		if p.ChangeID != "" {
			expected := ChangeStep(s.Changes[p.ChangeID])
			if expected == nil || expected.Operation != p.Operation || expected.TargetID != p.TargetID {
				return model.Err("PRECONDITION_FAILED", "Change stage/target changed")
			}
		}
		if s.SlotOwner != owner || s.Slot.State != "BUSY" {
			return model.Err("BLOCKED", "execution slot not owned")
		}
		if !c.runnable(s, &p) {
			return model.Err("BLOCKED", "step no longer runnable")
		}
		if p.TargetType == "OPTION" && (s.Options[p.TargetID] == nil || s.Options[p.TargetID].Status != "OPEN" && (p.ChangeID == "" && p.Operation != "discussion" || s.Options[p.TargetID].CloseReason != "SUPERSEDED")) {
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
		objective := t.Objective
		if ch := s.Changes[p.ChangeID]; ch != nil {
			objective = ch.Objective
		}
		out = &model.Attempt{Objective: objective, ChangeID: p.ChangeID, ID: id, TaskID: t.ID, Operation: p.Operation, TargetType: p.TargetType, TargetID: p.TargetID, Actor: card.ID, Profile: profile.ID, AnchorType: kind, AnchorID: anchor, Lease: lease, SHA: t.SHA, Revision: t.Revision, Started: model.Now(), Status: "PREPARING"}
		logs := filepath.Join(filepath.Dir(c.Config.Database), "runtime", id+".logs")
		out.Stdout, out.Stderr = filepath.Join(logs, "stdout.log"), filepath.Join(logs, "stderr.log")
		if e = ledger.Reserve(s, id, anchor, lease); e != nil {
			return e
		}
		s.Attempts[id] = out
		if ch := s.Changes[p.ChangeID]; ch != nil && p.Operation == "review" {
			ch.ReviewAttemptID = id
		}
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
		if v.Artifact != nil && a.Operation == "mutation" {
			v.Artifact.ChangeID = a.ChangeID
			s.Artifacts[v.Artifact.ID] = v.Artifact
			if ch := s.Changes[a.ChangeID]; ch != nil {
				ch.ArtifactID = v.Artifact.ID
				ch.CIRunID = ""
				ch.ReviewAttemptID = ""
			}
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
		ch := s.Changes[a.ChangeID]
		if s.Interrupts[a.ID] || s.Cancellations[a.TaskID] {
			a.Status = "INTERRUPTED"
			v.Valid = false
			delete(s.Interrupts, a.ID)
		}
		if ch != nil && (s.Interrupts[a.ID] || s.Cancellations[a.TaskID] || a.Status == "INTERRUPTED" || ch.State == "PAUSED" || ch.State == "ABORTED") {
			if ch.State != "ABORTED" {
				ch.Set(ch.Stage, "PAUSED", "")
			}
			finishActivity(s, &v.Step)
			s.Touch(a.TaskID)
			return nil
		}
		if a.Status == "TERMINATED" && model.Exit(v.Reason) >= 4 {
			if e := c.block(s, v.Reason, a.TaskID, a.Profile, c.Config.WSL.Distro, "Attempt "+a.ID+": "+v.Reason); e != nil {
				return e
			}
			release(s)
			s.Touch(a.TaskID)
			return nil
		}
		if a.Operation == "discussion" {
			if v.Valid && a.Status == "RETURNED" && card.Has("claim.publish") {
				payload, _ := json.Marshal(discussionText{Text: v.Result.Text})
				if _, e := addClaim(s, t, card, "OPTION", a.TargetID, "discussion.reply", payload, a.SHA, a.Revision); e != nil {
					return e
				}
			} else {
				s.Audit(t.ID, "INVALID_OUTPUT", "discussion ended without reply")
			}
			finishActivity(s, &v.Step)
			s.Touch(t.ID)
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
		if t.Status != "ACTIVE" || s.Cancellations[t.ID] || v.Step.OptionID != "" && s.Options[v.Step.OptionID].Status != "OPEN" && s.Options[v.Step.OptionID].CloseReason != "SUPERSEDED" {
			if ch != nil && !ch.Terminal() {
				ch.Set(ch.Stage, "PAUSED", "OPTION_OR_TASK_CLOSED")
			}
			finishActivity(s, &v.Step)
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
					for _, id := range v.Step.Participants {
						s.Options[id].Status = "CLOSED"
						s.Options[id].CloseReason = "SUPERSEDED"
					}
				}
				x.GroupIndex++
			}
			p := explorationStep(t.ID, x, c.Config.Exploration.N)
			if p == nil {
				x.Done = true
				finishActivity(s, &v.Step)
			}
		} else if ch != nil {
			if a.Status != "RETURNED" || !v.Valid {
				reason := "AGENT_" + a.Status
				if a.Status == "RETURNED" {
					reason = "INVALID_AGENT_OUTPUT"
				}
				ch.Set(ch.Stage, "BLOCKED", reason)
			} else if a.Operation == "mutation" {
				if v.Artifact == nil || v.Result.Disposition == "DROP_FINAL" {
					ch.Set("MUTATION", "BLOCKED", "NO_SUBMITTED_RESULT")
				} else {
					stage := "MUTATION"
					if v.Result.Disposition == "PROMOTE_FINAL" {
						stage = "CI"
					}
					ch.Set(stage, "QUEUED", "")
				}
			} else if a.Operation == "review" {
				verdict := v.Result.Verdict
				s.Reviews[v.Step.TargetID] = &model.Review{ArtifactID: v.Step.TargetID, AttemptID: a.ID, CIRunID: ch.CIRunID, Verdict: verdict, Findings: v.Result.Findings, Created: now}
				ch.ReviewAttemptID = a.ID
				for _, finding := range v.Result.Findings {
					payload, _ := json.Marshal(map[string]string{"finding": finding})
					claim := &model.Claim{ID: model.ID(), TaskID: t.ID, SubjectType: "ARTIFACT", SubjectID: v.Step.TargetID, Type: "review.finding", Payload: payload, Issuer: card.ID, SHA: a.SHA, Revision: a.Revision, Created: now}
					s.Claims[claim.ID] = claim
				}
				if PromotionEvidence(s, ch) {
					ch.Set("AWAIT_PROMOTION", "QUEUED", "")
					s.Audit(t.ID, "CHANGE_AWAIT_PROMOTION", ch.ID)
				} else if verdict == "REJECT" && len(v.Result.Findings) > 0 {
					ch.Set("MUTATION", "QUEUED", "")
				} else {
					ch.Set("REVIEW", "BLOCKED", "REVIEW_REQUIRES_ATTENTION")
				}
			}
		}
		finishActivity(s, &v.Step)
		s.Touch(t.ID)
		return nil
	})
}
func (c *Core) EndPromotion(p model.Step, sha, reason string) error {
	return c.Store.Update(func(s *model.State) error {
		if sha != "" {
			s.Tasks[p.TaskID].SHA = sha
		}
		if ch := s.Changes[p.ChangeID]; ch != nil {
			ch.Set(ch.Stage, "STALE", reason)
		}
		s.Audit(p.TaskID, reason, p.ChangeID)
		finishActivity(s, &p)
		s.Touch(p.TaskID)
		return nil
	})
}
func CardFor(c *config.Config, a *model.Attempt) config.Card {
	for _, p := range c.Workers {
		if p.ID == a.Profile {
			return c.Cards[p.Card]
		}
	}
	panic("validated profile disappeared")
}
