package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func Now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

type Resources struct {
	Minted      int64 `json:"total_minted_wall_ms"`
	Remaining   int64 `json:"remaining_wall_ms"`
	Outstanding int64 `json:"outstanding_lease_wall_ms"`
	Charged     int64 `json:"total_charged_wall_ms"`
	Retired     int64 `json:"retired_wall_ms"`
}
type Task struct {
	ObjectiveRevision int64     `json:"objective_revision"`
	ID                string    `json:"id"`
	Objective         string    `json:"objective"`
	RepoPath          string    `json:"repo_path"`
	RepoRef           string    `json:"repo_ref"`
	Responsible       string    `json:"responsible_actor_id"`
	Status            string    `json:"status"`
	SHA               string    `json:"current_authoritative_sha"`
	Revision          int64     `json:"state_revision"`
	Resources         Resources `json:"resources"`
	Created           string    `json:"created_at"`
	Updated           string    `json:"updated_at"`
}
type Option struct {
	CloseReason string `json:"close_reason,omitempty"`
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	Text        string `json:"text"`
	Status      string `json:"status"`
	Parent      string `json:"resource_parent_id"`
	Remaining   int64  `json:"remaining_wall_ms"`
	SHA         string `json:"created_against_repo_sha"`
	Revision    int64  `json:"created_against_state_revision"`
	Actor       string `json:"created_by_actor"`
	Created     string `json:"created_at"`
}
type Claim struct {
	ID          string          `json:"id"`
	TaskID      string          `json:"task_id"`
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Type        string          `json:"claim_type"`
	Payload     json.RawMessage `json:"payload_json"`
	Issuer      string          `json:"issuer_actor_id"`
	SHA         string          `json:"created_against_repo_sha"`
	Revision    int64           `json:"created_against_state_revision"`
	Created     string          `json:"created_at"`
}
type Artifact struct {
	ChangeID  string `json:"change_id,omitempty"`
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Anchor    string `json:"semantic_anchor_option_id"`
	AttemptID string `json:"source_attempt_id"`
	BlobPath  string `json:"blob_path"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size_bytes"`
	BaseSHA   string `json:"base_repo_sha"`
	Created   string `json:"created_at"`
}
type Attempt struct {
	Objective  string  `json:"objective_snapshot,omitempty"`
	ChangeID   string  `json:"change_id,omitempty"`
	ID         string  `json:"id"`
	TaskID     string  `json:"task_id"`
	Operation  string  `json:"operation"`
	TargetType string  `json:"target_type"`
	TargetID   string  `json:"target_id"`
	Actor      string  `json:"actor_instance"`
	Profile    string  `json:"worker_profile"`
	AnchorType string  `json:"resource_anchor_type"`
	AnchorID   string  `json:"resource_anchor_id"`
	Lease      int64   `json:"lease_wall_ms"`
	SHA        string  `json:"created_against_repo_sha"`
	Revision   int64   `json:"created_against_state_revision"`
	Started    string  `json:"started_at"`
	Ended      *string `json:"ended_at"`
	Status     string  `json:"status"`
	ExitCode   *int    `json:"exit_code"`
	Reason     *string `json:"termination_reason"`
	Stdout     string  `json:"stdout_path"`
	Stderr     string  `json:"stderr_path"`
	ArtifactID *string `json:"produced_artifact_id"`
}

func (a Attempt) Active() bool { return a.Status == "PREPARING" || a.Status == "RUNNING" }

type Blocker struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Scope    string  `json:"scope"`
	Subject  *string `json:"subject_id"`
	Message  string  `json:"message"`
	Created  string  `json:"created_at"`
	Resolved *string `json:"resolved_at"`
}
type Journal struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	ArtifactID string `json:"artifact_id"`
	Ref        string `json:"target_ref"`
	OldSHA     string `json:"old_sha"`
	NewSHA     string `json:"new_sha"`
	State      string `json:"state"`
	Created    string `json:"created_at"`
}

// The following records are mechanical runtime state, never semantic objects.
type Account struct {
	TaskID, Parent string
	Remaining      int64
}
type Lease struct {
	TaskID, Anchor string
	Amount         int64
}
type Edge struct{ Parent, Child, Relation string }
type Exploration struct {
	Done       bool
	Count      int
	Batch      []string
	Judged     bool
	Groups     [][]string
	GroupIndex int
}
type Step struct {
	ChangeID, CIRunID, RequestID                      string
	TaskID, OptionID, Operation, TargetType, TargetID string
	Participants                                      []string
}
type Review struct {
	CIRunID                        string `json:"ci_run_id"`
	ArtifactID, AttemptID, Verdict string
	Findings                       []string
	Created                        string
}
type Event struct{ ID, TaskID, Code, Message, Created string }
type Slot struct {
	Kind  string  `json:"activity_kind,omitempty"`
	State string  `json:"state"`
	Owner *string `json:"owner_activity_id"`
}
type State struct {
	Changes              map[string]*Change
	CIRuns               map[string]*CIRun
	Requests             []*Step
	ObjectiveRevisions   []ObjectiveRevision
	SchedulerConfigHash  string
	SchedulerDiskHash    string
	SchedulerStarted     string
	Tasks                map[string]*Task
	RepositoryIdentities map[string]string
	Options              map[string]*Option
	Claims               map[string]*Claim
	Artifacts            map[string]*Artifact
	Attempts             map[string]*Attempt
	Accounts             map[string]*Account
	Leases               map[string]*Lease
	Edges                []Edge
	Exploration          map[string]*Exploration
	Pending              map[string]*Step
	Reviews              map[string]*Review
	Interrupts           map[string]bool
	Cancellations        map[string]bool
	Slot                 Slot
	SlotTask             string
	SlotPID              int
	SlotOwner            string
	Events               []Event
	Blockers             map[string]*Blocker
	Journals             map[string]*Journal
}

func NewState() *State {
	return &State{Changes: map[string]*Change{}, CIRuns: map[string]*CIRun{}, Requests: []*Step{}, ObjectiveRevisions: []ObjectiveRevision{}, Tasks: map[string]*Task{}, RepositoryIdentities: map[string]string{}, Options: map[string]*Option{}, Claims: map[string]*Claim{}, Artifacts: map[string]*Artifact{}, Attempts: map[string]*Attempt{}, Accounts: map[string]*Account{}, Leases: map[string]*Lease{}, Exploration: map[string]*Exploration{}, Pending: map[string]*Step{}, Reviews: map[string]*Review{}, Interrupts: map[string]bool{}, Cancellations: map[string]bool{}, Edges: []Edge{}, Events: []Event{}, Blockers: map[string]*Blocker{}, Journals: map[string]*Journal{}, Slot: Slot{State: "IDLE"}}
}
func (s *State) Audit(task, code, message string) {
	s.Events = append(s.Events, Event{ID(), task, code, message, Now()})
}
func (s *State) Touch(task string) {
	if t := s.Tasks[task]; t != nil {
		t.Revision++
		t.Updated = Now()
	}
}
func (s *State) Blocked(task, profile, runner string) bool {
	for _, b := range s.Blockers {
		if b.Resolved != nil {
			continue
		}
		if b.Scope == "GLOBAL" || b.Subject != nil && (b.Scope == "RUNNER" && *b.Subject == runner || b.Scope == "TASK" && *b.Subject == task || b.Scope == "WORKER_PROFILE" && *b.Subject == profile) {
			return true
		}
	}
	return false
}
func (s *State) FailStop() bool {
	for _, b := range s.Blockers {
		if b.Scope == "GLOBAL" && b.Resolved == nil {
			return true
		}
	}
	return false
}
