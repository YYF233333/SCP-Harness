package core

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"

	"scp-harness/internal/artifact"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

func writeJSON(path string, v any) error {
	data, e := json.Marshal(v)
	if e != nil {
		return model.Err("CORE_INCONSISTENT", "input encode: %v", e)
	}
	if e = os.WriteFile(path, data, 0600); e != nil {
		return artifact.StorageError("input write", e)
	}
	return nil
}

// Materialize uses channel names as literal files/directories. It never dumps
// the database. Visible IDs are the actual Claim subject allowlist for this run.
func (c *Core) Materialize(ctx context.Context, a *model.Attempt, p model.Step, host string) (map[string]bool, error) {
	s, e := c.Read()
	if e != nil {
		return nil, e
	}
	t := s.Tasks[a.TaskID]
	profile := c.Config.Profile(a.Operation)
	card := CardFor(c.Config, a)
	contextRoot := filepath.Join(host, "context")
	if e = os.MkdirAll(contextRoot, 0700); e != nil {
		return nil, artifact.StorageError("context directory", e)
	}
	if a.Operation == "discussion" {
		instruction := "Answer the current human question using the Option and ordered discussion. Give a reviewable conclusion, a summary of reasons, risks and recommendations; do not output hidden chain-of-thought. Do not claim to have modified the Option or code. Describe proposed refinements only; O5 decides whether to refine."
		if e = os.WriteFile(filepath.Join(contextRoot, "discussion-instruction.txt"), []byte(instruction), 0600); e != nil {
			return nil, artifact.StorageError("discussion instruction", e)
		}
	}
	base := a.SHA
	source := ""
	var submitted *model.Artifact
	anchor := p.OptionID
	if p.TargetType == "ARTIFACT" {
		submitted = s.Artifacts[p.TargetID]
		if submitted == nil {
			return nil, model.Err("CORE_INCONSISTENT", "Artifact target missing")
		}
		base = submitted.BaseSHA
		source = submitted.ID
		anchor = submitted.Anchor
	} else if p.TargetType == "OPTION" {
		anchor = p.TargetID
	}
	objective := a.Objective
	if objective == "" {
		objective = t.Objective
	}
	if ch := s.Changes[a.ChangeID]; ch != nil {
		objective = ch.Objective
	}
	in := worker.Input{Version: 0, AttemptID: a.ID, Operation: a.Operation, Objective: objective, Target: worker.Target{Type: p.TargetType, ID: p.TargetID}, Card: card, Origin: worker.Origin{SHA: a.SHA, Revision: a.Revision}, Context: worker.Root{Root: c.Runner.Root() + "/context"}, Workspace: worker.Workspace{Mode: profile.Workspace, Root: c.Runner.Root() + "/workspace", BaseSHA: base, ArtifactID: source, Synthetic: profile.Synthetic}, OutputSchema: a.Operation}
	if anchor != "" {
		in.Anchor = &worker.Anchor{OptionID: anchor}
	}
	if profile.Workspace == "none" {
		in.Workspace.BaseSHA = ""
	}
	if e = worker.ValidateInput(in, c.Runner.Root()); e != nil {
		return nil, e
	}
	if e = writeJSON(filepath.Join(host, "input.json"), in); e != nil {
		return nil, e
	}
	if e = os.WriteFile(filepath.Join(host, "result.json"), nil, 0600); e != nil {
		return nil, artifact.StorageError("result placeholder", e)
	}
	workspaceRoot := filepath.Join(host, "workspace")
	if profile.Workspace != "none" {
		if e = os.Mkdir(workspaceRoot, 0700); e != nil {
			return nil, artifact.StorageError("workspace directory", e)
		}
		if submitted != nil {
			e = artifact.Restore(*submitted, workspaceRoot, c.Config.Limits)
		} else {
			e = c.Git.Snapshot(ctx, t.RepoPath, a.SHA, workspaceRoot, host+".snapshot.tar")
		}
		if e != nil {
			return nil, e
		}
	}
	visible := map[string]bool{p.TargetType + ":" + p.TargetID: true}
	if anchor != "" {
		visible["OPTION:"+anchor] = true
	}
	add := func(channel string, v any) error { return writeJSON(filepath.Join(contextRoot, channel+".json"), v) }
	for _, channel := range card.Context {
		switch channel {
		case "task.objective":
			e = add(channel, objective)
		case "task.state":
			e = add(channel, t)
			visible["TASK:"+t.ID] = true
		case "option.target":
			opts := []*model.Option{}
			if len(p.Participants) > 0 {
				for _, id := range p.Participants {
					opts = append(opts, s.Options[id])
					visible["OPTION:"+id] = true
				}
			} else if anchor != "" {
				opts = append(opts, s.Options[anchor])
			}
			e = add(channel, opts)
		case "option.lineage.direct":
			edges := []model.Edge{}
			opts := map[string]*model.Option{}
			for _, edge := range s.Edges {
				if edge.Parent == anchor || edge.Child == anchor {
					edges = append(edges, edge)
					other := edge.Parent
					if other == anchor {
						other = edge.Child
					}
					opts[other] = s.Options[other]
					visible["OPTION:"+other] = true
				}
			}
			e = add(channel, map[string]any{"edges": edges, "options": opts})
		case "claim.related":
			claims := []*model.Claim{}
			for _, claim := range s.Claims {
				if claim.TaskID == t.ID && (claim.SubjectID == p.TargetID || claim.SubjectID == anchor || claim.SubjectID == t.ID) {
					claims = append(claims, claim)
					visible[claim.SubjectType+":"+claim.SubjectID] = true
				}
			}
			sort.Slice(claims, func(i, j int) bool {
				if claims[i].Created == claims[j].Created {
					return claims[i].ID < claims[j].ID
				}
				return claims[i].Created < claims[j].Created
			})
			e = add(channel, claims)
		case "artifact.metadata":
			if submitted != nil {
				e = add(channel, submitted)
				visible["ARTIFACT:"+submitted.ID] = true
			}
		case "artifact.content":
			if submitted != nil {
				dir := filepath.Join(contextRoot, channel)
				if e = os.Mkdir(dir, 0700); e == nil {
					e = artifact.Restore(*submitted, dir, c.Config.Limits)
				}
			}
		case "ci.result":
			if submitted != nil {
				run := s.LatestCI(submitted.ID)
				if ch := s.Changes[a.ChangeID]; ch != nil && ch.CIRunID != "" {
					run = s.CIRuns[ch.CIRunID]
				}
				if run != nil {
					e = materializeCI(contextRoot, run, c.Config.Test.Output)
				}
			}
		case "review.findings":
			if submitted != nil && s.Reviews[submitted.ID] != nil {
				e = add(channel, s.Reviews[submitted.ID])
			}
		case "ledger.resource":
			e = add(channel, map[string]any{"anchor_id": a.AnchorID, "remaining_wall_ms": s.Accounts[a.AnchorID].Remaining, "lease_wall_ms": a.Lease})
		case "repository.snapshot":
			if card.Has("repository.read") && submitted == nil && profile.Workspace != "none" {
				dir := filepath.Join(contextRoot, channel)
				if e = os.Mkdir(dir, 0700); e == nil {
					e = artifact.Copy(workspaceRoot, dir, c.Config.Limits)
				}
			}
		}
		if e != nil {
			return nil, e
		}
	}
	return visible, nil
}

func materializeCI(root string, run *model.CIRun, limit int64) error {
	dir := filepath.Join(root, "ci.result")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return artifact.StorageError("CI context", e)
	}
	projected := *run
	projected.Stdout = "stdout.log"
	projected.Stderr = "stderr.log"
	for _, log := range []struct {
		src, name string
		truncated *bool
	}{{run.Stdout, "stdout.log", &projected.StdoutTruncated}, {run.Stderr, "stderr.log", &projected.StderrTruncated}} {
		f, e := os.Open(log.src)
		if e != nil {
			return artifact.StorageError("CI evidence read", e)
		}
		data, e := io.ReadAll(io.LimitReader(f, limit+1))
		f.Close()
		if e != nil {
			return artifact.StorageError("CI evidence read", e)
		}
		if int64(len(data)) > limit {
			data = data[:limit]
			*log.truncated = true
		}
		if e = os.WriteFile(filepath.Join(dir, log.name), data, 0600); e != nil {
			return artifact.StorageError("CI evidence materialization", e)
		}
	}
	return writeJSON(filepath.Join(dir, "result.json"), projected)
}
