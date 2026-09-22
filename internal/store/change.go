package store

import (
	"reflect"
	"slices"

	"scp-harness/internal/model"
)

func validateChanges(s *model.State) error {
	bad := func(message string) error { return model.Err("CORE_INCONSISTENT", "%s", message) }
	options := map[string]bool{}
	for id, ch := range s.Changes {
		if ch == nil || ch.ID != id || s.Tasks[ch.TaskID] == nil || s.Options[ch.OptionID] == nil || s.Options[ch.OptionID].TaskID != ch.TaskID || ch.BaseSHA == "" {
			return bad("invalid Change identity")
		}
		if !slices.Contains([]string{"MUTATION", "CI", "REVIEW", "AWAIT_PROMOTION", "PROMOTION"}, ch.Stage) || !slices.Contains([]string{"QUEUED", "RUNNING", "PAUSED", "BLOCKED", "STALE", "DONE", "ABORTED"}, ch.State) {
			return bad("invalid Change stage/state")
		}
		if !ch.Terminal() {
			if options[ch.OptionID] {
				return bad("duplicate nonterminal Change")
			}
			options[ch.OptionID] = true
		}
		if ch.ArtifactID != "" {
			a := s.Artifacts[ch.ArtifactID]
			if a == nil || a.ChangeID != id || a.BaseSHA != ch.BaseSHA {
				return bad("Change Artifact ownership mismatch")
			}
		}
		if ch.CIRunID != "" {
			r := s.CIRuns[ch.CIRunID]
			if r == nil || r.ArtifactID != ch.ArtifactID {
				return bad("Change CI ownership mismatch")
			}
		}
		if ch.ReviewAttemptID != "" {
			a := s.Attempts[ch.ReviewAttemptID]
			if a == nil || a.ChangeID != id || a.Operation != "review" || a.TargetID != ch.ArtifactID {
				return bad("Change review ownership mismatch")
			}
		}
		if ch.Stage != "MUTATION" && ch.ArtifactID == "" {
			return bad("Change stage requires Artifact")
		}
	}
	active := 0
	for id, r := range s.CIRuns {
		if r == nil || r.ID != id || s.Artifacts[r.ArtifactID] == nil || !slices.Contains([]string{"QUEUED", "PREPARING", "RUNNING", "PASS", "FAIL", "TIMEOUT", "TERMINATED"}, r.Status) {
			return bad("invalid CI Run")
		}
		if r.ChangeID != "" && (s.Changes[r.ChangeID] == nil || s.Artifacts[r.ArtifactID].ChangeID != r.ChangeID) {
			return bad("CI Change ownership mismatch")
		}
		if r.Active() {
			active++
			if s.Leases[id] == nil {
				return bad("CI missing lease")
			}
		} else if s.Leases[id] != nil {
			return bad("terminal CI with lease")
		}
	}
	for _, a := range s.Attempts {
		if (a.Operation == "mutation" || a.Operation == "review") && a.ChangeID == "" {
			return bad("development Attempt without Change")
		}
		if a.ChangeID != "" && (s.Changes[a.ChangeID] == nil || s.Changes[a.ChangeID].TaskID != a.TaskID) {
			return bad("Attempt Change ownership mismatch")
		}
		if a.Active() {
			active++
		}
	}
	if active > 1 {
		return bad("concurrent execution activities")
	}
	for _, a := range s.Artifacts {
		if a.ChangeID == "" || s.Changes[a.ChangeID] == nil || s.Attempts[a.AttemptID].ChangeID != a.ChangeID {
			return bad("Artifact Change ownership mismatch")
		}
	}
	return nil
}

func immutableChanges(before, after *model.State) error {
	bad := func() error { return model.Err("CORE_INCONSISTENT", "immutable Change/CI/objective history altered") }
	for id, old := range before.Changes {
		n := after.Changes[id]
		if n == nil || old.ID != n.ID || old.TaskID != n.TaskID || old.OptionID != n.OptionID || old.BaseSHA != n.BaseSHA || old.Objective != n.Objective || old.ObjectiveRevision != n.ObjectiveRevision || old.Created != n.Created {
			return bad()
		}
		if old.Terminal() && (old.Stage != n.Stage || old.State != n.State) {
			return bad()
		}
	}
	for id, old := range before.CIRuns {
		n := after.CIRuns[id]
		if n == nil || n.ID != old.ID || n.ArtifactID != old.ArtifactID || n.ChangeID != old.ChangeID {
			return bad()
		}
		if old.Started != "" && (old.Started != n.Started || old.Timeout != n.Timeout || old.Lease != n.Lease || old.ConfigHash != n.ConfigHash || !reflect.DeepEqual(old.Command, n.Command) || old.Stdout != n.Stdout || old.Stderr != n.Stderr) {
			return bad()
		}
		if old.Ended != "" && !reflect.DeepEqual(old, n) {
			return bad()
		}
	}
	if len(after.ObjectiveRevisions) < len(before.ObjectiveRevisions) || !reflect.DeepEqual(before.ObjectiveRevisions, after.ObjectiveRevisions[:len(before.ObjectiveRevisions)]) {
		return bad()
	}
	return nil
}
