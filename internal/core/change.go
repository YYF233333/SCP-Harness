package core

import (
	"context"
	"strings"
	"time"

	"scp-harness/internal/model"
)

// ChangeStep is derived from durable lifecycle state, never the reverse.
func ChangeStep(ch *model.Change) *model.Step {
	p := &model.Step{ChangeID: ch.ID, TaskID: ch.TaskID, OptionID: ch.OptionID, TargetType: "ARTIFACT", TargetID: ch.ArtifactID}
	switch ch.Stage {
	case "MUTATION":
		p.Operation = "mutation"
		if ch.ArtifactID == "" {
			p.TargetType, p.TargetID = "OPTION", ch.OptionID
		}
	case "CI":
		p.Operation = "ci"
	case "REVIEW":
		p.Operation = "review"
	case "PROMOTION":
		p.Operation = "promotion"
	default:
		return nil
	}
	return p
}

// PromotionEvidence binds approval to the latest completed verification of this Artifact.
func PromotionEvidence(s *model.State, ch *model.Change) bool {
	for _, run := range s.CIRuns {
		if run.ArtifactID == ch.ArtifactID && (run.Active() || run.Status == "QUEUED") {
			return false
		}
	}
	a, ci, review := s.Artifacts[ch.ArtifactID], s.CIRuns[ch.CIRunID], s.Reviews[ch.ArtifactID]
	return a != nil && s.LatestCI(a.ID) != nil && s.LatestCI(a.ID).ID == ch.CIRunID && a.ChangeID == ch.ID && a.BaseSHA == ch.BaseSHA && ci != nil && ci.ArtifactID == a.ID && ci.Status == "PASS" && review != nil && review.AttemptID == ch.ReviewAttemptID && review.CIRunID == ci.ID && review.Verdict == "APPROVE"
}

func (c *Core) ChangeAction(ctx context.Context, id, action string) (*model.Change, error) {
	capability := "change." + action
	if e := c.Operator().Require(capability); e != nil {
		return nil, e
	}
	var result *model.Change
	snapshot, e := c.Read()
	if e != nil {
		return nil, e
	}
	ch := snapshot.Changes[id]
	if ch == nil {
		return nil, model.Err("NOT_FOUND", "Change")
	}
	unlock, e := c.LockTaskControl(ch.TaskID)
	if e != nil {
		return nil, e
	}
	defer unlock()
	e = c.Update(func(s *model.State) error {
		ch := s.Changes[id]
		if ch == nil {
			return model.Err("NOT_FOUND", "Change %s", id)
		}
		if ch.Terminal() {
			return model.Err("INVALID_STATE", "Change is terminal")
		}
		if action != "pause" && action != "abort" && s.Tasks[ch.TaskID].Status != "ACTIVE" {
			return model.Err("INVALID_STATE", "Task must be ACTIVE")
		}
		if s.Cancellations[ch.TaskID] {
			return model.Err("BLOCKED", "Task control in progress")
		}
		if action != "pause" && action != "abort" && changeActive(s, id) {
			return model.Err("BLOCKED", "Change activity has not settled")
		}
		switch action {
		case "pause", "abort":
			state := "PAUSED"
			if action == "abort" {
				state = "ABORTED"
			}
			ch.Set(ch.Stage, state, "")
			for _, a := range s.Attempts {
				if a.ChangeID == id && a.Active() {
					s.Interrupts[a.ID] = true
				}
			}
		case "promote":
			if ch.Stage != "AWAIT_PROMOTION" || ch.State != "QUEUED" || !PromotionEvidence(s, ch) {
				return model.Err("PRECONDITION_FAILED", "promotion requires awaiting, passing CI and current approved review")
			}
			ch.Set("PROMOTION", "QUEUED", "")
		case "resume", "retry-ci", "retry-review", "rework":
			if ch.State == "STALE" {
				return model.Err("INVALID_STATE", "STALE Change cannot be transplanted")
			}
			if action == "resume" && ch.State != "PAUSED" && ch.State != "BLOCKED" {
				return model.Err("INVALID_STATE", "resume requires PAUSED or BLOCKED")
			}
			if action != "resume" {
				a := s.Artifacts[ch.ArtifactID]
				if a == nil || a.ChangeID != id {
					return model.Err("PRECONDITION_FAILED", "current Artifact must belong to Change")
				}
				stage := map[string]string{"retry-ci": "CI", "retry-review": "REVIEW", "rework": "MUTATION"}[action]
				if stage == "REVIEW" {
					ci := s.LatestCI(a.ID)
					if ci == nil {
						return model.Err("PRECONDITION_FAILED", "review requires CI evidence")
					}
					ch.CIRunID = ci.ID
				}
				ch.Stage = stage
				ch.ReviewAttemptID = ""
			}
			// Keep historical CIRuns and streak. The timestamp starts a new explicit recovery epoch.
			ch.TimeoutResetAt = model.Now()
			ch.Set(ch.Stage, "QUEUED", "")
		default:
			return model.Err("USAGE_ERROR", "unknown Change action")
		}
		s.Audit(ch.TaskID, "CHANGE_"+strings.ToUpper(action), ch.ID)
		s.Touch(ch.TaskID)
		result = ch
		return nil
	})
	if e != nil {
		return nil, e
	}
	if action == "pause" || action == "abort" {
		deadline := time.NewTimer(time.Duration(c.Config.Limits.ProcessMS) * time.Millisecond)
		defer deadline.Stop()
		for {
			s, e := c.Read()
			if e != nil {
				return nil, e
			}
			if !changeActive(s, id) {
				return s.Changes[id], nil
			}
			select {
			case <-ctx.Done():
				return nil, model.Err("BLOCKED", "waiting for Change settlement interrupted")
			case <-deadline.C:
				return nil, model.Err("BLOCKED", "activity owner did not settle; recover after owner exits")
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	return result, nil
}

func changeActive(s *model.State, id string) bool {
	for _, a := range s.Attempts {
		if a.ChangeID == id && a.Active() {
			return true
		}
	}
	for _, r := range s.CIRuns {
		if r.ChangeID == id && r.Active() {
			return true
		}
	}
	if p := s.Pending[s.SlotTask]; s.Slot.State == "BUSY" && p != nil && p.ChangeID == id {
		return true
	}
	return false
}

func (c *Core) ReviseTask(id, objective string) (*model.Task, error) {
	if e := c.Operator().Require("task.revise"); e != nil {
		return nil, e
	}
	if strings.TrimSpace(objective) == "" {
		return nil, model.Err("USAGE_ERROR", "objective must not be empty")
	}
	var result *model.Task
	e := c.Update(func(s *model.State) error {
		t, e := task(s, id)
		if e != nil {
			return e
		}
		if t.Status == "CLOSED" {
			return model.Err("INVALID_STATE", "Task is CLOSED")
		}
		t.ObjectiveRevision++
		s.ObjectiveRevisions = append(s.ObjectiveRevisions, model.ObjectiveRevision{TaskID: id, Revision: t.ObjectiveRevision, Old: t.Objective, New: objective, Operator: c.Operator().ID, Created: model.Now()})
		t.Objective = objective
		s.Touch(id)
		result = t
		return nil
	})
	return result, e
}
