package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"scp-harness/internal/config"
	"scp-harness/internal/gitrepo"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
	"scp-harness/internal/store"
	"scp-harness/internal/wsl"
)

type Core struct {
	Config     *config.Config
	Store      *store.Store
	Git        *gitrepo.Git
	Runner     wsl.Runner
	TestRunner wsl.Runner
}

func Open(cfg *config.Config, init bool) (*Core, error) {
	if init {
		if e := os.MkdirAll(cfg.Artifacts, 0700); e != nil {
			return nil, model.Err("STORAGE_FAILURE", "artifact store: %v", e)
		}
	}
	db, e := store.Open(cfg.Database, init)
	if e != nil {
		return nil, e
	}
	return &Core{Config: cfg, Store: db, Git: &gitrepo.Git{Config: cfg}, Runner: wsl.Runner{Config: cfg}, TestRunner: wsl.Runner{Config: cfg, Protected: true}}, nil
}
func (c *Core) Operator() config.Card { return c.Config.Cards[c.Config.Operator] }
func (c *Core) Read() (*model.State, error) {
	s, e := c.Store.Read()
	if model.Exit(model.Code(e)) == 5 {
		_ = c.Store.EmergencyBlock(model.Code(e), e.Error())
	}
	return s, e
}
func task(s *model.State, id string) (*model.Task, error) {
	t := s.Tasks[id]
	if t == nil {
		return nil, model.Err("NOT_FOUND", "Task %s", id)
	}
	return t, nil
}
func openOption(s *model.State, id string) (*model.Option, error) {
	o := s.Options[id]
	if o == nil {
		return nil, model.Err("NOT_FOUND", "Option %s", id)
	}
	if o.Status != "OPEN" {
		return nil, model.Err("INVALID_STATE", "Option is CLOSED")
	}
	if s.Tasks[o.TaskID].Status != "ACTIVE" {
		return nil, model.Err("INVALID_STATE", "Task is not ACTIVE")
	}
	return o, nil
}
func check(s *model.State) error {
	if s.FailStop() {
		return model.Err("BLOCKED", "global fail-stop; inspect blockers")
	}
	return nil
}
func (c *Core) Update(fn func(*model.State) error) error {
	return c.Store.Update(func(s *model.State) error {
		if e := check(s); e != nil {
			return e
		}
		return fn(s)
	})
}
func (c *Core) CreateTask(ctx context.Context, objective, repo, ref, responsible string, n int64) (*model.Task, error) {
	if e := c.Operator().Require("task.create"); e != nil {
		return nil, e
	}
	if strings.TrimSpace(objective) == "" || repo == "" || ref == "" || strings.TrimSpace(responsible) == "" || n <= 0 {
		return nil, model.Err("PRECONDITION_FAILED", "all Task inputs and positive budget required")
	}
	absolute, identity, e := gitrepo.Identity(repo)
	if e != nil {
		return nil, e
	}
	sha, e := c.Git.Resolve(ctx, absolute, ref)
	if e != nil {
		return nil, e
	}
	var result *model.Task
	e = c.Update(func(s *model.State) error {
		for _, t := range s.Tasks {
			if t.Status == "ACTIVE" && s.RepositoryIdentities[t.ID] == identity {
				return model.Err("PRECONDITION_FAILED", "repository already bound")
			}
		}
		id := model.ID()
		now := model.Now()
		t := &model.Task{ID: id, Objective: objective, RepoPath: absolute, RepoRef: ref, Responsible: responsible, Status: "ACTIVE", SHA: sha, Revision: 1, Resources: model.Resources{Minted: n}, Created: now, Updated: now}
		s.Tasks[id] = t
		s.RepositoryIdentities[id] = identity
		s.Accounts[id] = &model.Account{TaskID: id, Remaining: n}
		s.Exploration[id] = &model.Exploration{Batch: []string{}, Groups: [][]string{}}
		s.Audit(id, "TASK_CREATED", "explicit initial budget")
		result = t
		return nil
	})
	return result, e
}
func (c *Core) Extend(id string, n int64) (*model.Task, error) {
	if e := c.Operator().Require("task.extend"); e != nil {
		return nil, e
	}
	var result *model.Task
	e := c.Update(func(s *model.State) error {
		t, e := task(s, id)
		if e != nil {
			return e
		}
		if t.Status == "CLOSED" {
			return model.Err("INVALID_STATE", "CLOSED Task cannot mint")
		}
		if n <= 0 {
			return model.Err("PRECONDITION_FAILED", "extension must be positive")
		}
		v, e := ledger.Add(t.Resources.Minted, n)
		if e != nil {
			return e
		}
		r, e := ledger.Add(s.Accounts[id].Remaining, n)
		if e != nil {
			return e
		}
		t.Resources.Minted = v
		s.Accounts[id].Remaining = r
		s.Touch(id)
		s.Audit(id, "TASK_EXTENDED", fmt.Sprint(n))
		result = t
		return nil
	})
	return result, e
}
func createOption(s *model.State, t *model.Task, text, parent, actor, sha string, rev int64, parents []string, relation string) *model.Option {
	o := &model.Option{ID: model.ID(), TaskID: t.ID, Text: text, Status: "OPEN", Parent: parent, SHA: sha, Revision: rev, Actor: actor, Created: model.Now()}
	s.Options[o.ID] = o
	s.Accounts[o.ID] = &model.Account{TaskID: t.ID, Parent: parent}
	for _, p := range parents {
		s.Edges = append(s.Edges, model.Edge{Parent: p, Child: o.ID, Relation: relation})
	}
	return o
}
func (c *Core) Propose(taskID, text, source string) (*model.Option, error) {
	if e := c.Operator().Require("option.propose"); e != nil {
		return nil, e
	}
	if strings.TrimSpace(text) == "" {
		return nil, model.Err("SCHEMA_INVALID", "empty Option text")
	}
	var result *model.Option
	e := c.Update(func(s *model.State) error {
		t, e := task(s, taskID)
		if e != nil {
			return e
		}
		if t.Status != "ACTIVE" {
			return model.Err("INVALID_STATE", "Task not ACTIVE")
		}
		parent := taskID
		parents := []string{}
		if source != "" {
			o, e := openOption(s, source)
			if e != nil {
				return e
			}
			if o.TaskID != taskID {
				return model.Err("PRECONDITION_FAILED", "cross-Task source")
			}
			parent = source
			parents = append(parents, source)
		}
		result = createOption(s, t, text, parent, c.Operator().ID, t.SHA, t.Revision, parents, "propose")
		s.Touch(taskID)
		return nil
	})
	return result, e
}

type Child struct {
	Text string `json:"text"`
	Wall int64  `json:"wall_ms"`
}
type SplitSpec struct {
	Children []Child `json:"children"`
}
type Participant struct {
	ID   string `json:"option_id"`
	Wall int64  `json:"transfer_wall_ms"`
}
type MergeSpec struct {
	Text         string        `json:"text"`
	Participants []Participant `json:"participants"`
}
type SplitResult struct {
	Parent   *model.Option   `json:"parent"`
	Children []*model.Option `json:"children"`
}

func (c *Core) Split(id string, children []Child, refine bool) (*SplitResult, error) {
	cap, rel := "option.split", "split"
	minimum := 2
	if refine {
		cap = "option.refine"
		rel = "refine"
		minimum = 1
	}
	if e := c.Operator().Require(cap); e != nil {
		return nil, e
	}
	if len(children) < minimum {
		return nil, model.Err("SCHEMA_INVALID", "too few children")
	}
	var result *SplitResult
	e := c.Update(func(s *model.State) error {
		o, e := openOption(s, id)
		if e != nil {
			return e
		}
		var total int64
		for _, child := range children {
			if strings.TrimSpace(child.Text) == "" {
				return model.Err("SCHEMA_INVALID", "empty child text")
			}
			total, e = ledger.Add(total, child.Wall)
			if e != nil {
				return e
			}
		}
		if total > s.Accounts[id].Remaining {
			return model.Err("INSUFFICIENT_RESOURCE", "child allocations exceed parent balance")
		}
		t := s.Tasks[o.TaskID]
		result = &SplitResult{Parent: o, Children: []*model.Option{}}
		for _, child := range children {
			n := createOption(s, t, child.Text, id, c.Operator().ID, t.SHA, t.Revision, []string{id}, rel)
			if e = ledger.Move(s, id, n.ID, child.Wall); e != nil {
				return e
			}
			result.Children = append(result.Children, n)
		}
		if refine {
			o.Status = "CLOSED"
			o.CloseReason = "SUPERSEDED"
		}
		s.Touch(t.ID)
		return nil
	})
	return result, e
}
func (c *Core) Merge(taskID string, spec MergeSpec) (*model.Option, error) {
	if e := c.Operator().Require("option.merge"); e != nil {
		return nil, e
	}
	if len(spec.Participants) < 2 || strings.TrimSpace(spec.Text) == "" {
		return nil, model.Err("SCHEMA_INVALID", "invalid merge")
	}
	var result *model.Option
	e := c.Update(func(s *model.State) error {
		t, e := task(s, taskID)
		if e != nil {
			return e
		}
		ids := []string{}
		seen := map[string]bool{}
		var total int64
		for _, p := range spec.Participants {
			o, e := openOption(s, p.ID)
			if e != nil {
				return e
			}
			if o.TaskID != taskID || seen[p.ID] {
				return model.Err("PRECONDITION_FAILED", "cross-Task/duplicate merge participant")
			}
			seen[p.ID] = true
			ids = append(ids, p.ID)
			total, e = ledger.Add(total, p.Wall)
			if e != nil {
				return e
			}
			if s.Accounts[p.ID].Remaining < p.Wall {
				return model.Err("INSUFFICIENT_RESOURCE", "merge transfer exceeds balance")
			}
		}
		parent, e := ledger.LCA(s, ids)
		if e != nil {
			return e
		}
		result = createOption(s, t, spec.Text, parent, c.Operator().ID, t.SHA, t.Revision, ids, "merge")
		for _, p := range spec.Participants {
			if e = ledger.Move(s, p.ID, result.ID, p.Wall); e != nil {
				return e
			}
		}
		for _, p := range spec.Participants {
			s.Options[p.ID].Status = "CLOSED"
			s.Options[p.ID].CloseReason = "SUPERSEDED"
		}
		s.Touch(taskID)
		return nil
	})
	return result, e
}
func (c *Core) Allocate(id string, n int64) (*model.Option, error) {
	if e := c.Operator().Require("option.allocate"); e != nil {
		return nil, e
	}
	if n <= 0 {
		return nil, model.Err("PRECONDITION_FAILED", "allocation must be positive")
	}
	var result *model.Option
	e := c.Update(func(s *model.State) error {
		o, e := openOption(s, id)
		if e != nil {
			return e
		}
		if e = ledger.Allocate(s, id, n); e != nil {
			return e
		}
		s.Touch(o.TaskID)
		result = o
		return nil
	})
	return result, e
}
func closeOption(s *model.State, id string) error {
	o := s.Options[id]
	if o == nil {
		return model.Err("NOT_FOUND", "Option")
	}
	if o.Status != "OPEN" {
		return model.Err("INVALID_STATE", "Option already CLOSED")
	}
	if s.Tasks[o.TaskID].Status != "ACTIVE" {
		return model.Err("INVALID_STATE", "Option resources are frozen")
	}
	for _, l := range s.Leases {
		if l.Anchor == id {
			return model.Err("PRECONDITION_FAILED", "Option has outstanding execution")
		}
	}
	if e := ledger.Move(s, id, o.Parent, s.Accounts[id].Remaining); e != nil {
		return e
	}
	o.Status = "CLOSED"
	o.CloseReason = "FULFILLED"
	if p := s.Pending[o.TaskID]; p != nil && p.OptionID == id {
		delete(s.Pending, o.TaskID)
	}
	return nil
}
func closeTask(s *model.State, id string) error {
	t := s.Tasks[id]
	if t == nil {
		return model.Err("NOT_FOUND", "Task")
	}
	if t.Status == "CLOSED" {
		return model.Err("INVALID_STATE", "Task already CLOSED")
	}
	for _, l := range s.Leases {
		if l.TaskID == id {
			return model.Err("CORE_INCONSISTENT", "Task close with outstanding lease")
		}
	}
	var n int64
	for _, a := range s.Accounts {
		if a.TaskID == id {
			n += a.Remaining
			a.Remaining = 0
		}
	}
	t.Resources.Retired += n
	t.Status = "CLOSED"
	for _, ch := range s.Changes {
		if ch.TaskID == id && !ch.Terminal() {
			ch.Set(ch.Stage, "PAUSED", "TASK_CLOSED")
		}
	}
	delete(s.Pending, id)
	delete(s.Cancellations, id)
	s.Exploration[id].Done = true
	return nil
}
func addClaim(s *model.State, t *model.Task, card config.Card, kind, id, typ string, payload json.RawMessage, sha string, rev int64) (*model.Claim, error) {
	if typ == "discussion.comment" || typ == "discussion.reply" {
		var v discussionText
		if e := config.Strict(payload, &v); e != nil || kind != "OPTION" || strings.TrimSpace(v.Text) == "" {
			return nil, model.Err("SCHEMA_INVALID", "discussion requires an Option and non-empty text payload")
		}
	}
	if typ == "resource.propose" {
		if e := card.Require("claim.publish", "resource.propose"); e != nil {
			return nil, e
		}
	}
	if !store.Subject(s, t.ID, kind, id) || strings.TrimSpace(typ) == "" {
		return nil, model.Err("PRECONDITION_FAILED", "invalid claim subject/type")
	}
	claim := &model.Claim{ID: model.ID(), TaskID: t.ID, SubjectType: kind, SubjectID: id, Type: typ, Payload: payload, Issuer: card.ID, SHA: sha, Revision: rev, Created: model.Now()}
	if typ == "fulfilled" {
		if kind == "OPTION" && card.Has("option.complete") {
			if e := closeOption(s, id); e != nil {
				return nil, e
			}
		}
		if kind == "TASK" && card.Has("task.complete") {
			if e := closeTask(s, id); e != nil {
				return nil, e
			}
		}
	}
	s.Claims[claim.ID] = claim
	return claim, nil
}
func (c *Core) CreateClaim(ctx context.Context, taskID, kind, id, typ string, payload json.RawMessage) (*model.Claim, error) {
	card := c.Operator()
	if e := card.Require("claim.publish"); e != nil {
		return nil, e
	}
	var obj map[string]json.RawMessage
	if e := config.Strict(payload, &obj); e != nil {
		return nil, model.Err("INVALID_JSON", "claim payload must be object: %v", e)
	}
	qualifying := qualifyingClaim(card, kind, typ)
	if qualifying {
		s, unlock, e := c.beginControl(taskID)
		if e != nil {
			return nil, e
		}
		defer unlock()
		t, e := task(s, taskID)
		if e != nil {
			return nil, e
		}
		if !store.Subject(s, taskID, kind, id) {
			return nil, model.Err("PRECONDITION_FAILED", "invalid completion subject")
		}
		if t.Status == "CLOSED" || kind == "OPTION" && (s.Options[id].Status == "CLOSED" || t.Status != "ACTIVE") {
			return nil, model.Err("INVALID_STATE", "completion subject already CLOSED")
		}
		if e := c.cancel(ctx, taskID); e != nil {
			return nil, e
		}
	}
	var result *model.Claim
	e := c.Update(func(s *model.State) error {
		t, e := task(s, taskID)
		if e != nil {
			return e
		}
		if t.Status == "CLOSED" {
			return model.Err("INVALID_STATE", "CLOSED Task")
		}
		if qualifying && !store.TaskQuiescent(s, taskID) {
			return model.Err("BLOCKED", "completion requires settled Task execution")
		}
		result, e = addClaim(s, t, card, kind, id, typ, payload, t.SHA, t.Revision)
		if e != nil {
			return e
		}
		// Informational and resource-proposal Claims do not own the lifecycle
		// cancellation barrier. Only this request's qualifying close may release it.
		if qualifying {
			delete(s.Cancellations, taskID)
		}
		s.Touch(taskID)
		return nil
	})
	return result, e
}
func (c *Core) CloseOption(ctx context.Context, id string) (*model.Option, error) {
	return c.CloseOptionReason(ctx, id, "FULFILLED")
}
func (c *Core) CloseOptionReason(ctx context.Context, id, reason string) (*model.Option, error) {
	if reason != "FULFILLED" && reason != "SUPERSEDED" && reason != "ABANDONED" {
		return nil, model.Err("USAGE_ERROR", "invalid close reason")
	}
	if e := c.Operator().Require("option.complete"); e != nil {
		return nil, e
	}
	s, e := c.Read()
	if e != nil {
		return nil, e
	}
	o := s.Options[id]
	if o == nil {
		return nil, model.Err("NOT_FOUND", "Option")
	}
	// Only immutable membership is read before the gate; state is checked again
	// after acquiring the same Task gate used by every other control entry.
	s, unlock, e := c.beginControl(o.TaskID)
	if e != nil {
		return nil, e
	}
	defer unlock()
	o, e = openOption(s, id)
	if e != nil {
		return nil, e
	}
	cancelled := false
	if p := s.Pending[o.TaskID]; p != nil && p.OptionID == id {
		if e = c.cancel(ctx, o.TaskID); e != nil {
			return nil, e
		}
		cancelled = true
	}
	var result *model.Option
	e = c.Update(func(s *model.State) error {
		o, e := openOption(s, id)
		if e != nil {
			return e
		}
		t := s.Tasks[o.TaskID]
		if cancelled && !store.TaskQuiescent(s, t.ID) {
			return model.Err("BLOCKED", "Option close requires settled cancellation")
		}
		if reason == "FULFILLED" {
			if _, e = addClaim(s, t, c.Operator(), "OPTION", id, "fulfilled", json.RawMessage(`{}`), t.SHA, t.Revision); e != nil {
				return e
			}
		} else {
			if e = closeOption(s, id); e != nil {
				return e
			}
			o.CloseReason = reason
		}
		delete(s.Cancellations, t.ID)
		s.Touch(t.ID)
		result = o
		return nil
	})
	return result, e
}
func (c *Core) Lifecycle(ctx context.Context, id, action string) (*model.Task, error) {
	capability := map[string]string{"suspend": "task.suspend", "resume": "task.resume", "close": "task.complete"}[action]
	if e := c.Operator().Require(capability); e != nil {
		return nil, e
	}
	s, unlock, e := c.beginControl(id)
	if e != nil {
		return nil, e
	}
	defer unlock()
	t, e := task(s, id)
	if e != nil {
		return nil, e
	}
	if action == "resume" && t.Status != "SUSPENDED" || action == "suspend" && t.Status != "ACTIVE" || action == "close" && t.Status == "CLOSED" {
		return nil, model.Err("INVALID_STATE", "invalid Task lifecycle transition")
	}
	sha := ""
	identity := ""
	if action == "resume" {
		_, identity, e = gitrepo.Identity(t.RepoPath)
		if e != nil {
			return nil, e
		}
		sha, e = c.Git.Resolve(ctx, t.RepoPath, t.RepoRef)
		if e != nil {
			return nil, e
		}
	} else {
		if e = c.cancel(ctx, id); e != nil {
			return nil, e
		}
	}
	return c.finishLifecycle(id, action, sha, identity)
}

// finishLifecycle is the final transaction after cancellation has settled.
func (c *Core) finishLifecycle(id, action, sha, identity string) (*model.Task, error) {
	var result *model.Task
	e := c.Update(func(s *model.State) error {
		t, e := task(s, id)
		if e != nil {
			return e
		}
		switch action {
		case "resume":
			if t.Status != "SUSPENDED" {
				return model.Err("INVALID_STATE", "Task not SUSPENDED")
			}
			for _, other := range s.Tasks {
				if other.ID != id && other.Status == "ACTIVE" && s.RepositoryIdentities[other.ID] == identity {
					return model.Err("PRECONDITION_FAILED", "repository bound")
				}
			}
			t.Status = "ACTIVE"
			t.SHA = sha
			s.RepositoryIdentities[id] = identity
			for _, ch := range s.Changes {
				if ch.TaskID == id && !ch.Terminal() && ch.BaseSHA != sha {
					ch.Set(ch.Stage, "STALE", "BASE_CHANGED")
				}
			}
		case "suspend":
			if t.Status != "ACTIVE" {
				return model.Err("INVALID_STATE", "Task not ACTIVE")
			}
			if !store.TaskQuiescent(s, id) {
				return model.Err("BLOCKED", "suspend requires settled Task execution")
			}
			t.Status = "SUSPENDED"
			delete(s.Pending, id)
			delete(s.Cancellations, id)
		case "close":
			if _, e = addClaim(s, t, c.Operator(), "TASK", id, "fulfilled", json.RawMessage(`{}`), t.SHA, t.Revision); e != nil {
				return e
			}
		}
		s.Touch(id)
		result = t
		return nil
	})
	return result, e
}
func (c *Core) requestCancellation(taskID string) error {
	return c.Store.Update(func(s *model.State) error {
		if s.Tasks[taskID] == nil {
			return model.Err("NOT_FOUND", "Task")
		}
		s.Cancellations[taskID] = true
		for _, ch := range s.Changes {
			if ch.TaskID == taskID && !ch.Terminal() {
				ch.Set(ch.Stage, "PAUSED", "")
			}
		}
		// Keep the selected activity until settlement; its monitor observes cancellation.
		if s.SlotTask != taskID {
			delete(s.Pending, taskID)
		}
		requests := s.Requests[:0]
		for _, p := range s.Requests {
			if p.TaskID != taskID {
				requests = append(requests, p)
			} else if r := s.CIRuns[p.CIRunID]; r != nil && r.Status == "QUEUED" {
				r.Status = "TERMINATED"
				r.Ended = model.Now()
			}
		}
		s.Requests = requests
		for id, a := range s.Attempts {
			if a.TaskID == taskID && a.Active() {
				s.Interrupts[id] = true
			}
		}
		return nil
	})
}

// cancel is shared by control operations that already hold their Task gate.
func (c *Core) cancel(ctx context.Context, taskID string) error {
	if e := c.requestCancellation(taskID); e != nil {
		return e
	}
	deadline := time.NewTimer(time.Duration(c.Config.Limits.ProcessMS) * time.Millisecond)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		s, e := c.Read()
		if e != nil {
			return e
		}
		if store.TaskQuiescent(s, taskID) {
			break
		}
		select {
		case <-ctx.Done():
			return model.Err("PRECONDITION_FAILED", "cancellation wait interrupted")
		case <-deadline.C:
			return model.Err("BLOCKED", "execution owner did not settle; run recover after it stops")
		case <-tick.C:
		}
	}
	// The lifecycle transaction clears the cancellation flag. Clearing it here
	// would let the scheduler start new work in the gap before suspend/close.
	return nil
}
func (c *Core) Interrupt(ctx context.Context, id string) (*model.Attempt, error) {
	if e := c.Operator().Require("attempt.interrupt"); e != nil {
		return nil, e
	}
	e := c.Store.Update(func(s *model.State) error {
		a := s.Attempts[id]
		if a == nil {
			return model.Err("NOT_FOUND", "Attempt")
		}
		if !a.Active() {
			return model.Err("INVALID_STATE", "Attempt already terminal")
		}
		s.Interrupts[id] = true
		s.Audit(a.TaskID, "INTERRUPT_REQUESTED", id)
		return nil
	})
	if e != nil {
		return nil, e
	}
	deadline := time.Now().Add(time.Duration(c.Config.Limits.ProcessMS) * time.Millisecond)
	for {
		state, e := c.Read()
		if e != nil {
			return nil, e
		}
		a := state.Attempts[id]
		if !a.Active() {
			return a, nil
		}
		if time.Now().After(deadline) {
			return nil, model.Err("BLOCKED", "execution owner did not settle; recover required")
		}
		select {
		case <-ctx.Done():
			return nil, model.Err("PRECONDITION_FAILED", "interrupt wait canceled")
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (c *Core) Block(kind, taskID, profile, runner, message string) error {
	return c.Store.Update(func(s *model.State) error { return c.block(s, kind, taskID, profile, runner, message) })
}
func (c *Core) block(s *model.State, kind, taskID, profile, runner, message string) error {
	scope := "GLOBAL"
	var subject *string
	switch kind {
	case "WORKER_UNAVAILABLE":
		scope = "WORKER_PROFILE"
		subject = &profile
	case "RUNNER_UNAVAILABLE":
		scope = "RUNNER"
		subject = &runner
	case "REPOSITORY_UNAVAILABLE":
		scope = "TASK"
		subject = &taskID
	}
	for _, b := range s.Blockers {
		if b.Resolved == nil && b.Kind == kind && b.Scope == scope && (b.Subject == nil && subject == nil || b.Subject != nil && subject != nil && *b.Subject == *subject) {
			return nil
		}
	}
	id := model.ID()
	s.Blockers[id] = &model.Blocker{ID: id, Kind: kind, Scope: scope, Subject: subject, Message: message, Created: model.Now()}
	s.Audit(taskID, kind, message)
	if taskID != "" {
		s.Touch(taskID)
	}
	return nil
}
func (c *Core) ResolveBlocker(id string) (*model.Blocker, error) {
	var result *model.Blocker
	e := c.Store.Update(func(s *model.State) error {
		b := s.Blockers[id]
		if b == nil {
			return model.Err("NOT_FOUND", "Blocker")
		}
		if b.Resolved != nil {
			return model.Err("INVALID_STATE", "Blocker already resolved")
		}
		now := model.Now()
		b.Resolved = &now
		s.Audit("", "BLOCKER_RESOLVED", id)
		result = b
		return nil
	})
	return result, e
}
func SortedTasks(s *model.State) []*model.Task {
	v := []*model.Task{}
	for _, t := range s.Tasks {
		v = append(v, t)
	}
	sort.Slice(v, func(i, j int) bool {
		if v[i].Created == v[j].Created {
			return v[i].ID < v[j].ID
		}
		return v[i].Created < v[j].Created
	})
	return v
}
