package gitrepo

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestArchitectureConstraints(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := map[string]bool{"config": true, "model": true, "store": true, "core": true, "ledger": true, "scheduler": true, "worker": true, "wsl": true, "artifact": true, "gitrepo": true, "boundedexec": true}
	entries, e := os.ReadDir(filepath.Join(root, "internal"))
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range entries {
		if d.IsDir() && !allowed[d.Name()] {
			t.Fatalf("forbidden package %s", d.Name())
		}
	}
	forbidden := []string{"rev-list", "blame", "bisect", "reflog", "merge-base", "--contains", "fsck", "repack", "fetch", "pull"}
	attributeInvocations := 0
	e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".local" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		file, e := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for _, imp := range file.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == "os/exec" && !strings.HasPrefix(rel, "internal/boundedexec/") {
				t.Errorf("external process outside boundedexec: %s", rel)
			}
			if strings.Contains(strings.Split(path, "/")[0], ".") && path != "modernc.org/sqlite" {
				t.Errorf("forbidden direct third-party import %s", path)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if rel == "internal/gitrepo/attributes.go" {
					if selector, ok := n.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "invoke" {
						attributeInvocations++
						if len(n.Args) < 2 {
							t.Fatal("attribute inspection missing category")
						}
						category, ok := n.Args[1].(*ast.BasicLit)
						if !ok || category.Value != `"export_attr_inspection"` {
							t.Fatal("attribute inspection hidden outside its budget")
						}
					}
				}
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					v, _ := strconv.Unquote(n.Value)
					if (v == "git" || v == "git.exe") && !strings.HasPrefix(rel, "internal/gitrepo/") {
						t.Errorf("Git construction outside gitrepo: %s", rel)
					}
					if strings.HasPrefix(rel, "internal/gitrepo/") {
						if v == "check-attr" || v == "ls-tree" {
							t.Errorf("superseded R4 attribute evaluation command: %s", v)
						}
						for _, bad := range forbidden {
							if v == bad {
								t.Errorf("forbidden Core Git argv %s", bad)
							}
						}
						if v == "log" || v == "gc" {
							t.Errorf("forbidden Git command %s", v)
						}
					}
				}
			case *ast.GoStmt:
				if strings.HasPrefix(rel, "internal/scheduler/") {
					call := n.Call
					_, literal := call.Fun.(*ast.FuncLit)
					if !literal {
						t.Errorf("parallel dispatch outside single-process cancellation monitor: %s", rel)
					}
				}
			}
			return true
		})
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if attributeInvocations != 1 {
		t.Fatalf("R4 must have one bounded attribute inspection invocation, got %d", attributeInvocations)
	}
	mod, e := os.ReadFile(filepath.Join(root, "go.mod"))
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, " v") && !strings.HasPrefix(line, "require modernc.org/sqlite ") && !strings.HasPrefix(line, "modernc.org/sqlite ") && !strings.HasSuffix(line, "// indirect") {
			t.Errorf("additional direct dependency: %s", line)
		}
	}
	manifest, e := os.ReadFile(filepath.Join(root, "docs", "spec", "MANIFEST.sha256"))
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "  ", 2)
		if len(parts) != 2 {
			t.Fatal("invalid authority manifest")
		}
		b, e := os.ReadFile(filepath.Join(root, "docs", "spec", parts[1]))
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != parts[0] {
			t.Errorf("authority digest mismatch: %s", parts[1])
		}
	}
}
