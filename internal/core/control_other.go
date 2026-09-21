//go:build !windows

package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"syscall"

	"scp-harness/internal/model"
)

func (c *Core) LockTaskControl(taskID string) (func(), error) {
	// Keep the lock inode after release: unlinking it could let a contender
	// acquire a second inode while another process still holds the first.
	path := fmt.Sprintf("%s.control-%x", c.Config.Database, sha256.Sum256([]byte(taskID)))
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, model.Err("BLOCKED", "Task control lock unavailable: %v", e)
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, model.Err("BLOCKED", "Task control operation already in progress: %v", e)
	}
	return func() { f.Close() }, nil
}
