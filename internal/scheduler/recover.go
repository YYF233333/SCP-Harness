package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/ledger"
	"scp-harness/internal/model"
)

type Recovery struct {
	Attempts []string `json:"recovered_attempt_ids"`
	Journals int      `json:"promotion_journal_reconciled"`
	Stale    bool     `json:"stale_lock_removed"`
}

func (s *Scheduler) Recover(ctx context.Context) (Recovery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Recovery{Attempts: []string{}}
	c := s.Core
	lock := filepath.Join(filepath.Dir(c.Config.Database), "run.lock")
	owner, e := os.ReadFile(filepath.Join(lock, "owner"))
	if e == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(owner)))
		if boundedexec.Alive(pid) {
			return result, model.Err("BLOCKED", "scheduler process still alive")
		}
	}
	state, e := c.Read()
	if e != nil {
		return result, e
	}
	if state.SlotPID != 0 && boundedexec.Alive(state.SlotPID) {
		return result, model.Err("BLOCKED", "execution owner still alive")
	}
	if e = c.Store.Update(func(st *model.State) error {
		if st.SlotPID != 0 && boundedexec.Alive(st.SlotPID) {
			return model.Err("BLOCKED", "execution/recovery owner still alive")
		}
		st.Slot = model.Slot{State: "BUSY"}
		st.SlotOwner = s.Owner
		st.SlotPID = os.Getpid()
		return nil
	}); e != nil {
		return result, e
	}
	defer c.Release(s.Owner)
	if e = c.Runner.Terminate(ctx); e != nil {
		_ = c.Block("RUNNER_UNAVAILABLE", "", "", e.Error())
		return result, e
	}
	for _, a := range state.Attempts {
		if !a.Active() {
			continue
		}
		var captured *model.Artifact
		profile := c.Config.Profile(a.Operation)
		p := state.Pending[a.TaskID]
		if p == nil {
			p = &model.Step{TaskID: a.TaskID, OptionID: a.AnchorID, Operation: a.Operation, TargetType: a.TargetType, TargetID: a.TargetID}
		}
		if profile.Workspace == "writable" {
			var out bytes.Buffer
			if e = c.Runner.Files(ctx, "exists", nil, nil, &out, 128); e != nil {
				return result, e
			}
			if strings.TrimSpace(out.String()) == "yes" {
				dir, e := s.temp("recovery-" + model.ID())
				if e != nil {
					return result, e
				}
				captured, e = s.capture(a, *p, dir)
				if e != nil && model.Code(e) != "LIMIT_EXCEEDED" {
					return result, e
				}
			}
		}
		if e = c.Store.Update(func(st *model.State) error {
			v := st.Attempts[a.ID]
			if captured != nil {
				st.Artifacts[captured.ID] = captured
				v.ArtifactID = &captured.ID
			}
			if e := ledger.Settle(st, a.ID, 0, true); e != nil {
				return e
			}
			v.Status = "CRASHED"
			now := model.Now()
			v.Ended = &now
			reason := "CORE_RESTART"
			v.Reason = &reason
			delete(st.Interrupts, a.ID)
			delete(st.Pending, a.TaskID)
			if a.Operation == "option_generation" {
				st.Exploration[a.TaskID].Count++
			}
			if a.Operation == "merge_judge" {
				st.Exploration[a.TaskID].Judged = true
				st.Exploration[a.TaskID].Groups = [][]string{}
			}
			if a.Operation == "merge_synth" {
				st.Exploration[a.TaskID].GroupIndex++
			}
			st.Touch(a.TaskID)
			return nil
		}); e != nil {
			return result, e
		}
		result.Attempts = append(result.Attempts, a.ID)
	}
	// Protected tests are leases, never worker Attempts. Their interrupted test
	// step stays pending, and uncertain resource is fully charged.
	if e = c.Store.Update(func(st *model.State) error {
		for id, l := range st.Leases {
			if st.Attempts[id] != nil {
				return model.Err("CORE_INCONSISTENT", "unexpected Attempt lease during recovery")
			}
			task := l.TaskID
			if e := ledger.Settle(st, id, 0, true); e != nil {
				return e
			}
			st.Touch(task)
		}
		return nil
	}); e != nil {
		return result, e
	}
	state, e = c.Read()
	if e != nil {
		return result, e
	}
	for _, j := range state.Journals {
		if j.State != "PREPARED" {
			continue
		}
		t := state.Tasks[j.TaskID]
		sha, e := c.Git.Resolve(ctx, t.RepoPath, j.Ref)
		if e != nil {
			_ = c.Block(model.Code(e), t.ID, "", e.Error())
			return result, e
		}
		outcome := "CONFLICT"
		if sha == j.NewSHA {
			outcome = "APPLIED"
		} else if sha == j.OldSHA {
			if e = c.Git.CAS(ctx, t.RepoPath, j.Ref, j.OldSHA, j.NewSHA); e == nil {
				outcome = "APPLIED"
			} else if model.Code(e) != "PRECONDITION_CHANGED" {
				return result, e
			}
		}
		if e = c.Store.Update(func(st *model.State) error {
			st.Journals[j.ID].State = outcome
			if outcome == "APPLIED" {
				st.Tasks[j.TaskID].SHA = j.NewSHA
			}
			delete(st.Pending, j.TaskID)
			st.Audit(j.TaskID, "PROMOTION_RECOVERED", outcome)
			st.Touch(j.TaskID)
			return nil
		}); e != nil {
			return result, e
		}
		result.Journals++
	}
	// Only disposable trees/transfers are cleaned. Attempt inputs and process
	// logs remain audit evidence; Artifact storage is never garbage-collected.
	bounds, _ := json.Marshal(c.Config.Limits)
	if e = c.Runner.Files(ctx, "discard", []string{string(bounds)}, nil, nil, c.Config.Limits.Stdout); e != nil {
		return result, e
	}
	runtimeRoot := filepath.Join(filepath.Dir(c.Config.Database), "runtime")
	directory, de := os.Open(runtimeRoot)
	if de == nil {
		entries, re := directory.ReadDir(int(c.Config.Limits.Files + 1))
		directory.Close()
		if re != nil && re != io.EOF {
			return result, artifact.StorageError("recovery inventory", re)
		}
		if int64(len(entries)) > c.Config.Limits.Files {
			return result, model.Err("LIMIT_EXCEEDED", "recovery directory count")
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return result, model.Err("CORE_INCONSISTENT", "runtime symlink")
			}
			path := filepath.Join(runtimeRoot, entry.Name())
			if entry.IsDir() {
				for _, name := range []string{"workspace", "context", "tree", "captured"} {
					if e = artifact.Discard(filepath.Join(path, name), c.Config.Limits); e != nil {
						return result, e
					}
				}
			} else if strings.HasSuffix(entry.Name(), ".tar") || strings.HasPrefix(entry.Name(), "index-") {
				if e = os.Remove(path); e != nil {
					return result, artifact.StorageError("recovery temporary file", e)
				}
			}
		}
	} else if !os.IsNotExist(de) {
		return result, artifact.StorageError("recovery directory", de)
	}
	// Remove only the two exact scheduler lock paths after durable reconciliation.
	if _, e = os.Stat(lock); e == nil {
		if e = os.Remove(filepath.Join(lock, "owner")); e != nil && !os.IsNotExist(e) {
			return result, artifact.StorageError("stale lock owner", e)
		}
		if e = os.Remove(lock); e != nil {
			return result, artifact.StorageError("stale lock", e)
		}
		result.Stale = true
	}
	return result, nil
}
