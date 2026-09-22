package core

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
	"testing"
	"time"

	"scp-harness/internal/worker"
)

type watchOutput struct {
	sync.Mutex
	bytes.Buffer
}

func (b *watchOutput) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *watchOutput) contains(text string) bool {
	b.Lock()
	defer b.Unlock()
	return strings.Contains(b.Buffer.String(), text)
}

func TestWatchPreparingHasNoAuthority(t *testing.T) {
	c, _, option := claimFixture(t, "option.release", "sandbox.write", "process.execute", "repository.read")
	if _, e := c.ReleaseOption(option); e != nil {
		t.Fatal(e)
	}
	p, e := c.Next("owner")
	if e != nil {
		t.Fatal(e)
	}
	a, e := c.PrepareAttempt(*p, "owner")
	if e != nil {
		t.Fatal(e)
	}
	if a.Stdout == "" || a.Stderr == "" {
		t.Fatal("PREPARING paths missing")
	}
	before, e := c.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	old, _ := json.Marshal(before)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output watchOutput
	done := make(chan error, 1)
	go func() { done <- c.WatchAttempt(ctx, a.ID, &output) }()
	deadline := time.Now().Add(5 * time.Second)
	for !output.contains("PREPARING") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !output.contains("PREPARING") {
		t.Fatal("watch did not enter PREPARING")
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	after, e := c.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	next, _ := json.Marshal(after)
	if !bytes.Equal(old, next) {
		t.Fatal("PREPARING watch cancellation changed authority")
	}
	stdout, stderr, e := worker.OpenLogs(a.Stdout, a.Stderr)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = stdout.WriteString("durable\n"); e != nil {
		t.Fatal(e)
	}
	if e = worker.CloseLogs(stdout, stderr); e != nil {
		t.Fatal(e)
	}
	if e = c.MarkRunning(a.ID); e != nil {
		t.Fatal(e)
	}
	if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Status: "RETURNED", Stdout: a.Stdout, Stderr: a.Stderr}); e != nil {
		t.Fatal(e)
	}
	var replay bytes.Buffer
	if e = c.WatchAttempt(context.Background(), a.ID, &replay); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(replay.String(), "[stdout] durable\n") {
		t.Fatal(replay.String())
	}
}

func TestObservationArchitecture(t *testing.T) {
	f, e := parser.ParseFile(token.NewFileSet(), "observation.go", nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	forbidden := map[string]bool{"Update": true, "EmergencyBlock": true, "Block": true, "ReleaseOption": true, "Reserve": true, "Settle": true, "Move": true, "Terminate": true, "Run": true, "Control": true, "Interrupt": true}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Pending" {
			t.Error("Pending access in observation")
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if forbidden[sel.Sel.Name] {
					t.Errorf("authority/execution call in observation: %s", sel.Sel.Name)
				}
				if sel.Sel.Name == "Files" {
					if len(call.Args) < 2 {
						t.Error("missing helper operation")
					} else if op, ok := call.Args[1].(*ast.BasicLit); !ok || op.Value != `"observe"` {
						t.Error("observation invoked mutating helper")
					}
				}
			}
		}
		return true
	})
}

func TestDiffTextBoundsAndBinary(t *testing.T) {
	before := map[string]diffFile{"file": {data: []byte("old\n")}, "binary": {data: []byte{0, 1}}}
	after := map[string]diffFile{"file": {data: []byte("new\n")}, "binary": {data: []byte{0, 2}}}
	r, e := formatDiff("id", before, after, 1024)
	if e != nil || !strings.Contains(r.Text, "M binary\nM file\n") || !strings.Contains(r.Text, "Binary files differ") || !strings.Contains(r.Text, "-old\n+new\n") {
		t.Fatal(r, e)
	}
	r, e = formatDiff("id", before, after, 40)
	if e != nil || !r.Truncated || len(r.Text) > 40 {
		t.Fatal("text cap", r, e)
	}
	if _, e = formatDiff("id", before, after, 1); e == nil {
		t.Fatal("inventory overflow accepted")
	}
}
