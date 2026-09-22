package core

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

func TestInfluenceCannotChangeSchedulingAuthorizationOrLease(t *testing.T) {
	c, taskID, id := claimFixture(t, "change.resume", "option.release", "option.propose", "option.allocate", "sandbox.write", "process.execute", "repository.read")
	owner := "test-owner"
	if _, e := c.ReleaseOption(id); e != nil {
		t.Fatal(e)
	}
	p, e := c.Next(owner)
	if e != nil {
		t.Fatal(e)
	}
	a, e := c.PrepareAttempt(*p, owner)
	if e != nil {
		t.Fatal(e)
	}
	lease := a.Lease
	if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Valid: true, Status: "RETURNED", Elapsed: 0, Result: worker.Result{Disposition: "DROP_FINAL"}}); e != nil {
		t.Fatal(e)
	}
	card := c.Operator()
	card.Influence = map[string]json.Number{"priority": "1e1000", "authority": "9999999"}
	c.Config.Cards[c.Config.Operator] = card
	if next, e := c.Next(owner); e != nil || next != nil {
		t.Fatal("influence released a chain", e)
	}
	var changeID string
	for id := range readState(t, c).Changes {
		changeID = id
	}
	if _, e = c.ChangeAction(context.Background(), changeID, "resume"); e != nil {
		t.Fatal(e)
	}
	p, e = c.Next(owner)
	if e != nil || p == nil || p.TargetID != id {
		t.Fatalf("influence changed scheduling: %v", e)
	}
	b, e := c.PrepareAttempt(*p, owner)
	if e != nil {
		t.Fatal(e)
	}
	if b.Lease != lease || b.AnchorID != id || b.TaskID != taskID {
		t.Fatal("influence changed lease/anchor")
	}
	if e = c.Complete(Completion{AttemptID: b.ID, Step: *p, Valid: true, Status: "RETURNED", Elapsed: 0, Result: worker.Result{Disposition: "DROP_FINAL"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Extend(taskID, 1); model.Code(e) != "CAPABILITY_DENIED" {
		t.Fatal("influence granted mint authority")
	}
}
func TestFrozenSchemaCapabilityAndContextRegistries(t *testing.T) {
	data, e := os.ReadFile(filepath.Join("..", "..", "docs", "spec", "scp_harness_role_card_v0.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	var schema map[string]any
	if e = json.Unmarshal(data, &schema); e != nil {
		t.Fatal(e)
	}
	properties := schema["properties"].(map[string]any)
	channels := properties["context"].(map[string]any)["items"].(map[string]any)["enum"].([]any)
	if len(channels) != len(config.Channels) {
		t.Fatal("context registry diverged")
	}
	for _, v := range channels {
		found := false
		for _, s := range config.Channels {
			found = found || s == v.(string)
		}
		if !found {
			t.Fatal("missing schema context")
		}
	}
	caps := map[string]bool{}
	variants := properties["capabilities"].(map[string]any)["items"].(map[string]any)["oneOf"].([]any)
	for _, v := range variants {
		name := v.(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)
		if constant, ok := name["const"]; ok {
			caps[constant.(string)] = true
		} else {
			for _, s := range name["enum"].([]any) {
				caps[s.(string)] = true
			}
		}
	}
	if len(caps) != len(config.Capabilities) {
		t.Fatal("capability registry diverged")
	}
	for _, s := range config.Capabilities {
		if !caps[s] {
			t.Fatal("invented capability")
		}
	}
}

// The behavioral suite exercises every creation path and 100 funded candidates.
// This AST guard also prevents reintroducing a direct fresh-Option seed or a
// scheduler/worker call to the host release entrypoint.
func TestFreshMutationSeedArchitecture(t *testing.T) {
	root := filepath.Join("..", "..")
	seeds, calls := 0, 0
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".local" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReleaseOption" {
						calls++
						if rel != "cmd/scp/main.go" {
							t.Errorf("release called outside host CLI: %s", rel)
						}
					}
				}
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				typ, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || typ.Sel.Name != "Change" {
					return true
				}
				fields := map[string]string{}
				for _, element := range lit.Elts {
					kv, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					value, ok := kv.Value.(*ast.BasicLit)
					if ok && value.Kind == token.STRING {
						fields[key.Name], _ = strconv.Unquote(value.Value)
					}
				}
				if fields["Stage"] == "MUTATION" && fields["State"] == "QUEUED" {
					seeds++
					if rel != "internal/core/discussion.go" || fn.Name.Name != "ReleaseOption" {
						t.Errorf("fresh mutation seed outside ReleaseOption: %s/%s", rel, fn.Name.Name)
					}
				}
				return true
			})
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if seeds != 1 || calls != 1 {
		t.Fatalf("expected one seed and one host caller; got %d/%d", seeds, calls)
	}
}
