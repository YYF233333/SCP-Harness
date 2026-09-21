package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scp-harness/internal/artifact"
	"scp-harness/internal/boundedexec"
	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

func fixture(t *testing.T, count int) (*Git, string) {
	t.Helper()
	cfg, e := config.Load(filepath.Join("..", "..", "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	repo := t.TempDir()
	run := func(in *bytes.Buffer, args ...string) {
		cmd := boundedexec.Command{Argv: append([]string{Executable(), "-C", repo}, args...), Timeout: 60 * time.Second, MaxStdout: 1 << 20, MaxStderr: 1 << 20}
		if in != nil {
			cmd.Stdin = in
		}
		r, e := boundedexec.Run(context.Background(), cmd)
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("fixture Git: %v %s", e, r.Stderr)
		}
	}
	run(nil, "init", "-b", "main")
	var stream bytes.Buffer
	fmt.Fprint(&stream, "blob\nmark :1\ndata 5\nbase\n\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&stream, "commit refs/heads/main\nmark :%d\ncommitter Fixture <fixture@local> %d +0000\ndata 1\nx\n", i+2, 1000000000+i)
		if i > 0 {
			fmt.Fprintf(&stream, "from :%d\n", i+1)
		}
		fmt.Fprint(&stream, "M 100644 :1 README.md\n\n")
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&stream, "reset refs/heads/branch-%d\nfrom :%d\n\n", i, count+1)
	}
	fmt.Fprint(&stream, "done\n")
	run(&stream, "fast-import", "--quiet")
	return &Git{Config: cfg}, repo
}
func TestHistoryIndependentBoundedGitAndCAS(t *testing.T) {
	for _, count := range []int{1, 100, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			g, repo := fixture(t, count)
			ctx := context.Background()
			sha, e := g.Resolve(ctx, repo, "refs/heads/main")
			if e != nil {
				t.Fatal(e)
			}
			tree := filepath.Join(t.TempDir(), "tree")
			if e = os.Mkdir(tree, 0700); e != nil {
				t.Fatal(e)
			}
			if e = g.Export(ctx, repo, sha, tree); e != nil {
				t.Fatal(e)
			}
			if g.Calls["resolve_ref"] != 1 || g.Calls["export_attr_inspection"] != 1 || g.Calls["export_tree"] != 1 {
				t.Fatalf("history-dependent Git calls %+v", g.Calls)
			}
			b, e := os.ReadFile(filepath.Join(tree, "README.md"))
			if e != nil || string(b) != "base\n" {
				t.Fatal("exact export")
			}
			if e = os.WriteFile(filepath.Join(tree, "added.txt"), []byte("candidate\n"), 0644); e != nil {
				t.Fatal(e)
			}
			source, e := os.CreateTemp(t.TempDir(), "candidate-*.tar")
			if e != nil {
				t.Fatal(e)
			}
			if e = artifact.Pack(tree, source, g.Config.Limits); e != nil {
				t.Fatal(e)
			}
			source.Close()
			newSHA, e := g.Construct(ctx, model.Task{RepoPath: repo, RepoRef: "refs/heads/main"}, model.Artifact{ID: model.ID(), BaseSHA: sha, BlobPath: source.Name()}, tree)
			if e != nil {
				t.Fatal(e)
			}
			if e = g.CAS(ctx, repo, "refs/heads/main", sha, newSHA); e != nil {
				t.Fatal(e)
			}
			if g.Calls["promotion"] > 8 {
				t.Fatal("promotion call cap")
			}
			current, e := g.Resolve(ctx, repo, "refs/heads/main")
			if e != nil || current != newSHA {
				t.Fatal("CAS did not update")
			}
			g.Calls["promotion"] = 0
			if _, e = g.Construct(ctx, model.Task{RepoPath: repo, RepoRef: "refs/heads/main"}, model.Artifact{BaseSHA: sha}, tree); model.Code(e) != "PRECONDITION_CHANGED" {
				t.Fatalf("drift misclassified: %v", e)
			}
			if g.Calls["promotion"] != 1 {
				t.Fatal("drift did extra Git work")
			}
			if strings.Contains(string(b), "candidate") {
				t.Fatal("unrelated content")
			}
		})
	}
}
