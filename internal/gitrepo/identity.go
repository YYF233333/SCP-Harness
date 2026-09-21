package gitrepo

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"scp-harness/internal/model"
)

// Identity normalizes subdirectories, junctions and Git common directories for
// the single ACTIVE binding. It reads only Git's location metadata, not history.
func Identity(path string) (string, string, error) {
	root, e := filepath.Abs(path)
	if e != nil {
		return "", "", model.Err("REPOSITORY_UNAVAILABLE", "repository path: %v", e)
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return "", "", model.Err("REPOSITORY_UNAVAILABLE", "repository path: %v", e)
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return "", "", model.Err("REPOSITORY_UNAVAILABLE", "repository must be a directory")
	}
	read := func(p string) (string, error) {
		f, e := os.Open(p)
		if e != nil {
			return "", e
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, 65537))
		if e != nil {
			return "", e
		}
		if len(b) > 65536 {
			return "", model.Err("LIMIT_EXCEEDED", "Git location metadata bound")
		}
		return strings.TrimSpace(string(b)), nil
	}
	for p := root; ; p = filepath.Dir(p) {
		gitdir := filepath.Join(p, ".git")
		if info, e = os.Stat(gitdir); e == nil {
			root = p
			if !info.IsDir() {
				text, e := read(gitdir)
				if e != nil || !strings.HasPrefix(text, "gitdir: ") {
					return "", "", model.Err("REPOSITORY_UNAVAILABLE", "invalid Git directory pointer")
				}
				gitdir = strings.TrimPrefix(text, "gitdir: ")
				if !filepath.IsAbs(gitdir) {
					gitdir = filepath.Join(root, gitdir)
				}
			}
			if common, e := read(filepath.Join(gitdir, "commondir")); e == nil {
				if filepath.IsAbs(common) {
					gitdir = common
				} else {
					gitdir = filepath.Join(gitdir, common)
				}
			} else if !os.IsNotExist(e) {
				return "", "", model.Err("REPOSITORY_UNAVAILABLE", "Git common directory: %v", e)
			}
			gitdir, e = filepath.EvalSymlinks(gitdir)
			if e != nil {
				return "", "", model.Err("REPOSITORY_UNAVAILABLE", "Git identity: %v", e)
			}
			return root, identityPath(gitdir), nil
		}
		if _, e = os.Stat(filepath.Join(p, "HEAD")); e == nil {
			if info, e = os.Stat(filepath.Join(p, "objects")); e == nil && info.IsDir() {
				return p, identityPath(p), nil
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return "", "", model.Err("REPOSITORY_UNAVAILABLE", "no local Git repository")
}
