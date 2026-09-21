//go:build !windows

package core

import "scp-harness/internal/model"

func (c *Core) LockTaskControl(taskID string) (func(), error) {
	return nil, model.Err("BLOCKED", "Task control requires the Windows host")
}
