package model

type Change struct {
	ID                string `json:"id"`
	TaskID            string `json:"task_id"`
	OptionID          string `json:"option_id"`
	BaseSHA           string `json:"base_repo_sha"`
	Objective         string `json:"objective"`
	ObjectiveRevision int64  `json:"objective_revision"`
	ArtifactID        string `json:"current_artifact_id,omitempty"`
	CIRunID           string `json:"current_ci_run_id,omitempty"`
	ReviewAttemptID   string `json:"current_review_attempt_id,omitempty"`
	Stage             string `json:"stage"`
	State             string `json:"state"`
	Reason            string `json:"reason,omitempty"`
	TimeoutStreak     int    `json:"timeout_streak"`
	TimeoutResetAt    string `json:"timeout_reset_at,omitempty"`
	Started           bool   `json:"started"`
	Created           string `json:"created_at"`
	Updated           string `json:"updated_at"`
}

func (c *Change) Terminal() bool { return c.State == "DONE" || c.State == "ABORTED" }
func (c *Change) Set(stage, state, reason string) {
	c.Stage, c.State, c.Reason, c.Updated = stage, state, reason, Now()
}

type CIRun struct {
	ID              string   `json:"id"`
	ChangeID        string   `json:"change_id,omitempty"`
	ArtifactID      string   `json:"artifact_id"`
	Status          string   `json:"status"`
	Created         string   `json:"created_at"`
	Started         string   `json:"started_at,omitempty"`
	Ended           string   `json:"ended_at,omitempty"`
	ExitCode        *int     `json:"exit_code"`
	Stdout          string   `json:"stdout_path"`
	Stderr          string   `json:"stderr_path"`
	StdoutTruncated bool     `json:"stdout_truncated"`
	StderrTruncated bool     `json:"stderr_truncated"`
	Lease           int64    `json:"lease_wall_ms"`
	Elapsed         int64    `json:"elapsed_wall_ms"`
	Timeout         int64    `json:"effective_timeout_ms"`
	Command         []string `json:"effective_command"`
	ConfigHash      string   `json:"effective_config_hash"`
}

func (r *CIRun) Active() bool { return r.Status == "PREPARING" || r.Status == "RUNNING" }

type ObjectiveRevision struct {
	TaskID   string `json:"task_id"`
	Revision int64  `json:"revision"`
	Old      string `json:"old_objective"`
	New      string `json:"new_objective"`
	Operator string `json:"operator"`
	Created  string `json:"timestamp"`
}

// LatestCI is a read projection. Every run remains independently inspectable.
func (s *State) LatestCI(artifact string) *CIRun {
	var latest *CIRun
	for _, r := range s.CIRuns {
		if r.ArtifactID == artifact && r.Ended != "" && (latest == nil || r.Ended > latest.Ended || r.Ended == latest.Ended && r.ID > latest.ID) {
			latest = r
		}
	}
	return latest
}
