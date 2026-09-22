package core

import (
	"path/filepath"
	"sort"

	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
)

// QueueCI authorizes machine verification only. It creates no agent Attempt.
func (c *Core) QueueCI(artifact string) (*model.CIRun, error) {
	if e := c.Operator().Require("ci.run"); e != nil {
		return nil, e
	}
	var result *model.CIRun
	e := c.Update(func(s *model.State) error {
		a := s.Artifacts[artifact]
		if a == nil {
			return model.Err("NOT_FOUND", "Artifact")
		}
		if s.Tasks[a.TaskID].Status != "ACTIVE" || s.Cancellations[a.TaskID] {
			return model.Err("INVALID_STATE", "Task must be ACTIVE")
		}
		result = &model.CIRun{ID: model.ID(), ArtifactID: artifact, Status: "QUEUED", Created: model.Now()}
		s.CIRuns[result.ID] = result
		s.Requests = append(s.Requests, &model.Step{RequestID: model.ID(), CIRunID: result.ID, TaskID: a.TaskID, Operation: "ci", TargetType: "ARTIFACT", TargetID: artifact})
		return nil
	})
	return result, e
}

func (c *Core) PrepareCI(p model.Step, owner string) (*model.CIRun, error) {
	var result *model.CIRun
	e := c.Update(func(s *model.State) error {
		if s.SlotOwner != owner || s.Slot.State != "BUSY" || !c.runnable(s, &p) {
			return model.Err("BLOCKED", "CI activity not admitted")
		}
		anchor, e := c.Anchor(s, &p)
		if e != nil {
			return e
		}
		id := p.CIRunID
		if id == "" {
			id = model.ID()
		}
		r := s.CIRuns[id]
		if r == nil {
			r = &model.CIRun{ID: id, ChangeID: p.ChangeID, ArtifactID: p.TargetID, Created: model.Now()}
			s.CIRuns[id] = r
		} else if r.Status != "QUEUED" {
			return model.Err("INVALID_STATE", "CI Run already started")
		}
		r.Status = "PREPARING"
		r.Started = model.Now()
		r.Timeout = c.Config.Test.Timeout
		r.Command = append([]string{}, c.Config.Test.Command...)
		r.ConfigHash = c.Config.Hash()
		r.Lease = min(s.Accounts[anchor].Remaining, r.Timeout)
		logs := filepath.Join(filepath.Dir(c.Config.Database), "runtime", id, "logs")
		r.Stdout, r.Stderr = filepath.Join(logs, "stdout.log"), filepath.Join(logs, "stderr.log")
		if e = ledger.Reserve(s, id, anchor, r.Lease); e != nil {
			return e
		}
		if ch := s.Changes[p.ChangeID]; ch != nil {
			ch.CIRunID = id
			ch.ReviewAttemptID = ""
		}
		s.Slot.Owner = &id
		result = r
		return nil
	})
	return result, e
}

func (c *Core) FinishCI(p model.Step, result model.CIRun, uncertain bool) error {
	return c.Store.Update(func(s *model.State) error {
		r := s.CIRuns[result.ID]
		if r == nil || !r.Active() {
			return model.Err("CORE_INCONSISTENT", "CI Run not active")
		}
		if e := ledger.Settle(s, r.ID, result.Elapsed, uncertain); e != nil {
			return e
		}
		*r = result
		r.Ended = model.Now()
		if ch := s.Changes[p.ChangeID]; ch != nil {
			if ch.State == "PAUSED" || ch.State == "ABORTED" || s.Cancellations[p.TaskID] || r.Status == "TERMINATED" {
				if ch.State != "ABORTED" {
					ch.Set(ch.Stage, "PAUSED", "")
				}
			} else {
				runs := []*model.CIRun{}
				for _, prior := range s.CIRuns {
					if prior.ChangeID == ch.ID && prior.Ended != "" && prior.Started >= ch.TimeoutResetAt {
						runs = append(runs, prior)
					}
				}
				sort.Slice(runs, func(i, j int) bool { return runs[i].Started > runs[j].Started })
				streak := 0
				for _, prior := range runs {
					if prior.Status != "TIMEOUT" {
						break
					}
					streak++
				}
				ch.TimeoutStreak = streak
				if streak >= 2 {
					ch.Set("CI", "BLOCKED", "REPEATED_CI_TIMEOUT")
				} else {
					ch.Set("REVIEW", "QUEUED", "")
				}
			}
		}
		finishActivity(s, &p)
		s.Touch(p.TaskID)
		return nil
	})
}
