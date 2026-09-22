package core

import "scp-harness/internal/model"

func (c *Core) PreparePromotion(j *model.Journal) error {
	return c.Store.Update(func(s *model.State) error {
		if s.Cancellations[j.TaskID] {
			return model.Err("PRECONDITION_FAILED", "promotion canceled")
		}
		a := s.Artifacts[j.ArtifactID]
		if a == nil || a.TaskID != j.TaskID || a.BaseSHA != j.OldSHA || s.Changes[a.ChangeID] == nil || s.Changes[a.ChangeID].Stage != "PROMOTION" || s.Changes[a.ChangeID].State != "RUNNING" || !PromotionEvidence(s, s.Changes[a.ChangeID]) {
			return model.Err("PRECONDITION_FAILED", "promotion evidence or authority changed")
		}
		s.Journals[j.ID] = j
		s.Touch(j.TaskID)
		return nil
	})
}
func (c *Core) ApplyPromotion(id string) error {
	return c.Store.Update(func(s *model.State) error {
		j := s.Journals[id]
		if j == nil || j.State != "PREPARED" {
			return model.Err("CORE_INCONSISTENT", "promotion journal not PREPARED")
		}
		j.State = "APPLIED"
		ch := s.Changes[s.Artifacts[j.ArtifactID].ChangeID]
		if ch == nil {
			return model.Err("CORE_INCONSISTENT", "promotion Change missing")
		}
		ch.Set("PROMOTION", "DONE", "")
		s.Tasks[j.TaskID].SHA = j.NewSHA
		s.Audit(j.TaskID, "PROMOTED", j.OldSHA+" -> "+j.NewSHA)
		p := s.Pending[j.TaskID]
		if p != nil {
			finishActivity(s, p)
		} else {
			release(s)
		}
		s.Touch(j.TaskID)
		return nil
	})
}
