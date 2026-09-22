package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"

	"scp-harness/internal/artifact"
	"scp-harness/internal/config"
	"scp-harness/internal/core"
	"scp-harness/internal/model"
	"scp-harness/internal/scheduler"
	"scp-harness/internal/store"
	"scp-harness/internal/wsl"
)

//go:embed VERSION
var version string

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errout io.Writer) (exit int) {
	command := ""
	asJSON := false
	emit := func(data any, e error) int {
		if e != nil {
			code := model.Code(e)
			if asJSON {
				_ = json.NewEncoder(out).Encode(map[string]any{"ok": false, "command": command, "error": map[string]string{"code": code, "message": e.Error()}})
			} else {
				fmt.Fprintln(errout, e)
			}
			return model.Exit(code)
		}
		if asJSON {
			if e := json.NewEncoder(out).Encode(map[string]any{"ok": true, "command": command, "data": data}); e != nil {
				fmt.Fprintln(errout, "INTERNAL_ERROR: encode command output:", e)
				return 1
			}
		} else if thread, ok := data.(*core.OptionThread); ok {
			for _, claim := range thread.Messages {
				var payload struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(claim.Payload, &payload)
				fmt.Fprintf(out, "[%s] %s:\n%s\n", claim.Created, claim.Issuer, payload.Text)
			}
		} else {
			b, _ := json.MarshalIndent(data, "", "  ")
			fmt.Fprintln(out, string(b))
		}
		return 0
	}
	defer func() {
		if v := recover(); v != nil {
			exit = emit(nil, model.Err("INTERNAL_ERROR", "panic boundary: %v", v))
		}
	}()
	configPath := os.Getenv("SCP_CONFIG")
	if configPath == "" {
		configPath = "scp.json"
	}
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--help":
			help(out, "")
			return 0
		case "--version":
			if len(args) != 1 {
				return emit(nil, model.Err("USAGE_ERROR", "--version takes no arguments"))
			}
			command = "version"
			if asJSON {
				return emit(map[string]string{"version": strings.TrimSpace(version)}, nil)
			}
			fmt.Fprintf(out, "SCP Harness v%s\n", strings.TrimSpace(version))
			return 0
		case "--json":
			asJSON = true
			args = args[1:]
		case "--config":
			if len(args) < 2 {
				return emit(nil, model.Err("USAGE_ERROR", "--config requires path"))
			}
			configPath = args[1]
			args = args[2:]
		default:
			return emit(nil, model.Err("USAGE_ERROR", "unknown global flag"))
		}
	}
	if len(args) == 0 {
		return emit(nil, model.Err("USAGE_ERROR", "command required"))
	}
	command = args[0]
	args = args[1:]
	if command == "help" {
		group := ""
		if len(args) > 0 {
			group = args[0]
		}
		help(out, group)
		return 0
	}
	if len(args) > 0 && (args[0] == "--help" || args[0] == "help") {
		help(out, command)
		return 0
	}
	switch command {
	case "task", "option", "attempt", "artifact", "claim", "blocker", "change", "ci", "config":
		if len(args) == 0 {
			return emit(nil, model.Err("USAGE_ERROR", "subcommand required"))
		}
		command += "." + args[0]
		args = args[1:]
	}
	if len(args) == 1 && args[0] == "--help" {
		help(out, command)
		return 0
	}
	if strings.HasSuffix(command, ".watch") && asJSON {
		return emit(nil, model.Err("USAGE_ERROR", "attempt watch does not support --json"))
	}
	if e := validateCommand(command, args); e != nil {
		return emit(nil, e)
	}
	cfg, e := config.Load(configPath)
	if e != nil {
		return emit(nil, e)
	}
	if command != "init" {
		db, re := store.OpenReadOnly(cfg.Database)
		if re != nil {
			return emit(nil, re)
		}
		st, re := db.Read()
		db.Close()
		if re != nil {
			return emit(nil, re)
		}
		args, re = resolveArguments(st, command, args)
		if re != nil {
			return emit(nil, re)
		}
	}
	// Observations never open the normal controller or its error/blocker path.
	if command == "attempt.watch" || command == "attempt.diff" {
		f, id, rest, e := flags(command, args, true)
		if e != nil {
			return emit(nil, e)
		}
		if e = parse(f, rest); e != nil {
			return emit(nil, e)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		c := &core.Core{Config: cfg, Runner: wsl.Runner{Config: cfg}}
		if command == "attempt.watch" {
			if e = c.WatchAttempt(ctx, id, out); e != nil {
				return emit(nil, e)
			}
			return 0
		}
		diff, e := c.DiffAttempt(ctx, id)
		if e == nil && asJSON {
			encoded, _ := json.Marshal(map[string]any{"ok": true, "command": command, "data": diff})
			if int64(len(encoded)+1) > cfg.Limits.Stdout {
				e = model.Err("LIMIT_EXCEEDED", "encoded live diff exceeds output bound")
			}
		}
		if e != nil || asJSON {
			return emit(diff, e)
		}
		_, e = fmt.Fprintf(out, "Attempt %s — %s\n%s", id, diff.Observation, diff.Text)
		if e == nil && diff.Truncated {
			_, e = fmt.Fprintln(out, "[textual diff truncated at output bound]")
		}
		if e != nil {
			return emit(nil, e)
		}
		return 0
	}
	var c *core.Core
	readonly := command == "status" || command == "config.show" || strings.HasSuffix(command, ".show") || strings.HasSuffix(command, ".list") || strings.HasSuffix(command, ".watch") || command == "option.thread"
	if readonly {
		var db *store.Store
		db, e = store.OpenReadOnly(cfg.Database)
		if e == nil {
			c = &core.Core{Store: db, Config: cfg}
		}
	} else {
		c, e = core.Open(cfg, command == "init")
	}
	if e != nil {
		return emit(nil, e)
	}
	defer c.Store.Close()
	slog.SetDefault(slog.New(slog.NewTextHandler(errout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if command == "change.watch" || command == "ci.watch" {
		if command == "change.watch" {
			e = c.WatchChange(ctx, args[0], out)
		} else {
			e = c.WatchCI(ctx, args[0], out)
		}
		if e != nil {
			return emit(nil, e)
		}
		return 0
	}
	data, e := execute(ctx, c, command, args)
	if model.Exit(model.Code(e)) == 5 {
		_ = c.Block(model.Code(e), "", "", "", e.Error())
	}
	return emit(data, e)
}
func flags(command string, args []string, position bool) (*flag.FlagSet, string, []string, error) {
	id := ""
	if position {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			return nil, "", nil, model.Err("USAGE_ERROR", "%s requires ID", command)
		}
		id = args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f, id, args, nil
}
func parse(f *flag.FlagSet, args []string) error {
	if e := f.Parse(args); e != nil {
		return model.Err("USAGE_ERROR", "%v", e)
	}
	if f.NArg() != 0 {
		return model.Err("USAGE_ERROR", "unexpected positional arguments")
	}
	required := map[string][]string{
		"task.create": {"objective", "repo", "ref", "responsible-actor", "wall-ms"}, "task.revise": {"objective"}, "task.extend": {"wall-ms"}, "option.list": {"task"}, "option.propose": {"task", "text"}, "option.refine": {"text"}, "option.comment": {"text"}, "option.discuss": {"text"}, "option.split": {"spec"}, "option.merge": {"task", "spec"}, "option.allocate": {"wall-ms"}, "artifact.export": {"out"}, "claim.create": {"task", "subject-type", "subject-id", "type"},
	}
	provided := map[string]bool{}
	f.Visit(func(v *flag.Flag) { provided[v.Name] = true })
	for _, name := range required[f.Name()] {
		if !provided[name] {
			return model.Err("USAGE_ERROR", "--%s is required", name)
		}
	}
	return nil
}
func readSpec(path string, v any) error {
	if path == "" {
		return model.Err("USAGE_ERROR", "--spec required")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return model.Err("INVALID_JSON", "spec read: %v", e)
	}
	if e = config.Strict(b, v); e != nil {
		return model.Err("SCHEMA_INVALID", "%v", e)
	}
	return nil
}
func execute(ctx context.Context, c *core.Core, command string, args []string) (any, error) {
	if strings.HasPrefix(command, "change.") || strings.HasPrefix(command, "ci.") {
		return executeChange(ctx, c, command, args)
	}
	if command == "config.show" {
		s, e := c.Read()
		if e != nil {
			return nil, e
		}
		return map[string]any{"config_path": c.Config.Path, "disk_config_hash": c.Config.DiskHash, "scheduler_effective_config_hash": s.SchedulerConfigHash, "scheduler_started_at": s.SchedulerStarted, "restart_required": s.SchedulerConfigHash != "" && (s.SchedulerConfigHash != c.Config.Hash() || s.SchedulerDiskHash != c.Config.DiskHash)}, nil
	}
	positional := map[string]bool{"task.revise": true, "task.extend": true, "task.suspend": true, "task.resume": true, "task.close": true, "task.show": true, "option.show": true, "option.release": true, "option.comment": true, "option.discuss": true, "option.thread": true, "option.refine": true, "option.split": true, "option.allocate": true, "option.close": true, "attempt.show": true, "attempt.interrupt": true, "artifact.show": true, "artifact.export": true, "blocker.resolve": true}
	f, id, args, e := flags(command, args, positional[command])
	if e != nil {
		return nil, e
	}
	switch command {
	case "init":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return map[string]any{"schema_version": 2, "database": c.Config.Database, "artifact_store": c.Config.Artifacts}, nil
	case "run":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		reason, e := scheduler.New(c).Run(ctx)
		if e != nil {
			return nil, e
		}
		return map[string]string{"stop_reason": reason}, nil
	case "recover":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return scheduler.New(c).Recover(ctx)
	case "status":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		s, e := c.Read()
		if e != nil {
			return nil, e
		}
		tasks := []*model.Task{}
		for _, t := range core.SortedTasks(s) {
			if t.Status != "CLOSED" {
				tasks = append(tasks, t)
			}
		}
		var attempt *model.Attempt
		for _, a := range s.Attempts {
			if a.Status == "RUNNING" {
				attempt = a
			}
		}
		blockers := []*model.Blocker{}
		for _, b := range s.Blockers {
			if b.Resolved == nil {
				blockers = append(blockers, b)
			}
		}
		sort.Slice(blockers, func(i, j int) bool {
			if blockers[i].Created == blockers[j].Created {
				return blockers[i].ID < blockers[j].ID
			}
			return blockers[i].Created < blockers[j].Created
		})
		changes := []*model.Change{}
		attention := []*model.Change{}
		queued := 0
		var activeChange *model.Change
		for _, ch := range s.Changes {
			changes = append(changes, ch)
			if ch.State == "QUEUED" {
				queued++
			}
			if ch.State == "PAUSED" || ch.State == "BLOCKED" || ch.State == "STALE" {
				attention = append(attention, ch)
			}
			if ch.State == "RUNNING" {
				activeChange = ch
			}
		}
		var activity any
		if s.Slot.Owner != nil {
			if a := s.Attempts[*s.Slot.Owner]; a != nil {
				activity = a
			} else {
				activity = s.CIRuns[*s.Slot.Owner]
			}
		} else if s.Slot.State == "BUSY" {
			activity = map[string]string{"kind": s.Slot.Kind}
		}
		return map[string]any{"executing_activity": activity, "current_change": activeChange, "changes": changes, "queued_change_count": queued, "attention_changes": attention, "fail_stop": s.FailStop(), "execution_slot": s.Slot, "tasks": tasks, "running_attempt": attempt, "unresolved_blockers": blockers}, nil
	case "task.create":
		objective := f.String("objective", "", "")
		repo := f.String("repo", "", "")
		ref := f.String("ref", "", "")
		actor := f.String("responsible-actor", "", "")
		wall := f.Int64("wall-ms", 0, "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.CreateTask(ctx, *objective, *repo, *ref, *actor, *wall)
	case "task.revise":
		objective := f.String("objective", "", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.ReviseTask(id, *objective)
	case "task.extend":
		wall := f.Int64("wall-ms", 0, "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.Extend(id, *wall)
	case "task.suspend", "task.resume", "task.close":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.Lifecycle(ctx, id, strings.TrimPrefix(command, "task."))
	case "option.propose":
		task := f.String("task", "", "")
		text := f.String("text", "", "")
		source := f.String("source", "", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.Propose(*task, *text, *source)
	case "option.release", "option.thread":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		if command == "option.release" {
			return c.ReleaseOption(id)
		}
		return c.OptionThread(id)
	case "option.comment", "option.discuss":
		text := f.String("text", "", "human discussion text")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		if command == "option.comment" {
			return c.CommentOption(id, *text)
		}
		return c.DiscussOption(id, *text)
	case "option.refine":
		text := f.String("text", "", "")
		wall := f.Int64("transfer-wall-ms", 0, "optional resource transfer (default 0)")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		v, e := c.Split(id, []core.Child{{Text: *text, Wall: *wall}}, true)
		if e != nil {
			return nil, e
		}
		return v.Children[0], nil
	case "option.split":
		path := f.String("spec", "", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		var spec core.SplitSpec
		if e = readSpec(*path, &spec); e != nil {
			return nil, e
		}
		return c.Split(id, spec.Children, false)
	case "option.merge":
		task := f.String("task", "", "")
		path := f.String("spec", "", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		var spec core.MergeSpec
		if e = readSpec(*path, &spec); e != nil {
			return nil, e
		}
		st, re := c.Read()
		if re != nil {
			return nil, re
		}
		for i := range spec.Participants {
			resolved, re := core.ResolveID(st, "option", spec.Participants[i].ID)
			if re != nil {
				return nil, re
			}
			spec.Participants[i].ID = resolved
		}
		return c.Merge(*task, spec)
	case "option.allocate":
		wall := f.Int64("wall-ms", 0, "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.Allocate(id, *wall)
	case "option.close":
		reason := f.String("reason", "ABANDONED", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.CloseOptionReason(ctx, id, *reason)
	case "attempt.interrupt":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.Interrupt(ctx, id)
	case "blocker.resolve":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		return c.ResolveBlocker(id)
	case "artifact.export":
		out := f.String("out", "", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		if *out == "" {
			return nil, model.Err("USAGE_ERROR", "--out required")
		}
		if e = c.Operator().Require("artifact.export"); e != nil {
			return nil, e
		}
		s, e := c.Read()
		if e != nil {
			return nil, e
		}
		a := s.Artifacts[id]
		if a == nil {
			return nil, model.Err("NOT_FOUND", "Artifact")
		}
		if e = artifact.Export(*a, *out); e != nil {
			return nil, e
		}
		return map[string]any{"artifact_id": id, "out": *out, "sha256": a.SHA256, "size_bytes": a.Size}, nil
	case "claim.create":
		task := f.String("task", "", "")
		kind := f.String("subject-type", "", "")
		subject := f.String("subject-id", "", "")
		typ := f.String("type", "", "")
		path := f.String("payload", "", "")
		if e = parse(f, args); e != nil {
			return nil, e
		}
		if *kind != "TASK" && *kind != "OPTION" && *kind != "ARTIFACT" {
			return nil, model.Err("USAGE_ERROR", "invalid subject type")
		}
		payload := []byte(`{}`)
		if *path != "" {
			payload, e = os.ReadFile(*path)
			if e != nil {
				return nil, model.Err("INVALID_JSON", "payload: %v", e)
			}
		}
		return c.CreateClaim(ctx, *task, *kind, *subject, *typ, payload)
	case "task.show", "option.show", "attempt.show", "artifact.show":
		if e = parse(f, args); e != nil {
			return nil, e
		}
		s, e := c.Read()
		if e != nil {
			return nil, e
		}
		switch command {
		case "task.show":
			if v := s.Tasks[id]; v != nil {
				revisions := []model.ObjectiveRevision{}
				for _, revision := range s.ObjectiveRevisions {
					if revision.TaskID == id {
						revisions = append(revisions, revision)
					}
				}
				return map[string]any{"task": v, "objective_revisions": revisions}, nil
			}
		case "option.show":
			if v := s.Options[id]; v != nil {
				return v, nil
			}
		case "attempt.show":
			if v := s.Attempts[id]; v != nil {
				return v, nil
			}
		case "artifact.show":
			if v := s.Artifacts[id]; v != nil {
				return v, nil
			}
		}
		return nil, model.Err("NOT_FOUND", "%s", id)
	case "option.list", "attempt.list", "artifact.list", "claim.list", "blocker.list":
		taskID := ""
		status := ""
		kind := ""
		subject := ""
		unresolved := false
		if command != "blocker.list" {
			f.StringVar(&taskID, "task", "", "")
		}
		if command == "option.list" {
			f.StringVar(&status, "status", "", "")
		}
		if command == "claim.list" {
			f.StringVar(&kind, "subject-type", "", "")
			f.StringVar(&subject, "subject-id", "", "")
		}
		if command == "blocker.list" {
			f.BoolVar(&unresolved, "unresolved", false, "")
		}
		if e = parse(f, args); e != nil {
			return nil, e
		}
		if command == "option.list" && taskID == "" {
			return nil, model.Err("USAGE_ERROR", "option list requires --task")
		}
		if status != "" && status != "OPEN" && status != "CLOSED" {
			return nil, model.Err("USAGE_ERROR", "invalid status")
		}
		if kind != "" && kind != "TASK" && kind != "OPTION" && kind != "ARTIFACT" {
			return nil, model.Err("USAGE_ERROR", "invalid subject type")
		}
		s, e := c.Read()
		if e != nil {
			return nil, e
		}
		type item struct {
			created, id string
			data        any
		}
		items := []item{}
		switch command {
		case "option.list":
			for _, v := range s.Options {
				if v.TaskID == taskID && (status == "" || v.Status == status) {
					items = append(items, item{v.Created, v.ID, v})
				}
			}
		case "attempt.list":
			for _, v := range s.Attempts {
				if taskID == "" || v.TaskID == taskID {
					items = append(items, item{v.Started, v.ID, v})
				}
			}
		case "artifact.list":
			for _, v := range s.Artifacts {
				if taskID == "" || v.TaskID == taskID {
					items = append(items, item{v.Created, v.ID, v})
				}
			}
		case "claim.list":
			for _, v := range s.Claims {
				if (taskID == "" || v.TaskID == taskID) && (kind == "" || v.SubjectType == kind) && (subject == "" || v.SubjectID == subject) {
					items = append(items, item{v.Created, v.ID, v})
				}
			}
		case "blocker.list":
			for _, v := range s.Blockers {
				if !unresolved || v.Resolved == nil {
					items = append(items, item{v.Created, v.ID, v})
				}
			}
		}
		sort.Slice(items, func(i, j int) bool {
			if command == "option.list" {
				a, b := items[i].data.(*model.Option), items[j].data.(*model.Option)
				if a.Status != b.Status {
					return a.Status == "OPEN"
				}
			}
			if items[i].created == items[j].created {
				return items[i].id < items[j].id
			}
			return items[i].created < items[j].created
		})
		data := []any{}
		for _, v := range items {
			data = append(data, v.data)
		}
		return map[string]any{"items": data}, nil
	}
	return nil, model.Err("USAGE_ERROR", "unknown command %s", command)
}
