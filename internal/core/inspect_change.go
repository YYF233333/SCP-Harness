package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"scp-harness/internal/model"
)

func ResolveID(s *model.State, kind, prefix string) (string, error) {
	ids := []string{}
	switch strings.ToLower(kind) {
	case "task":
		for id := range s.Tasks {
			ids = append(ids, id)
		}
	case "option":
		for id := range s.Options {
			ids = append(ids, id)
		}
	case "change":
		for id := range s.Changes {
			ids = append(ids, id)
		}
	case "ci":
		for id := range s.CIRuns {
			ids = append(ids, id)
		}
	case "attempt":
		for id := range s.Attempts {
			ids = append(ids, id)
		}
	case "artifact":
		for id := range s.Artifacts {
			ids = append(ids, id)
		}
	case "claim":
		for id := range s.Claims {
			ids = append(ids, id)
		}
	case "blocker":
		for id := range s.Blockers {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if id == prefix {
			return id, nil
		}
	}
	match := ""
	for _, id := range ids {
		if id == prefix {
			return id, nil
		}
		if prefix != "" && strings.HasPrefix(id, prefix) {
			if match != "" {
				return "", model.Err("AMBIGUOUS_ID", "%s prefix %s", kind, prefix)
			}
			match = id
		}
	}
	if match == "" {
		return "", model.Err("NOT_FOUND", "%s %s", kind, prefix)
	}
	return match, nil
}

func ChangeView(s *model.State, id string) (map[string]any, error) {
	ch := s.Changes[id]
	if ch == nil {
		return nil, model.Err("NOT_FOUND", "Change")
	}
	type entry struct {
		Time  string `json:"time"`
		Kind  string `json:"kind"`
		Value any    `json:"value"`
	}
	timeline := []entry{{ch.Created, "release", ch.ID}}
	for _, a := range s.Attempts {
		if a.ChangeID == id {
			timeline = append(timeline, entry{a.Started, a.Operation, a})
		}
	}
	for _, a := range s.Artifacts {
		if a.ChangeID == id {
			timeline = append(timeline, entry{a.Created, "artifact", a})
		}
	}
	for _, r := range s.CIRuns {
		if r.ChangeID == id || s.Artifacts[r.ArtifactID].ChangeID == id {
			timeline = append(timeline, entry{r.Created, "ci", r})
		}
	}
	for _, j := range s.Journals {
		if a := s.Artifacts[j.ArtifactID]; a != nil && a.ChangeID == id {
			timeline = append(timeline, entry{j.Created, "promotion", j})
		}
	}
	for _, e := range s.Events {
		if e.Message == id {
			timeline = append(timeline, entry{e.Created, e.Code, e})
		}
	}
	timeline = append(timeline, entry{ch.Updated, ch.Stage + "/" + ch.State, ch.Reason})
	sort.SliceStable(timeline, func(i, j int) bool {
		if timeline[i].Time == timeline[j].Time {
			a, _ := json.Marshal(timeline[i])
			b, _ := json.Marshal(timeline[j])
			return string(a) < string(b)
		}
		return timeline[i].Time < timeline[j].Time
	})
	return map[string]any{"change": ch, "artifact": s.Artifacts[ch.ArtifactID], "ci_run": s.CIRuns[ch.CIRunID], "latest_artifact_ci_run": s.LatestCI(ch.ArtifactID), "review_attempt": s.Attempts[ch.ReviewAttemptID], "timeline": timeline}, nil
}

func (c *Core) WatchChange(ctx context.Context, id string, out io.Writer) error {
	previous := ""
	for {
		s, e := c.Read()
		if e != nil {
			return e
		}
		view, e := ChangeView(s, id)
		if e != nil {
			return e
		}
		data, _ := json.MarshalIndent(view, "", "  ")
		if string(data) != previous {
			if _, e = fmt.Fprintln(out, string(data)); e != nil {
				return e
			}
			previous = string(data)
		}
		ch := s.Changes[id]
		if ch.Terminal() || ch.State == "PAUSED" || ch.State == "BLOCKED" || ch.State == "STALE" || ch.Stage == "AWAIT_PROMOTION" {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *Core) WatchCI(ctx context.Context, id string, out io.Writer) error {
	stdout, stderr := logTail{label: "stdout", cap: c.Config.Test.Output, lineStart: true}, logTail{label: "stderr", cap: c.Config.Test.Output, lineStart: true}
	last := ""
	for {
		s, e := c.Read()
		if e != nil {
			return e
		}
		r := s.CIRuns[id]
		if r == nil {
			return model.Err("NOT_FOUND", "CI Run")
		}
		if r.Status != last {
			if _, e = fmt.Fprintf(out, "CI %s %s\n", id, r.Status); e != nil {
				return e
			}
			last = r.Status
		}
		if r.Stdout != "" {
			if e = stdout.drain(r.Stdout, out); e != nil {
				return e
			}
		}
		if r.Stderr != "" {
			if e = stderr.drain(r.Stderr, out); e != nil {
				return e
			}
		}
		if r.Ended != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}
