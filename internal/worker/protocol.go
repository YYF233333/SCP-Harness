package worker

import (
	"encoding/json"
	"strings"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

type Target struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
type Anchor struct {
	OptionID string `json:"option_id"`
}
type Origin struct {
	SHA      string `json:"repo_sha"`
	Revision int64  `json:"state_revision"`
}
type Root struct {
	Root string `json:"root"`
}
type Workspace struct {
	Mode       string `json:"mode"`
	Root       string `json:"root"`
	BaseSHA    string `json:"base_repo_sha"`
	ArtifactID string `json:"source_artifact_id"`
	Synthetic  bool   `json:"synthetic_git"`
}
type Input struct {
	Version      int         `json:"schema_version"`
	AttemptID    string      `json:"attempt_id"`
	Operation    string      `json:"operation"`
	Objective    string      `json:"objective"`
	Target       Target      `json:"target"`
	Anchor       *Anchor     `json:"semantic_anchor"`
	Card         config.Card `json:"actor_card"`
	Origin       Origin      `json:"created_against"`
	Context      Root        `json:"context"`
	Workspace    Workspace   `json:"workspace"`
	OutputSchema string      `json:"output_schema"`
}
type ClaimRequest struct {
	SubjectType string          `json:"subject_type"`
	SubjectID   string          `json:"subject_id"`
	Type        string          `json:"claim_type"`
	Payload     json.RawMessage `json:"payload"`
}
type OptionRequest struct {
	Text string `json:"text"`
}
type Mutation struct {
	Version     int             `json:"schema_version"`
	Operation   string          `json:"operation"`
	Disposition string          `json:"disposition"`
	Claims      []ClaimRequest  `json:"claims"`
	Options     []OptionRequest `json:"new_options"`
}
type Review struct {
	Version   int            `json:"schema_version"`
	Operation string         `json:"operation"`
	Verdict   string         `json:"verdict"`
	Findings  []string       `json:"findings"`
	Claims    []ClaimRequest `json:"claims"`
}
type Generation struct {
	Version   int             `json:"schema_version"`
	Operation string          `json:"operation"`
	Options   []OptionRequest `json:"options"`
}
type Judge struct {
	Version   int        `json:"schema_version"`
	Operation string     `json:"operation"`
	Groups    [][]string `json:"groups"`
}
type Synth struct {
	Version   int    `json:"schema_version"`
	Operation string `json:"operation"`
	Text      string `json:"text"`
}
type Discussion struct {
	Version   int    `json:"schema_version"`
	Operation string `json:"operation"`
	Text      string `json:"text"`
}

type Result struct {
	Disposition, Verdict, Text string
	Findings                   []string
	Claims                     []ClaimRequest
	Options                    []OptionRequest
	Groups                     [][]string
}

func Parse(data []byte, operation string, cap int64) (Result, error) {
	r := Result{}
	invalid := func(e any) (Result, error) { return r, model.Err("SCHEMA_INVALID", "worker result: %v", e) }
	if int64(len(data)) > cap {
		return invalid("size cap")
	}
	version := -1
	op := ""
	switch operation {
	case "mutation":
		var v Mutation
		if e := config.Strict(data, &v); e != nil {
			return invalid(e)
		}
		version, op = v.Version, v.Operation
		r.Disposition = v.Disposition
		r.Claims = v.Claims
		r.Options = v.Options
		if v.Disposition != "PROMOTE_FINAL" && v.Disposition != "CONTINUE_FINAL" && v.Disposition != "DROP_FINAL" {
			return invalid("disposition")
		}
	case "review":
		var v Review
		if e := config.Strict(data, &v); e != nil {
			return invalid(e)
		}
		version, op = v.Version, v.Operation
		r.Verdict = v.Verdict
		r.Findings = v.Findings
		r.Claims = v.Claims
		if v.Verdict != "APPROVE" && v.Verdict != "REJECT" {
			return invalid("verdict")
		}
	case "option_generation":
		var v Generation
		if e := config.Strict(data, &v); e != nil {
			return invalid(e)
		}
		version, op = v.Version, v.Operation
		r.Options = v.Options
	case "merge_judge":
		var v Judge
		if e := config.Strict(data, &v); e != nil {
			return invalid(e)
		}
		version, op = v.Version, v.Operation
		r.Groups = v.Groups
	case "discussion":
		var v Discussion
		if e := config.Strict(data, &v); e != nil {
			return invalid(e)
		}
		version, op = v.Version, v.Operation
		r.Text = v.Text
		if strings.TrimSpace(v.Text) == "" {
			return invalid("empty discussion text")
		}
	case "merge_synth":
		var v Synth
		if e := config.Strict(data, &v); e != nil {
			return invalid(e)
		}
		version, op = v.Version, v.Operation
		r.Text = v.Text
		if strings.TrimSpace(v.Text) == "" {
			return invalid("empty synthesized text")
		}
	default:
		return invalid("unknown operation")
	}
	if version != 0 || op != operation {
		return invalid("version/operation")
	}
	for _, o := range r.Options {
		if strings.TrimSpace(o.Text) == "" {
			return invalid("empty Option text")
		}
	}
	for _, f := range r.Findings {
		if strings.TrimSpace(f) == "" {
			return invalid("empty finding")
		}
	}
	for _, c := range r.Claims {
		if c.SubjectType != "TASK" && c.SubjectType != "OPTION" && c.SubjectType != "ARTIFACT" || strings.TrimSpace(c.SubjectID) == "" || strings.TrimSpace(c.Type) == "" {
			return invalid("claim shape")
		}
		var obj map[string]json.RawMessage
		if e := config.Strict(c.Payload, &obj); e != nil {
			return invalid("claim payload must be object")
		}
	}
	return r, nil
}
func Partition(groups [][]string, ids []string) error {
	remaining := map[string]bool{}
	for _, id := range ids {
		if remaining[id] {
			return model.Err("SCHEMA_INVALID", "duplicate partition input")
		}
		remaining[id] = true
	}
	for _, g := range groups {
		if len(g) == 0 {
			return model.Err("SCHEMA_INVALID", "empty group")
		}
		for _, id := range g {
			if !remaining[id] {
				return model.Err("SCHEMA_INVALID", "unknown/duplicate partition ID")
			}
			delete(remaining, id)
		}
	}
	if len(remaining) != 0 {
		return model.Err("SCHEMA_INVALID", "missing partition ID")
	}
	return nil
}
