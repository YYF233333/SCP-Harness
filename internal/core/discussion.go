package core

import (
	"encoding/json"
	"sort"
	"strings"

	"scp-harness/internal/model"
)

type discussionText struct {
	Text string `json:"text"`
}

type OptionThread struct {
	Option   *model.Option  `json:"option"`
	Messages []*model.Claim `json:"messages"`
}

type DiscussionRequest struct {
	Comment *model.Claim `json:"comment"`
	Queued  bool         `json:"queued"`
}

// The SQLite transaction is acquired before the nonblocking Task gate. A busy
// control returns immediately; the gate is never held waiting for SQLite.
func (c *Core) enqueueOption(id string, fn func(*model.State, *model.Option) error) error {
	var unlock func()
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	return c.Update(func(s *model.State) error {
		o, e := openOption(s, id)
		if e != nil {
			return e
		}
		unlock, e = c.LockTaskControl(o.TaskID)
		if e != nil {
			return e
		}
		if s.Cancellations[o.TaskID] {
			return model.Err("BLOCKED", "Task cancellation in progress")
		}
		if !s.Exploration[o.TaskID].Done {
			return model.Err("INVALID_STATE", "initial exploration is not complete")
		}
		return fn(s, o)
	})
}

// ReleaseOption is the sole host entry that seeds a new Option mutation chain.
// Funding and informational Claims never call this method.
func (c *Core) ReleaseOption(id string) (*model.Change, error) {
	if e := c.Operator().Require("option.release"); e != nil {
		return nil, e
	}
	var result *model.Change
	e := c.enqueueOption(id, func(s *model.State, o *model.Option) error {
		if e := c.Operator().Require("option.release"); e != nil {
			return e
		}
		if s.Accounts[id].Remaining <= 0 {
			return model.Err("INSUFFICIENT_RESOURCE", "Option has no remaining resource")
		}
		for _, ch := range s.Changes {
			if ch.OptionID == id && !ch.Terminal() {
				return model.Err("INVALID_STATE", "Option already has a nonterminal Change")
			}
		}
		t := s.Tasks[o.TaskID]
		now := model.Now()
		result = &model.Change{ID: model.ID(), TaskID: o.TaskID, OptionID: id, BaseSHA: t.SHA, Objective: t.Objective, ObjectiveRevision: t.ObjectiveRevision, Stage: "MUTATION", State: "QUEUED", Created: now, Updated: now}
		s.Changes[result.ID] = result
		s.Audit(o.TaskID, "OPTION_RELEASED", id)
		s.Touch(o.TaskID)
		return nil
	})
	return result, e
}

func (c *Core) CommentOption(id, text string) (*model.Claim, error) {
	if e := c.Operator().Require("claim.publish"); e != nil {
		return nil, e
	}
	if strings.TrimSpace(text) == "" {
		return nil, model.Err("SCHEMA_INVALID", "empty discussion text")
	}
	var result *model.Claim
	e := c.Update(func(s *model.State) error {
		o := s.Options[id]
		if o == nil {
			return model.Err("NOT_FOUND", "Option %s", id)
		}
		t := s.Tasks[o.TaskID]
		if t.Status == "CLOSED" {
			return model.Err("INVALID_STATE", "CLOSED Task")
		}
		payload, _ := json.Marshal(discussionText{Text: text})
		var e error
		result, e = addClaim(s, t, c.Operator(), "OPTION", id, "discussion.comment", payload, t.SHA, t.Revision)
		if e == nil {
			s.Touch(t.ID)
		}
		return e
	})
	return result, e
}

func (c *Core) DiscussOption(id, text string) (*DiscussionRequest, error) {
	if e := c.Operator().Require("option.discuss", "claim.publish"); e != nil {
		return nil, e
	}
	if strings.TrimSpace(text) == "" {
		return nil, model.Err("SCHEMA_INVALID", "empty discussion text")
	}
	var result *DiscussionRequest
	e := c.enqueueOption(id, func(s *model.State, o *model.Option) error {
		if e := c.Operator().Require("option.discuss", "claim.publish"); e != nil {
			return e
		}
		t := s.Tasks[o.TaskID]
		if s.Accounts[t.ID].Remaining <= 0 {
			return model.Err("INSUFFICIENT_RESOURCE", "Task root has no remaining resource")
		}
		payload, _ := json.Marshal(discussionText{Text: text})
		claim, e := addClaim(s, t, c.Operator(), "OPTION", id, "discussion.comment", payload, t.SHA, t.Revision)
		if e != nil {
			return e
		}
		s.Requests = append(s.Requests, &model.Step{RequestID: model.ID(), TaskID: t.ID, OptionID: id, Operation: "discussion", TargetType: "OPTION", TargetID: id})
		s.Audit(t.ID, "OPTION_DISCUSSION_REQUESTED", id)
		s.Touch(t.ID)
		result = &DiscussionRequest{Comment: claim, Queued: true}
		return nil
	})
	return result, e
}

func (c *Core) OptionThread(id string) (*OptionThread, error) {
	s, e := c.Read()
	if e != nil {
		return nil, e
	}
	o := s.Options[id]
	if o == nil {
		return nil, model.Err("NOT_FOUND", "Option %s", id)
	}
	result := &OptionThread{Option: o, Messages: []*model.Claim{}}
	for _, claim := range s.Claims {
		if claim.SubjectType == "OPTION" && claim.SubjectID == id && (claim.Type == "discussion.comment" || claim.Type == "discussion.reply") {
			result.Messages = append(result.Messages, claim)
		}
	}
	sort.Slice(result.Messages, func(i, j int) bool {
		a, b := result.Messages[i], result.Messages[j]
		if a.Created == b.Created {
			return a.ID < b.ID
		}
		return a.Created < b.Created
	})
	return result, nil
}
