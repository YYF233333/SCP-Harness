package artifact

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

// Discard is only for Core-owned transient trees. It never reads file contents
// or follows links. Admission limits still apply to Pack/Extract, not to unlink.
// A batch bounds completed deletions; the whole operation has a finite deadline.
func Discard(root string, l config.Limits) error {
	if l.Files <= 0 || l.ProcessMS <= 0 {
		return model.Err("CORE_INCONSISTENT", "invalid cleanup execution bounds")
	}
	absolute, e := filepath.Abs(root)
	if e != nil {
		return StorageError("temporary cleanup path", e)
	}
	deadline := time.Now().Add(time.Duration(l.ProcessMS) * time.Millisecond)
	for {
		done, e := discardBatch(absolute, l.Files, deadline)
		if e != nil {
			return StorageError("temporary cleanup", e)
		}
		if done {
			return nil
		}
	}
}

func discardBatch(root string, budget int64, deadline time.Time) (bool, error) {
	current := root
	var removed int64
	for removed < budget {
		if !time.Now().Before(deadline) {
			return false, model.Err("LIMIT_EXCEEDED", "transient cleanup deadline exceeded; completed deletions are retained")
		}
		info, e := os.Lstat(current)
		if os.IsNotExist(e) {
			if current == root {
				return true, nil
			}
			current = filepath.Dir(current)
			continue
		}
		if e != nil {
			return false, e
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			if e = os.Remove(current); e != nil {
				return false, e
			}
			removed++
			if current == root {
				return true, nil
			}
			current = filepath.Dir(current)
			continue
		}
		directory, e := os.Open(current)
		if e != nil {
			return false, e
		}
		entries, e := directory.ReadDir(1)
		closeErr := directory.Close()
		if e != nil && e != io.EOF {
			return false, e
		}
		if closeErr != nil {
			return false, closeErr
		}
		if len(entries) == 0 {
			if e = os.Remove(current); e != nil {
				return false, e
			}
			removed++
			if current == root {
				return true, nil
			}
			current = filepath.Dir(current)
		} else {
			// ReadDir(1) keeps enumeration bounded even for an overfull directory.
			current = filepath.Join(current, entries[0].Name())
		}
	}
	return false, nil
}
