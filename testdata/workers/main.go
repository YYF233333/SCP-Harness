// Configured opaque fixture executable. This program is never linked into Core.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func write(name, text string) {
	must(os.WriteFile(filepath.Join(os.Getenv("SCP_WORKSPACE"), name), []byte(text), 0644))
}
func read(name string) string {
	b, _ := os.ReadFile(filepath.Join(os.Getenv("SCP_WORKSPACE"), name))
	return string(b)
}
func result(v any) {
	b, e := json.Marshal(v)
	must(e)
	must(os.WriteFile(os.Getenv("SCP_RESULT"), b, 0600))
}
func mutation(disposition string) {
	result(map[string]any{"schema_version": 0, "operation": "mutation", "disposition": disposition, "claims": []any{}, "new_options": []any{}})
}
func review(verdict string, findings []string) {
	result(map[string]any{"schema_version": 0, "operation": "review", "verdict": verdict, "findings": findings, "claims": []any{}})
}

type input struct {
	Operation, Objective string
	AttemptID            string `json:"attempt_id"`
}
type option struct{ ID, Text string }

func main() {
	mode := "success-worker"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "forever" {
		if len(os.Args) > 2 && os.Args[2] == "child" {
			cmd := exec.Command(os.Args[0], "forever", "grandchild", os.Args[3])
			must(cmd.Start())
			f, e := os.OpenFile("/tmp/scp-bad-pids-"+os.Args[3], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			must(e)
			_, e = fmt.Fprintln(f, cmd.Process.Pid)
			must(e)
			must(f.Close())
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "protected-test" {
		if _, e := os.Stat("README.md"); e != nil {
			os.Exit(1)
		}
		if len(os.Args) > 2 {
			switch os.Args[2] {
			case "fail":
				os.Exit(7)
			case "timeout":
				for {
					time.Sleep(time.Second)
				}
			case "write":
				must(os.WriteFile("test-output.txt", []byte("disposable\n"), 0644))
			}
		}
		fmt.Println("protected test PASS")
		return
	}
	b, e := os.ReadFile(os.Getenv("SCP_INPUT"))
	must(e)
	var in input
	must(json.Unmarshal(b, &in))
	switch mode {
	case "crash-worker":
		write("crash.txt", "captured\n")
		os.Exit(12)
	case "exit-127-worker":
		os.Exit(127)
	case "timeout-worker":
		write("timeout.txt", "captured\n")
		for {
			time.Sleep(time.Second)
		}
	case "invalid-json-worker":
		must(os.WriteFile(os.Getenv("SCP_RESULT"), []byte(`{"schema_version":0,"schema_version":0}`), 0600))
		return
	case "missing-result-worker":
		return
	case "huge-output-worker":
		for i := 0; i < 100000; i++ {
			fmt.Fprintln(os.Stdout, strings.Repeat("X", 1024))
			fmt.Fprintln(os.Stderr, strings.Repeat("E", 1024))
		}
		mutation("DROP_FINAL")
		return
	case "workspace-writer":
		write("written.txt", "workspace write\n")
		mutation("PROMOTE_FINAL")
		return
	case "executable-worker":
		write("run.sh", "#!/bin/sh\necho executable\n")
		must(os.Chmod(filepath.Join(os.Getenv("SCP_WORKSPACE"), "run.sh"), 0755))
		mutation("PROMOTE_FINAL")
		return
	case "generate-one-worker":
		if in.Operation == "option_generation" {
			result(map[string]any{"schema_version": 0, "operation": in.Operation, "options": []any{map[string]string{"text": "independent route"}}})
		}
		return
	case "worker-unavailable":
		os.Exit(75)
	case "fake-reviewer-approve":
		review("APPROVE", []string{})
		return
	case "fake-reviewer-reject":
		review("REJECT", []string{"needs-fix"})
		return
	case "readonly-reviewer":
		if os.WriteFile(filepath.Join(os.Getenv("SCP_WORKSPACE"), "README.md"), []byte("changed"), 0644) == nil {
			panic("reviewer could write candidate")
		}
		if os.WriteFile(filepath.Join(os.Getenv("SCP_WORKSPACE"), "new.txt"), []byte("changed"), 0644) == nil {
			panic("reviewer could create file")
		}
		review("APPROVE", []string{})
		return
	case "malicious-git-worker":
		dir := os.Getenv("SCP_WORKSPACE")
		cmd := exec.Command("git", "-C", dir, "rev-list", "--count", "HEAD")
		out, e := cmd.Output()
		must(e)
		if strings.TrimSpace(string(out)) != "1" {
			panic("authoritative history leaked")
		}
		cmd = exec.Command("git", "-C", dir, "remote")
		out, e = cmd.Output()
		must(e)
		if strings.TrimSpace(string(out)) != "" {
			panic("remote leaked")
		}
		write("git-count.txt", "1\n")
		mutation("DROP_FINAL")
		return
	case "special-worker":
		must(os.Symlink("README.md", filepath.Join(os.Getenv("SCP_WORKSPACE"), "unsupported-link")))
		mutation("PROMOTE_FINAL")
		return
	case "continue-worker":
		if read("stage.txt") == "" {
			write("stage.txt", "one\n")
			mutation("CONTINUE_FINAL")
		} else {
			write("stage.txt", "two\n")
			mutation("PROMOTE_FINAL")
		}
		return
	case "vorton":
		vorton(in)
		return
	case "success-worker":
		switch in.Operation {
		case "mutation":
			mutation("DROP_FINAL")
		case "review":
			review("APPROVE", []string{})
		case "option_generation":
			result(map[string]any{"schema_version": 0, "operation": in.Operation, "options": []any{}})
		}
		return
	default:
		panic("unknown fake mode " + mode)
	}
}
func targets() []option {
	b, e := os.ReadFile(filepath.Join(os.Getenv("SCP_CONTEXT"), "option.target.json"))
	must(e)
	var v []option
	must(json.Unmarshal(b, &v))
	return v
}
func vorton(in input) {
	switch in.Operation {
	case "option_generation":
		texts := []string{"benchmark-first", "prototype-first-a", "prototype-first-b"}
		if strings.Contains(in.Objective, "emergency") {
			texts = []string{"emergency-hotfix"}
		}
		options := []any{}
		for _, text := range texts {
			options = append(options, map[string]string{"text": text})
		}
		result(map[string]any{"schema_version": 0, "operation": in.Operation, "options": options})
	case "merge_judge":
		v := targets()
		if len(v) != 3 {
			panic("frozen generation changed")
		}
		result(map[string]any{"schema_version": 0, "operation": in.Operation, "groups": [][]string{{v[0].ID}, {v[1].ID, v[2].ID}}})
	case "merge_synth":
		result(map[string]any{"schema_version": 0, "operation": in.Operation, "text": "prototype-first"})
	case "review":
		if read("stage.txt") == "two\n" {
			review("REJECT", []string{"missing-final-fix"})
		} else {
			review("APPROVE", []string{})
		}
	case "mutation":
		v := targets()
		if len(v) != 1 {
			panic("target missing")
		}
		switch v[0].Text {
		case "prototype-first":
			switch read("stage.txt") {
			case "":
				write("stage.txt", "one\n")
				mutation("CONTINUE_FINAL")
			case "one\n":
				write("stage.txt", "two\n")
				mutation("PROMOTE_FINAL")
			case "two\n":
				write("stage.txt", "fixed\n")
				mutation("PROMOTE_FINAL")
			default:
				mutation("DROP_FINAL")
			}
		case "emergency-hotfix":
			write("hotfix.txt", "emergency\n")
			mutation("PROMOTE_FINAL")
		case "bad-worker":
			write("bad.txt", "interrupted\n")
			must(os.WriteFile("/tmp/scp-bad-pids-"+in.AttemptID, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600))
			cmd := exec.Command(os.Args[0], "forever", "child", in.AttemptID)
			must(cmd.Start())
			f, e := os.OpenFile("/tmp/scp-bad-pids-"+in.AttemptID, os.O_APPEND|os.O_WRONLY, 0600)
			must(e)
			_, e = fmt.Fprintln(f, cmd.Process.Pid)
			must(e)
			must(f.Close())
			for {
				time.Sleep(time.Second)
			}
		default:
			mutation("DROP_FINAL")
		}
	}
}
