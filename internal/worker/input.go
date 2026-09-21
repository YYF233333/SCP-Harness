package worker

import (
	"encoding/json"
	"regexp"
	"strings"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func ValidateInput(v Input, root string) error {
	bad := func() error { return model.Err("CORE_INCONSISTENT", "cannot construct schema-valid worker input") }
	operations := map[string]bool{"mutation": true, "review": true, "option_generation": true, "merge_judge": true, "merge_synth": true}
	if v.Version != 0 || !idPattern.MatchString(v.AttemptID) || !operations[v.Operation] || v.OutputSchema != v.Operation || strings.TrimSpace(v.Objective) == "" || !idPattern.MatchString(v.Target.ID) || !shaPattern.MatchString(v.Origin.SHA) || v.Origin.Revision < 0 || v.Context.Root != root+"/context" || v.Workspace.Root != root+"/workspace" {
		return bad()
	}
	if v.Target.Type != "TASK" && v.Target.Type != "OPTION" && v.Target.Type != "ARTIFACT" {
		return bad()
	}
	if v.Anchor == nil && v.Target.Type != "TASK" || v.Anchor != nil && !idPattern.MatchString(v.Anchor.OptionID) {
		return bad()
	}
	if v.Workspace.Mode != "none" && v.Workspace.Mode != "readonly" && v.Workspace.Mode != "writable" {
		return bad()
	}
	if v.Workspace.Mode != "none" && !shaPattern.MatchString(v.Workspace.BaseSHA) || v.Workspace.Mode == "none" && (v.Workspace.BaseSHA != "" || v.Workspace.Synthetic) {
		return bad()
	}
	if v.Workspace.ArtifactID != "" && !idPattern.MatchString(v.Workspace.ArtifactID) {
		return bad()
	}
	data, e := json.Marshal(v.Card)
	if e != nil {
		return bad()
	}
	if _, e = config.ParseCard(data); e != nil {
		return bad()
	}
	return nil
}
