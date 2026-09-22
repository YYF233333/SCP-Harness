package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"scp-harness/internal/config"
	"slices"
	"sort"
	"strconv"
	"strings"

	"scp-harness/internal/core"
	"scp-harness/internal/model"
)

var commandFlags = map[string]string{
	"init": "", "run": "", "recover": "", "status": "", "config.show": "",
	"task.create": "objective repo ref responsible-actor wall-ms", "task.extend": "wall-ms", "task.revise": "objective", "task.show": "", "task.suspend": "", "task.resume": "", "task.close": "",
	"option.list": "task status", "option.show": "", "option.propose": "task text source", "option.refine": "text transfer-wall-ms", "option.split": "spec", "option.merge": "task spec", "option.allocate": "wall-ms", "option.close": "reason", "option.release": "", "option.comment": "text", "option.discuss": "text", "option.thread": "",
	"change.list": "task state", "change.show": "", "change.watch": "", "change.promote": "", "change.pause": "", "change.resume": "", "change.abort": "", "change.retry-ci": "", "change.retry-review": "", "change.rework": "",
	"ci.list": "task change", "ci.show": "", "ci.watch": "", "ci.run": "",
	"attempt.list": "task", "attempt.show": "", "attempt.watch": "", "attempt.diff": "", "attempt.interrupt": "",
	"artifact.list": "task", "artifact.show": "", "artifact.export": "out",
	"claim.list": "task subject-type subject-id", "claim.create": "task subject-type subject-id type payload",
	"blocker.list": "unresolved", "blocker.resolve": "",
}

func hasID(command string) bool {
	group, verb, _ := strings.Cut(command, ".")
	return verb != "" && !slices.Contains([]string{"create", "propose", "list", "merge"}, verb) && group != "config"
}

// Validate the entire command before opening configuration, database or creating files.
func validateCommand(command string, args []string) error {
	shape, ok := commandFlags[command]
	if !ok {
		return model.Err("USAGE_ERROR", "unknown command %s", command)
	}
	f, _, rest, e := flags(command, args, hasID(command))
	if e != nil {
		return e
	}
	for _, name := range strings.Fields(shape) {
		if name == "unresolved" {
			f.Bool(name, false, "")
		} else {
			f.String(name, "", "")
		}
	}
	if e = parse(f, rest); e != nil {
		return e
	}
	f.Visit(func(v *flag.Flag) {
		value := v.Value.String()
		if strings.TrimSpace(value) == "" {
			e = model.Err("USAGE_ERROR", "--%s must not be empty", v.Name)
			return
		}
		if strings.HasSuffix(v.Name, "wall-ms") {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || v.Name == "wall-ms" && n == 0 {
				e = model.Err("USAGE_ERROR", "--%s requires a positive bounded integer", v.Name)
			}
		}
		valid := map[string][]string{"status": {"OPEN", "CLOSED"}, "state": {"QUEUED", "RUNNING", "PAUSED", "BLOCKED", "STALE", "DONE", "ABORTED"}, "subject-type": {"TASK", "OPTION", "ARTIFACT"}, "reason": {"FULFILLED", "SUPERSEDED", "ABANDONED"}}
		if allowed := valid[v.Name]; allowed != nil && !slices.Contains(allowed, value) {
			e = model.Err("USAGE_ERROR", "invalid --%s", v.Name)
		}
	})
	if e != nil {
		return e
	}
	if f.Lookup("spec") != nil {
		path := f.Lookup("spec").Value.String()
		if command == "option.split" {
			var spec core.SplitSpec
			if e = readSpec(path, &spec); e != nil {
				return e
			}
			if len(spec.Children) < 2 {
				return model.Err("SCHEMA_INVALID", "split requires two children")
			}
			for _, child := range spec.Children {
				if strings.TrimSpace(child.Text) == "" || child.Wall < 0 {
					return model.Err("SCHEMA_INVALID", "invalid split child")
				}
			}
		}
		if command == "option.merge" {
			var spec core.MergeSpec
			if e = readSpec(path, &spec); e != nil {
				return e
			}
			if strings.TrimSpace(spec.Text) == "" || len(spec.Participants) < 2 {
				return model.Err("SCHEMA_INVALID", "invalid merge")
			}
			for _, participant := range spec.Participants {
				if participant.ID == "" || participant.Wall < 0 {
					return model.Err("SCHEMA_INVALID", "invalid merge participant")
				}
			}
		}
	}
	if f.Lookup("payload") != nil && f.Lookup("payload").Value.String() != "" {
		data, re := os.ReadFile(f.Lookup("payload").Value.String())
		if re != nil {
			return model.Err("INVALID_JSON", "payload: %v", re)
		}
		var obj map[string]json.RawMessage
		if re = config.Strict(data, &obj); re != nil || obj == nil {
			return model.Err("INVALID_JSON", "payload must be an object")
		}
	}
	if f.Lookup("subject-id") != nil && f.Lookup("subject-id").Value.String() != "" && f.Lookup("subject-type").Value.String() == "" {
		return model.Err("USAGE_ERROR", "--subject-id requires --subject-type")
	}

	return e
}

func resolveArguments(s *model.State, command string, args []string) ([]string, error) {
	args = append([]string{}, args...)
	if hasID(command) {
		kind, _, _ := strings.Cut(command, ".")
		if command == "ci.run" {
			kind = "artifact"
		}
		id, e := core.ResolveID(s, kind, args[0])
		if e != nil {
			return nil, e
		}
		args[0] = id
	}
	kind := ""
	for i, arg := range args {
		if arg == "--subject-type" && i+1 < len(args) {
			kind = args[i+1]
		}
		if strings.HasPrefix(arg, "--subject-type=") {
			kind = strings.TrimPrefix(arg, "--subject-type=")
		}
	}
	for i := 0; i < len(args); i++ {
		name, value, equals := strings.Cut(args[i], "=")
		entity := map[string]string{"--task": "task", "--change": "change", "--source": "option", "--subject-id": kind}[name]
		if entity == "" {
			continue
		}
		if !equals {
			i++
			value = args[i]
		}
		id, e := core.ResolveID(s, entity, value)
		if e != nil {
			return nil, e
		}
		if equals {
			args[i] = name + "=" + id
		} else {
			args[i] = id
		}
	}
	return args, nil
}

func help(out io.Writer, group string) {
	fmt.Fprintln(out, "SCP Harness v0.2 — explicit development and promotion authority\nUsage: scph [--config PATH] [--json] COMMAND [arguments]\nIDs accept unique prefixes; ambiguous prefixes fail.")
	commands := []string{}
	for command := range commandFlags {
		if group == "" || command == group || strings.HasPrefix(command, group+".") {
			commands = append(commands, command)
		}
	}
	sort.Strings(commands)
	for _, command := range commands {
		id := ""
		if hasID(command) {
			id = " ID"
		}
		params := []string{}
		for _, name := range strings.Fields(commandFlags[command]) {
			params = append(params, "--"+name+" VALUE")
		}
		verb := command
		if _, v, ok := strings.Cut(command, "."); ok {
			verb = v
		}
		purpose := map[string]string{"init": "initialize independent database", "run": "execute queued activities serially", "recover": "settle stopped execution and reconcile journals", "status": "inspect activity and attention states", "show": "inspect saved record/evidence", "list": "list records with optional filters", "watch": "follow saved activity and logs", "create": "create Task with explicit objective and budget", "extend": "mint additional Task budget", "revise": "append an objective revision", "suspend": "pause Task work and release repository binding after settlement", "resume": "resume existing paused work", "close": "close intent and settle remaining resources", "propose": "create an unfunded Option", "refine": "create a replacement Option", "split": "create child Options with explicit transfers", "merge": "create replacement from participants", "allocate": "transfer wall-time budget", "release": "authorize a new development Change", "comment": "append human discussion", "discuss": "queue readonly agent discussion", "thread": "read ordered discussion", "promote": "authorize repository construction and CAS", "pause": "interrupt activity and preserve development position", "abort": "permanently abandon Change; retain evidence", "retry-ci": "queue new verification of current Artifact", "retry-review": "queue review using latest CI evidence", "rework": "continue mutation from current Artifact", "interrupt": "stop agent and pause its Change", "diff": "inspect bounded live workspace changes", "export": "copy immutable Artifact to --out", "resolve": "explicitly clear a repaired infrastructure blocker"}[verb]
		if command == "ci.run" {
			purpose = "queue independent verification; no agent invocation"
		}
		fmt.Fprintf(out, "  %s%s %s — %s\n", strings.ReplaceAll(command, ".", " "), id, strings.Join(params, " "), purpose)
	}
	fmt.Fprintln(out, "release: creates a queued Change; grants development authority.\npromote: separately authorizes Git construction and CAS through the scheduler.\npause / attempt interrupt: stop activity, retain Artifact and Change position.\nresume: continues that Change and Artifact. abort: permanently abandons Change, retaining evidence.\nretry-ci / ci run: machine verification only; retry-review: review; rework: mutation from current Artifact.\nallocation: transfers budget only; no execution authority.\nOnly an explicitly authorized promotion can update the authoritative repository.\nshow/list/watch/config show are observations. propose/refine/split/merge/comment/revise edit Core metadata only.\noption close --reason FULFILLED|SUPERSEDED|ABANDONED (default ABANDONED).")
}

func executeChange(ctx context.Context, c *core.Core, command string, args []string) (any, error) {
	f, id, rest, e := flags(command, args, hasID(command))
	if e != nil {
		return nil, e
	}
	taskID, changeID, state := "", "", ""
	if command == "change.list" || command == "ci.list" {
		f.StringVar(&taskID, "task", "", "")
		if command == "ci.list" {
			f.StringVar(&changeID, "change", "", "")
		} else {
			f.StringVar(&state, "state", "", "")
		}
	}
	if e = parse(f, rest); e != nil {
		return nil, e
	}
	if command == "ci.run" {
		return c.QueueCI(id)
	}
	if strings.HasPrefix(command, "change.") && command != "change.show" && command != "change.list" {
		return c.ChangeAction(ctx, id, strings.TrimPrefix(command, "change."))
	}
	s, e := c.Read()
	if e != nil {
		return nil, e
	}
	if command == "change.show" {
		return core.ChangeView(s, id)
	}
	if command == "ci.show" {
		return s.CIRuns[id], nil
	}
	items := []any{}
	if command == "change.list" {
		changes := []*model.Change{}
		for _, ch := range s.Changes {
			if (taskID == "" || ch.TaskID == taskID) && (state == "" || ch.State == state) {
				changes = append(changes, ch)
			}
		}
		sort.Slice(changes, func(i, j int) bool {
			if changes[i].Created == changes[j].Created {
				return changes[i].ID < changes[j].ID
			}
			return changes[i].Created < changes[j].Created
		})
		for _, ch := range changes {
			items = append(items, ch)
		}
	} else {
		runs := []*model.CIRun{}
		for _, r := range s.CIRuns {
			if (taskID == "" || s.Artifacts[r.ArtifactID].TaskID == taskID) && (changeID == "" || r.ChangeID == changeID || s.Artifacts[r.ArtifactID].ChangeID == changeID) {
				runs = append(runs, r)
			}
		}
		sort.Slice(runs, func(i, j int) bool {
			if runs[i].Created == runs[j].Created {
				return runs[i].ID < runs[j].ID
			}
			return runs[i].Created < runs[j].Created
		})
		for _, r := range runs {
			items = append(items, r)
		}
	}
	return map[string]any{"items": items}, nil
}
