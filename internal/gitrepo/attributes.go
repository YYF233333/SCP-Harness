package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"scp-harness/internal/model"
)

// Archive and its one preflight share private Git metadata and the original
// object directory. No repository files/objects are copied, no ref is checked
// out, and no additional Git subprocess initializes this environment.
func (g *Git) archiveEnvironment(repo, parent string) ([]string, string, error) {
	_, common, e := Identity(repo)
	if e != nil {
		return nil, "", model.Err("REPOSITORY_UNAVAILABLE", "archive repository identity: %v", e)
	}
	metadata, e := os.MkdirTemp(parent, "archive-git-*")
	if e != nil {
		return nil, "", model.Err("REPOSITORY_UNAVAILABLE", "cannot isolate archive metadata: %v", e)
	}
	for _, name := range []string{"objects", "refs", "info"} {
		if e = os.Mkdir(filepath.Join(metadata, name), 0700); e != nil {
			return nil, metadata, model.Err("REPOSITORY_UNAVAILABLE", "cannot isolate archive directory: %v", e)
		}
	}
	for name, body := range map[string]string{"HEAD": "ref: refs/heads/scp-snapshot\n", "config": "[core]\nrepositoryformatversion = 0\nbare = true\n"} {
		if e = os.WriteFile(filepath.Join(metadata, name), []byte(body), 0600); e != nil {
			return nil, metadata, model.Err("REPOSITORY_UNAVAILABLE", "cannot isolate archive configuration: %v", e)
		}
	}
	// Archive otherwise performs checkout conversion (including Windows CRLF)
	// even for ordinary text attributes. This fixed Core-owned policy preserves
	// blob bytes. It contains neither archive-attribute identifier and cannot be
	// influenced by the original repository's info/attributes or configuration.
	if e = os.WriteFile(filepath.Join(metadata, "info", "attributes"), []byte(archiveBytePolicy), 0600); e != nil {
		return nil, metadata, model.Err("REPOSITORY_UNAVAILABLE", "cannot establish archive byte policy: %v", e)
	}
	// Remove all ambient Git overrides, including injected command config,
	// pathspec modes, replacement refs and alternative attribute sources.
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env,
		"GIT_DIR="+metadata, "GIT_COMMON_DIR="+metadata,
		"GIT_OBJECT_DIRECTORY="+filepath.Join(common, "objects"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=NUL", "GIT_CONFIG_GLOBAL=NUL",
		"GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1",
	)
	return env, metadata, nil
}

const archiveBytePolicy = "* -text -eol -ident -filter -working-tree-encoding\n"

func archiveArgs(repo string, args ...string) []string {
	return argv(repo, append([]string{"-c", "core.attributesFile=NUL"}, args...)...)
}

// This is a textual feature ban, not an attribute evaluator. Only tracked
// .gitattributes blobs in this exact commit are searched, including binary
// content and comments. Quiet grep provides a small, explicit exit protocol.
func (g *Git) inspectArchiveAttributes(ctx context.Context, repo, sha string, env []string) error {
	args := archiveArgs(repo, "grep", "--quiet", "--fixed-strings", "--text", "--no-textconv", "--no-recurse-submodules", "--threads=1",
		"-e", "export-ignore", "-e", "export-subst", sha, "--", ":(top,literal).gitattributes", ":(top,glob)**/.gitattributes")
	r, e := g.invoke(ctx, "export_attr_inspection", args, env, nil, nil, g.Config.Limits.Stdout)
	if e != nil {
		return model.Err("REPOSITORY_UNAVAILABLE", "archive-attribute inspection of %s: %v", sha, e)
	}
	return archiveInspectionResult(r.ExitCode, r.Stdout, r.Stderr)
}

func archiveInspectionResult(exit int, stdout, stderr []byte) error {
	// Any unexpected output makes a claimed no-match inconclusive. A match or
	// an execution/protocol error never authorizes archive.
	if len(stdout) != 0 || len(stderr) != 0 {
		return model.Err("REPOSITORY_UNAVAILABLE", "unexpected archive-attribute inspection output")
	}
	switch exit {
	case 1:
		return nil
	case 0:
		return model.Err("REPOSITORY_UNAVAILABLE", "exact tree contains an unsupported archive attribute identifier in .gitattributes")
	default:
		return model.Err("REPOSITORY_UNAVAILABLE", "archive-attribute inspection failed (exit %d)", exit)
	}
}
