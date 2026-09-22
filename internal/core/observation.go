package core

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"scp-harness/internal/artifact"
	"scp-harness/internal/config"
	"scp-harness/internal/model"
	"scp-harness/internal/store"
)

func observedAttempt(db *store.Store, id string) (*model.Attempt, *model.State, error) {
	s, e := db.Read()
	if e != nil {
		return nil, nil, e
	}
	a := s.Attempts[id]
	if a == nil {
		return nil, nil, model.Err("NOT_FOUND", "Attempt %s", id)
	}
	return a, s, nil
}

// WatchAttempt has its own read-only connection and cancellation lifetime.
// It never shares the scheduler's context, slot, or worker pipes.
func (c *Core) WatchAttempt(ctx context.Context, id string, out io.Writer) error {
	db, e := store.OpenReadOnly(c.Config.Database)
	if e != nil {
		return e
	}
	defer db.Close()
	streams := []logTail{{label: "stdout", cap: c.Config.Limits.Stdout, lineStart: true}, {label: "stderr", cap: c.Config.Limits.Stderr, lineStart: true}}
	status := ""
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		a, _, e := observedAttempt(db, id)
		if e != nil {
			return e
		}
		// Announce entry, but drain final bytes before announcing termination.
		if a.Active() && status != a.Status {
			if _, e = fmt.Fprintf(out, "Attempt %s %s\n", id, a.Status); e != nil {
				return e
			}
			status = a.Status
		}
		for i, path := range []string{a.Stdout, a.Stderr} {
			if e = streams[i].drain(path, out); e != nil {
				return e
			}
		}
		if !a.Active() {
			if status != a.Status {
				_, e = fmt.Fprintf(out, "Attempt %s %s\n", id, a.Status)
			}
			return e
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

type logTail struct {
	label       string
	offset, cap int64
	lineStart   bool
}

func (l *logTail) drain(path string, out io.Writer) error {
	if path == "" {
		return nil
	}
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return nil // PREPARING or a launch that failed before log creation.
	}
	if e != nil {
		return model.Err("BLOCKED", "read live log: %v", e)
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > l.cap || info.Size() < l.offset {
		return model.Err("BLOCKED", "live log unavailable or outside output bound")
	}
	// Drain only the size observed at entry; a producer cannot monopolize watch.
	remaining := info.Size() - l.offset
	buf := make([]byte, 32*1024)
	for remaining > 0 {
		n, e := f.ReadAt(buf[:min(int64(len(buf)), remaining)], l.offset)
		if e != nil && e != io.EOF {
			return model.Err("BLOCKED", "read live log: %v", e)
		}
		if n == 0 {
			return model.Err("BLOCKED", "live log changed during observation; retry")
		}
		data := buf[:n]
		for len(data) > 0 {
			if l.lineStart {
				if _, e = fmt.Fprintf(out, "[%s] ", l.label); e != nil {
					return e
				}
			}
			end := bytes.IndexByte(data, '\n') + 1
			l.lineStart = end > 0
			if end == 0 {
				end = len(data)
			}
			if _, e = out.Write(data[:end]); e != nil {
				return e
			}
			data = data[end:]
		}
		l.offset += int64(n)
		remaining -= int64(n)
	}
	return nil
}

type AttemptDiff struct {
	AttemptID   string `json:"attempt_id"`
	Observation string `json:"observation"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

// DiffAttempt compares immutable input bytes with a bounded root-owned capture.
// Nothing is published as an Artifact and no workspace copy is materialized.
func (c *Core) DiffAttempt(ctx context.Context, id string) (*AttemptDiff, error) {
	db, e := store.OpenReadOnly(c.Config.Database)
	if e != nil {
		return nil, e
	}
	defer db.Close()
	a, s, e := observedAttempt(db, id)
	if e != nil {
		return nil, e
	}
	if c.Config.Profile(a.Operation).Workspace != "writable" {
		return nil, model.Err("INVALID_STATE", "Attempt has no writable workspace")
	}
	if !a.Active() {
		return nil, model.Err("INVALID_STATE", "Attempt is terminal; inspect its Artifact")
	}
	base := filepath.Join(filepath.Dir(c.Config.Database), "runtime", id) + ".snapshot.tar"
	if a.TargetType == "ARTIFACT" {
		base = s.Artifacts[a.TargetID].BlobPath
	}
	f, e := os.Open(base)
	if e != nil {
		return nil, model.Err("BLOCKED", "immutable input unavailable: %v", e)
	}
	before, e := diffFiles(f, c.Config.Limits)
	f.Close()
	if e != nil {
		return nil, e
	}
	var capture bytes.Buffer
	limits, _ := json.Marshal(c.Config.Limits)
	if e = c.Runner.Files(ctx, "observe", []string{string(limits), id}, nil, &capture, artifact.MaxTar(c.Config.Limits)); e != nil {
		return nil, model.Err("BLOCKED", "live workspace observation failed; retry: %v", e)
	}
	after, e := diffFiles(bytes.NewReader(capture.Bytes()), c.Config.Limits)
	if e != nil {
		return nil, model.Err("BLOCKED", "live workspace changed during observation; retry: %v", e)
	}
	current, _, e := observedAttempt(db, id)
	if e != nil {
		return nil, e
	}
	if !current.Active() {
		return nil, model.Err("BLOCKED", "Attempt ended during observation; inspect its Artifact")
	}
	// Include the human heading and possible truncation notice in the same cap.
	overhead := int64(len(fmt.Sprintf("Attempt %s — BEST-EFFORT OBSERVATION\n", id)) + len("[textual diff truncated at output bound]\n"))
	return formatDiff(id, before, after, c.Config.Limits.Stdout-overhead)
}

type diffFile struct {
	mode int64
	data []byte
}

func diffFiles(r io.ReadSeeker, limits config.Limits) (map[string]diffFile, error) {
	if e := artifact.ValidateArchive(io.LimitReader(r, artifact.MaxTar(limits)), limits); e != nil {
		return nil, e
	}
	if _, e := r.Seek(0, io.SeekStart); e != nil {
		return nil, e
	}
	files := map[string]diffFile{}
	tr := tar.NewReader(r)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return files, nil
		}
		if e != nil {
			return nil, e
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		data, e := io.ReadAll(io.LimitReader(tr, limits.Single+1))
		if e != nil || int64(len(data)) != h.Size || h.Size > limits.Single {
			return nil, model.Err("BLOCKED", "snapshot changed during observation; retry")
		}
		files[h.Name] = diffFile{mode: h.Mode & 0111, data: data}
	}
}

func formatDiff(id string, before, after map[string]diffFile, limit int64) (*AttemptDiff, error) {
	if limit <= 0 {
		return nil, model.Err("LIMIT_EXCEEDED", "live diff exceeds output bound")
	}
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	changed := []string{}
	var text strings.Builder
	for name := range names {
		b, bok := before[name]
		a, aok := after[name]
		if bok != aok || b.mode != a.mode || !bytes.Equal(b.data, a.data) {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	for _, name := range changed {
		kind := "M"
		if _, ok := before[name]; !ok {
			kind = "A"
		} else if _, ok := after[name]; !ok {
			kind = "D"
		}
		line := fmt.Sprintf("%s %s\n", kind, name)
		if int64(text.Len()+len(line)) > limit {
			return nil, model.Err("LIMIT_EXCEEDED", "live diff inventory exceeds output bound")
		}
		text.WriteString(line)
	}
	result := &AttemptDiff{AttemptID: id, Observation: "BEST-EFFORT OBSERVATION"}
	appendText := func(s string) {
		left := limit - int64(text.Len())
		if int64(len(s)) > left {
			result.Truncated = true
			s = s[:left]
			for !utf8.ValidString(s) && len(s) > 0 {
				s = s[:len(s)-1]
			}
		}
		text.WriteString(s)
	}
	for _, name := range changed {
		b, bok := before[name]
		a, aok := after[name]
		old, next := "a/"+name, "b/"+name
		if !bok {
			old = "/dev/null"
		}
		if !aok {
			next = "/dev/null"
		}
		appendText(fmt.Sprintf("\n--- %s\n+++ %s\n", old, next))
		if b.mode != a.mode {
			appendText(fmt.Sprintf("executable bits: %03o -> %03o\n", b.mode, a.mode))
		}
		if bytes.IndexByte(b.data, 0) >= 0 || bytes.IndexByte(a.data, 0) >= 0 || !utf8.Valid(b.data) || !utf8.Valid(a.data) {
			appendText("Binary files differ\n")
			continue
		}
		lines := func(data []byte) int {
			n := bytes.Count(data, []byte{'\n'})
			if len(data) > 0 && data[len(data)-1] != '\n' {
				n++
			}
			return n
		}
		start := func(n int) int {
			if n == 0 {
				return 0
			}
			return 1
		}
		bn, an := lines(b.data), lines(a.data)
		appendText(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", start(bn), bn, start(an), an))
		for i, data := range [][]byte{b.data, a.data} {
			prefix := "-"
			if i == 1 {
				prefix = "+"
			}
			for len(data) > 0 && !result.Truncated {
				end := bytes.IndexByte(data, '\n') + 1
				if end == 0 {
					appendText(prefix + string(data) + "\n\\ No newline at end of file\n")
					break
				}
				appendText(prefix + string(data[:end]))
				data = data[end:]
			}
		}
		if result.Truncated {
			break
		}
	}
	result.Text = text.String()
	return result, nil
}
