package core

import (
	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

func qualifyingClaim(card config.Card, kind, typ string) bool {
	return typ == "fulfilled" && (kind == "TASK" && card.Has("task.complete") || kind == "OPTION" && card.Has("option.complete"))
}

// beginControl acquires before checking state. A prior canceled/failed request
// cannot be implicitly replaced even after its OS gate has been released.
func (c *Core) beginControl(taskID string) (*model.State, func(), error) {
	unlock, e := c.LockTaskControl(taskID)
	if e != nil {
		return nil, nil, e
	}
	s, e := c.Read()
	if e == nil && s.Cancellations[taskID] {
		e = model.Err("BLOCKED", "Task cancellation requires explicit recover")
	}
	if e != nil {
		unlock()
		return nil, nil, e
	}
	return s, unlock, nil
}
