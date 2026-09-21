package gitrepo

import (
	"archive/tar"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/model"
	"scp-harness/internal/wsl"
)

type Git struct {
	Config *config.Config
	Calls  map[string]int
}

func (g *Git) invoke(ctx context.Context, category string, args, env []string, in io.Reader, out io.Writer, cap int64) (boundedexec.Result, error) {
	if g.Calls == nil {
		g.Calls = map[string]int{}
	}
	g.Calls[category]++
	slog.Debug("Core Git invocation", "category", category, "argv", args)
	r, e := boundedexec.Run(ctx, boundedexec.Command{Argv: args, Env: env, ReplaceEnv: category == "export_tree" || category == "export_attr_inspection", Stdin: in, StdoutSink: out, Timeout: time.Duration(g.Config.Limits.ProcessMS) * time.Millisecond, MaxStdout: cap, MaxStderr: g.Config.Limits.Stderr})
	if e != nil || r.TimedOut || r.Canceled {
		return r, model.Err("REPOSITORY_UNAVAILABLE", "Git mechanism: %v: %s", e, r.Stderr)
	}
	if r.OutputExceeded {
		return r, model.Err("LIMIT_EXCEEDED", "Git output cap")
	}
	return r, nil
}
func argv(repo string, args ...string) []string {
	return append([]string{"git.exe", "-c", "core.autocrlf=false", "-c", "core.safecrlf=false", "-c", "core.hooksPath=NUL", "-C", repo}, args...)
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (g *Git) Resolve(ctx context.Context, repo, ref string) (string, error) {
	return g.resolve(ctx, repo, ref, "resolve_ref")
}
func (g *Git) resolve(ctx context.Context, repo, ref, category string) (string, error) {
	if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\r\n") {
		return "", model.Err("PRECONDITION_FAILED", "invalid Git ref")
	}
	r, e := g.invoke(ctx, category, argv(repo, "rev-parse", "--verify", ref+"^{commit}"), nil, nil, nil, g.Config.Limits.Stdout)
	if e != nil {
		return "", e
	}
	sha := strings.TrimSpace(string(r.Stdout))
	if r.ExitCode != 0 || !shaPattern.MatchString(sha) {
		return "", model.Err("REPOSITORY_UNAVAILABLE", "cannot resolve authoritative ref: %s", r.Stderr)
	}
	return sha, nil
}
func (g *Git) Export(ctx context.Context, repo, sha, root string) error {
	return g.Snapshot(ctx, repo, sha, root, "")
}
func (g *Git) Snapshot(ctx context.Context, repo, sha, root, keep string) (result error) {
	if !shaPattern.MatchString(sha) {
		return model.Err("CORE_INCONSISTENT", "invalid exact snapshot SHA")
	}
	env, metadata, e := g.archiveEnvironment(repo, filepath.Dir(root))
	if metadata != "" {
		defer func() {
			if cleanup := artifact.Discard(metadata, g.Config.Limits); cleanup != nil && result == nil {
				result = model.Err("REPOSITORY_UNAVAILABLE", "archive environment cleanup: %v", cleanup)
			}
		}()
	}
	if e != nil {
		return e
	}
	if e = g.inspectArchiveAttributes(ctx, repo, sha, env); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(root), "export-*.tar")
	if e != nil {
		return artifact.StorageError("Git export temp", e)
	}
	defer os.Remove(f.Name())
	defer f.Close()
	r, e := g.invoke(ctx, "export_tree", archiveArgs(repo, "archive", "--format=tar", sha), env, nil, f, g.Config.Limits.Export)
	if e != nil {
		return e
	}
	if r.ExitCode != 0 {
		return model.Err("REPOSITORY_UNAVAILABLE", "Git export: %s", r.Stderr)
	}
	if _, e = f.Seek(0, 0); e != nil {
		return artifact.StorageError("export seek", e)
	}
	if e = artifact.Extract(f, root, g.Config.Limits); e != nil {
		return e
	}
	if keep != "" {
		if _, e = f.Seek(0, 0); e != nil {
			return artifact.StorageError("snapshot seek", e)
		}
		dst, e := os.OpenFile(keep, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return artifact.StorageError("snapshot archive", e)
		}
		_, e = io.Copy(dst, f)
		ce := dst.Close()
		if e != nil {
			return artifact.StorageError("snapshot archive copy", e)
		}
		if ce != nil {
			return artifact.StorageError("snapshot close", ce)
		}
	}
	return nil
}
func (g *Git) Synthetic(ctx context.Context) error {
	runner := wsl.Runner{Config: g.Config}
	root := g.Config.WSL.Root + "/workspace"
	for _, args := range [][]string{{"init"}, {"add", "-A", "-f"}, {"-c", "user.name=SCP", "-c", "user.email=scp@local", "-c", "core.hooksPath=/dev/null", "commit", "--allow-empty", "-m", "SCP base"}} {
		cmd := runner.Args("root", append([]string{"git", "-C", root}, args...)...)
		r, e := g.invoke(ctx, "synthetic_git", cmd, nil, nil, nil, g.Config.Limits.Stdout)
		if e != nil {
			return e
		}
		if r.ExitCode != 0 {
			return model.Err("RUNNER_UNAVAILABLE", "synthetic Git: %s", r.Stderr)
		}
	}
	return nil
}

// Construct does not update a ref. The caller durably writes PREPARED before CAS.
func (g *Git) Construct(ctx context.Context, t model.Task, a model.Artifact, tree string) (string, error) {
	current, e := g.resolve(ctx, t.RepoPath, t.RepoRef, "promotion")
	if e != nil {
		return "", e
	}
	if current != a.BaseSHA {
		return "", model.Err("PRECONDITION_CHANGED", "target moved from Artifact base")
	}
	index := filepath.Join(filepath.Dir(tree), "index-"+model.ID())
	defer os.Remove(index)
	env := []string{"GIT_INDEX_FILE=" + index, "GIT_WORK_TREE=" + tree, "GIT_AUTHOR_NAME=SCP", "GIT_AUTHOR_EMAIL=scp@local", "GIT_COMMITTER_NAME=SCP", "GIT_COMMITTER_EMAIL=scp@local", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=NUL"}
	call := func(args ...string) (string, error) {
		r, e := g.invoke(ctx, "promotion", argv(t.RepoPath, args...), env, nil, nil, g.Config.Limits.Stdout)
		if e != nil {
			return "", e
		}
		if r.ExitCode != 0 {
			return "", model.Err("REPOSITORY_UNAVAILABLE", "promotion Git: %s", r.Stderr)
		}
		return strings.TrimSpace(string(r.Stdout)), nil
	}
	if _, e = call("read-tree", a.BaseSHA); e != nil {
		return "", e
	}
	if _, e = call("add", "-A", "-f"); e != nil {
		return "", e
	}
	// Set each blob's exact captured mode and content identity in one bounded
	// index operation. NTFS cannot represent Unix executable bits. If a local
	// Git filter changed bytes, write-tree fails rather than promoting different
	// content from the submitted Artifact.
	indexInfo, e := os.CreateTemp(filepath.Dir(tree), "index-info-*")
	if e != nil {
		return "", artifact.StorageError("index metadata", e)
	}
	defer indexInfo.Close()
	defer os.Remove(indexInfo.Name())
	if e = writeIndexInfo(a, indexInfo, g.Config.Limits); e != nil {
		return "", e
	}
	if _, e = indexInfo.Seek(0, 0); e != nil {
		return "", artifact.StorageError("index metadata seek", e)
	}
	r, e := g.invoke(ctx, "promotion", argv(t.RepoPath, "update-index", "-z", "--index-info"), env, indexInfo, nil, g.Config.Limits.Stdout)
	if e != nil {
		return "", e
	}
	if r.ExitCode != 0 {
		return "", model.Err("REPOSITORY_UNAVAILABLE", "index metadata: %s", r.Stderr)
	}
	treeSHA, e := call("write-tree")
	if e != nil {
		return "", e
	}
	if !shaPattern.MatchString(treeSHA) {
		return "", model.Err("CORE_INCONSISTENT", "invalid constructed tree")
	}
	sha, e := call("commit-tree", treeSHA, "-p", a.BaseSHA, "-m", "SCP artifact "+a.ID)
	if e != nil {
		return "", e
	}
	if !shaPattern.MatchString(sha) {
		return "", model.Err("CORE_INCONSISTENT", "invalid constructed commit")
	}
	return sha, nil
}
func writeIndexInfo(a model.Artifact, w io.Writer, l config.Limits) error {
	f, e := os.Open(a.BlobPath)
	if e != nil {
		return artifact.StorageError("Artifact index source", e)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var count, total int64
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return model.Err("CORE_INCONSISTENT", "Artifact tar: %v", e)
		}
		if h.Typeflag == tar.TypeDir || h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return model.Err("CORE_INCONSISTENT", "unsupported Artifact index entry")
		}
		name, e := artifact.SafeName(h.Name, l.Path)
		if e != nil {
			return e
		}
		count++
		if count > l.Files || h.Size < 0 || h.Size > l.Single || h.Size > l.Bytes-total {
			return model.Err("LIMIT_EXCEEDED", "index metadata bounds")
		}
		total += h.Size
		hash := sha1.New()
		fmt.Fprintf(hash, "blob %d%c", h.Size, 0)
		if _, e = io.CopyN(hash, tr, h.Size); e != nil {
			return artifact.StorageError("index source read", e)
		}
		mode := "100644"
		if h.Mode&0111 != 0 {
			mode = "100755"
		}
		if _, e = fmt.Fprintf(w, "%s %x\t%s%c", mode, hash.Sum(nil), name, 0); e != nil {
			return artifact.StorageError("index metadata write", e)
		}
	}
}
func (g *Git) CAS(ctx context.Context, repo, ref, old, new string) error {
	if !shaPattern.MatchString(old) || !shaPattern.MatchString(new) {
		return model.Err("CORE_INCONSISTENT", "invalid CAS SHA")
	}
	r, e := g.invoke(ctx, "promotion", argv(repo, "update-ref", ref, new, old), nil, nil, nil, g.Config.Limits.Stdout)
	if e != nil {
		return e
	}
	if r.ExitCode == 0 {
		return nil
	}
	current, e := g.resolve(ctx, repo, ref, "promotion")
	if e != nil {
		return e
	}
	if current != old {
		return model.Err("PRECONDITION_CHANGED", "CAS precondition changed")
	}
	return model.Err("REPOSITORY_UNAVAILABLE", "Git CAS mechanism: %s", r.Stderr)
}
