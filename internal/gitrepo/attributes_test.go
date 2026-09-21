package gitrepo

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

func attrGit(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	r, e := boundedexec.Run(context.Background(), boundedexec.Command{Argv: append([]string{Executable(), "-C", repo}, args...), Timeout: 30 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20})
	if e != nil || r.ExitCode != 0 {
		t.Fatalf("Git fixture %v: %v %s", args, e, r.Stderr)
	}
	return r.Stdout
}
func attrFixture(t *testing.T, attributes map[string]string) (*Git, string, string, map[string]string) {
	t.Helper()
	cfg, e := config.Load(filepath.Join("..", "..", "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	repo := t.TempDir()
	attrGit(t, repo, "init", "-b", "main")
	files := map[string]string{"README.md": "base\n", "dir/data.txt": "tracked\n", "value.txt": "adjacent:$Format:%H$$Format:%h$:end\n"}
	for path, value := range attributes {
		files[path] = value
	}
	for path, value := range files {
		full := filepath.Join(repo, filepath.FromSlash(path))
		if e = os.MkdirAll(filepath.Dir(full), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(full, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	attrGit(t, repo, "-c", "core.autocrlf=false", "add", "-A")
	attrGit(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@local", "commit", "-m", "attribute fixture")
	sha := strings.TrimSpace(string(attrGit(t, repo, "rev-parse", "HEAD")))
	return &Git{Config: cfg}, repo, sha, files
}
func treeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	e := filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return files
}
func exportRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tree")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	return root
}

func TestR4ExactSnapshotAttributes(t *testing.T) {
	cases := []struct{ name, path, body string }{
		{"root_ignore", ".gitattributes", "README.md export-ignore\n"},
		{"nested_ignore", "dir/.gitattributes", "data.txt export-ignore\n"},
		{"root_subst_adjacent_tokens", ".gitattributes", "value.txt export-subst\n"},
		{"nested_subst", "dir/.gitattributes", "data.txt export-subst\n"},
		{"directory_pattern", ".gitattributes", "dir/ export-ignore\n"},
		{"unset", ".gitattributes", "* -export-ignore\n"},
		{"unspecified", ".gitattributes", "* !export-ignore\n"},
		{"value", ".gitattributes", "* export-ignore=value\n"},
		{"value_unset", ".gitattributes", "* export-ignore=unset\n"},
		{"value_unspecified", ".gitattributes", "* export-ignore=unspecified\n"},
		{"macro", ".gitattributes", "[attr]hidden export-ignore\nREADME.md hidden\n"},
		{"comment", "dir/.gitattributes", "# export-subst is not used\n"},
		{"unmatched", ".gitattributes", "nonexistent export-ignore\n"},
		{"overridden", ".gitattributes", "* export-ignore\n* -export-ignore\n"},
		{"binary_text", "dir/.gitattributes", "\x00export-ignore\x00\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, repo, sha, _ := attrFixture(t, map[string]string{tc.path: tc.body})
			root := exportRoot(t)
			e := g.Export(context.Background(), repo, sha, root)
			if model.Code(e) != "REPOSITORY_UNAVAILABLE" {
				t.Fatalf("unsupported identifiers were not rejected: %v", e)
			}
			if g.Calls["export_attr_inspection"] != 1 || g.Calls["export_tree"] != 0 {
				t.Fatalf("rejected snapshot consumed archive or wrong budget: %+v", g.Calls)
			}
			if len(treeFiles(t, root)) != 0 {
				t.Fatal("rejected snapshot materialized files")
			}
		})
	}
	for _, name := range []string{"no_attributes", "ordinary_attributes"} {
		t.Run(name, func(t *testing.T) {
			attrs := map[string]string{}
			if name == "ordinary_attributes" {
				attrs[".gitattributes"] = "*.txt text eol=crlf ident\n"
				attrs["dir/.gitattributes"] = "*.txt -diff\n"
			}
			g, repo, sha, want := attrFixture(t, attrs)
			root := exportRoot(t)
			if e := g.Export(context.Background(), repo, sha, root); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(treeFiles(t, root), want) {
				t.Fatal("snapshot is not exact")
			}
			if g.Calls["export_attr_inspection"] != 1 || g.Calls["export_tree"] != 1 {
				t.Fatalf("normal snapshot budget: %+v", g.Calls)
			}
			// An unchanged export promoted through the real construction/CAS path
			// must have the identical Git tree, including adjacent format tokens.
			tarFile, e := os.CreateTemp(t.TempDir(), "snapshot-*.tar")
			if e != nil {
				t.Fatal(e)
			}
			if e = artifact.Pack(root, tarFile, g.Config.Limits); e != nil {
				t.Fatal(e)
			}
			if e = tarFile.Close(); e != nil {
				t.Fatal(e)
			}
			newSHA, e := g.Construct(context.Background(), model.Task{RepoPath: repo, RepoRef: "refs/heads/main"}, model.Artifact{ID: model.ID(), BaseSHA: sha, BlobPath: tarFile.Name()}, root)
			if e != nil {
				t.Fatal(e)
			}
			if e = g.CAS(context.Background(), repo, "refs/heads/main", sha, newSHA); e != nil {
				t.Fatal(e)
			}
			oldTree := attrGit(t, repo, "rev-parse", sha+"^{tree}")
			newTree := attrGit(t, repo, "rev-parse", newSHA+"^{tree}")
			if !bytes.Equal(oldTree, newTree) {
				t.Fatalf("no-op round-trip changed tree: %s -> %s", oldTree, newTree)
			}
			if g.Calls["promotion"] > 8 {
				t.Fatal("promotion budget grew")
			}
		})
	}
}

func TestR4ExactSHAIgnoresWorktreeAndIndex(t *testing.T) {
	for _, committedBan := range []bool{false, true} {
		t.Run(map[bool]string{false: "mutable_ban_is_not_source", true: "mutable_removal_cannot_hide_ban"}[committedBan], func(t *testing.T) {
			body := "*.txt text\n"
			if committedBan {
				body = "* export-ignore\n"
			}
			g, repo, sha, want := attrFixture(t, map[string]string{".gitattributes": body})
			replacement := "* export-ignore\n"
			if committedBan {
				replacement = "*.txt text\n"
			}
			if e := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte(replacement), 0600); e != nil {
				t.Fatal(e)
			}
			attrGit(t, repo, "add", ".gitattributes")
			root := exportRoot(t)
			e := g.Export(context.Background(), repo, sha, root)
			if committedBan {
				if model.Code(e) != "REPOSITORY_UNAVAILABLE" || g.Calls["export_tree"] != 0 {
					t.Fatal("mutable state hid exact-tree ban")
				}
			} else if e != nil || !reflect.DeepEqual(treeFiles(t, root), want) {
				t.Fatalf("mutable attributes changed snapshot: %v; got=%q want=%q", e, treeFiles(t, root), want)
			}
		})
	}
}

func TestR4NonTreeAttributeIsolation(t *testing.T) {
	g, repo, sha, want := attrFixture(t, nil)
	poison := "dir/ export-ignore\nvalue.txt export-subst\n"
	info := filepath.Join(repo, ".git", "info", "attributes")
	if e := os.WriteFile(info, []byte(poison), 0600); e != nil {
		t.Fatal(e)
	}
	// First prove this is a real archive-changing source, not an inert fixture.
	unsafe := attrGit(t, repo, "archive", "--format=tar", sha)
	tr := tar.NewReader(bytes.NewReader(unsafe))
	unsafeFiles := map[string]string{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			b, e := io.ReadAll(tr)
			if e != nil {
				t.Fatal(e)
			}
			unsafeFiles[h.Name] = string(b)
		}
	}
	if _, ok := unsafeFiles["dir/data.txt"]; ok || unsafeFiles["value.txt"] == want["value.txt"] {
		t.Fatal("info/attributes fixture did not affect native archive")
	}
	global := filepath.Join(t.TempDir(), "attributes")
	if e := os.WriteFile(global, []byte(poison), 0600); e != nil {
		t.Fatal(e)
	}
	configPath := filepath.Join(t.TempDir(), "gitconfig")
	configText := "[core]\nattributesFile = " + filepath.ToSlash(global) + "\n"
	if e := os.WriteFile(configPath, []byte(configText), 0600); e != nil {
		t.Fatal(e)
	}
	attrGit(t, repo, "config", "core.attributesFile", global)
	t.Setenv("GIT_CONFIG_GLOBAL", configPath)
	t.Setenv("GIT_CONFIG_SYSTEM", configPath)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.attributesFile")
	t.Setenv("GIT_CONFIG_VALUE_0", global)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.attributesFile="+filepath.ToSlash(global)+"'")
	t.Setenv("GIT_LITERAL_PATHSPECS", "1")
	root := exportRoot(t)
	if e := g.Export(context.Background(), repo, sha, root); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(treeFiles(t, root), want) {
		t.Fatal("non-tree attributes changed archive")
	}
	if g.Calls["export_attr_inspection"] != 1 || g.Calls["export_tree"] != 1 {
		t.Fatal("isolation added Git calls")
	}
	got, e := os.ReadFile(info)
	if e != nil || string(got) != poison {
		t.Fatal("isolation modified authoritative metadata")
	}
	env, metadata, e := g.archiveEnvironment(repo, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer artifact.Discard(metadata, g.Config.Limits)
	vars := map[string]string{}
	for _, v := range env {
		key, value, _ := strings.Cut(v, "=")
		vars[key] = value
	}
	for key, value := range map[string]string{"GIT_ATTR_NOSYSTEM": "1", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": os.DevNull} {
		if vars[key] != value {
			t.Fatalf("missing explicit isolation %s", key)
		}
	}
	privatePolicy, e := os.ReadFile(filepath.Join(vars["GIT_DIR"], "info", "attributes"))
	if e != nil || string(privatePolicy) != archiveBytePolicy || bytes.Contains(privatePolicy, []byte("export-ignore")) || bytes.Contains(privatePolicy, []byte("export-subst")) {
		t.Fatal("private Git metadata can provide uncontrolled archive attributes")
	}
	if _, ok := vars["GIT_CONFIG_PARAMETERS"]; ok {
		t.Fatal("ambient command configuration retained")
	}
}

func TestR4InspectionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		exit     int
		out, err string
	}{{"match", 0, "", ""}, {"failure", 128, "", ""}, {"unsupported", 129, "", ""}, {"unexpected_stdout", 1, "unexpected", ""}, {"warning_on_nomatch", 1, "", "warning"}, {"invalid_negative_exit", -1, "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			if e := archiveInspectionResult(tc.exit, []byte(tc.out), []byte(tc.err)); model.Code(e) != "REPOSITORY_UNAVAILABLE" {
				t.Fatalf("inconclusive inspection allowed: %v", e)
			}
		})
	}
	if e := archiveInspectionResult(1, nil, nil); e != nil {
		t.Fatal("clean no-match denied")
	}
	g, repo, _, _ := attrFixture(t, nil)
	root := exportRoot(t)
	e := g.Export(context.Background(), repo, strings.Repeat("0", 40), root)
	if model.Code(e) != "REPOSITORY_UNAVAILABLE" || g.Calls["export_attr_inspection"] != 1 || g.Calls["export_tree"] != 0 {
		t.Fatalf("actual Git failure was not closed: %v %+v", e, g.Calls)
	}
	g, repo, _, _ = attrFixture(t, nil)
	g.Config.Limits.Stderr = 1
	root = exportRoot(t)
	if e = g.Export(context.Background(), repo, strings.Repeat("0", 40), root); model.Code(e) != "REPOSITORY_UNAVAILABLE" || g.Calls["export_tree"] != 0 {
		t.Fatalf("inspection output cap did not fail closed: %v", e)
	}
	g, repo, sha, _ := attrFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root = exportRoot(t)
	if e = g.Export(ctx, repo, sha, root); model.Code(e) != "REPOSITORY_UNAVAILABLE" || g.Calls["export_tree"] != 0 {
		t.Fatalf("canceled inspection not closed: %v", e)
	}
}
