package artifact

import (
	"os"
	"path/filepath"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

// Discard removes a caller-selected Core-owned disposable tree. It validates a
// bounded inventory first and rejects links/reparse entries before any delete.
func Discard(root string, l config.Limits) error {
	if _, e := os.Lstat(root); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return StorageError("temporary tree stat", e)
	}
	paths := []string{}
	var total int64
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return model.Err("CORE_INCONSISTENT", "temporary tree contains link")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		if int64(len(paths)) >= l.Files || int64(len(rel)) > l.Path {
			return model.Err("LIMIT_EXCEEDED", "temporary inventory bound")
		}
		if info.Mode().IsRegular() {
			if info.Size() > l.Single || info.Size() > l.Bytes-total {
				return model.Err("LIMIT_EXCEEDED", "temporary byte bound")
			}
			total += info.Size()
		} else if !d.IsDir() {
			return model.Err("CORE_INCONSISTENT", "temporary special file")
		}
		paths = append(paths, p)
		return nil
	})
	if e != nil {
		return StorageError("temporary inventory", e)
	}
	for i := len(paths) - 1; i >= 0; i-- {
		if e = os.Remove(paths[i]); e != nil {
			return StorageError("temporary cleanup", e)
		}
	}
	return nil
}
