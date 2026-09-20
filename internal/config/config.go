package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"scp-harness/internal/model"
)

type Capability struct {
	Name  string `json:"name"`
	Scope string `json:"scope,omitempty"`
}
type Card struct {
	Version      int                    `json:"schema_version"`
	ID           string                 `json:"id"`
	Context      []string               `json:"context"`
	Capabilities []Capability           `json:"capabilities"`
	Limits       map[string]json.Number `json:"limits"`
	Influence    map[string]json.Number `json:"influence"`
}

func (c Card) LeaseMS() int64 {
	s, _ := integer(string(c.Limits["lease.wall_ms"]))
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return math.MaxInt64
	}
	return n
}
func number(raw json.RawMessage) (json.Number, bool) {
	s := strings.TrimSpace(string(raw))
	return json.Number(s), len(s) > 0 && (s[0] == '-' || s[0] >= '0' && s[0] <= '9')
}
func negative(n json.Number) bool {
	s := string(n)
	if !strings.HasPrefix(s, "-") {
		return false
	}
	mantissa := strings.FieldsFunc(s, func(r rune) bool { return r == 'e' || r == 'E' })[0]
	return strings.ContainsAny(mantissa, "123456789")
}

func (c Card) Has(name string) bool {
	for _, v := range c.Capabilities {
		if v.Name == name {
			return true
		}
	}
	return false
}
func (c Card) Sees(name string) bool {
	for _, v := range c.Context {
		if v == name {
			return true
		}
	}
	return false
}
func (c Card) Require(names ...string) error {
	for _, name := range names {
		if !c.Has(name) {
			return model.Err("CAPABILITY_DENIED", "actor %s requires %s", c.ID, name)
		}
	}
	return nil
}

var Channels = []string{"task.objective", "task.state", "option.target", "option.lineage.direct", "claim.related", "artifact.metadata", "artifact.content", "test.result", "review.findings", "ledger.resource", "repository.snapshot"}
var Capabilities = []string{"task.create", "task.extend", "task.suspend", "task.resume", "task.complete", "option.propose", "option.refine", "option.split", "option.merge", "option.allocate", "option.complete", "claim.publish", "resource.propose", "review.decide", "attempt.interrupt", "artifact.export", "repository.read", "sandbox.write", "process.execute"}

func member(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
func ParseCard(data []byte) (Card, error) {
	// Capability scope is conditionally required; validate each exact shape separately.
	type shape struct {
		Version   int                        `json:"schema_version"`
		ID        string                     `json:"id"`
		Context   []string                   `json:"context"`
		Caps      []json.RawMessage          `json:"capabilities"`
		Limits    map[string]json.RawMessage `json:"limits"`
		Influence map[string]json.RawMessage `json:"influence"`
	}
	var s shape
	var c Card
	if e := Strict(data, &s); e != nil {
		return c, model.Err("SCHEMA_INVALID", "role card: %v", e)
	}
	wall, numeric := number(s.Limits["lease.wall_ms"])
	integerWall, isInteger := "", false
	if numeric {
		integerWall, isInteger = integer(string(wall))
	}
	if s.Version != 0 || s.ID == "" || len(s.Limits) != 1 || !numeric || !isInteger || integerWall == "0" || negative(wall) {
		return c, model.Err("SCHEMA_INVALID", "invalid role card version/id/limits")
	}
	c = Card{s.Version, s.ID, s.Context, []Capability{}, map[string]json.Number{"lease.wall_ms": wall}, map[string]json.Number{}}
	seen := map[string]bool{}
	for _, v := range s.Context {
		if !member(Channels, v) || seen[v] {
			return c, model.Err("SCHEMA_INVALID", "unknown/duplicate context %s", v)
		}
		seen[v] = true
	}
	seen = map[string]bool{}
	for _, raw := range s.Caps {
		var p map[string]json.RawMessage
		if e := Strict(raw, &p); e != nil {
			return c, model.Err("SCHEMA_INVALID", "capability: %v", e)
		}
		var name, scope string
		if e := json.Unmarshal(p["name"], &name); e != nil || !member(Capabilities, name) || seen[name] {
			return c, model.Err("SCHEMA_INVALID", "unknown/duplicate capability %s", name)
		}
		seen[name] = true
		expected := ""
		if name == "repository.read" {
			expected = "task.repository"
		}
		if name == "sandbox.write" || name == "process.execute" {
			expected = "lease.sandbox"
		}
		if expected == "" {
			if len(p) != 1 {
				return c, model.Err("SCHEMA_INVALID", "unexpected capability fields")
			}
		} else {
			if e := json.Unmarshal(p["scope"], &scope); e != nil || scope != expected || len(p) != 2 {
				return c, model.Err("SCHEMA_INVALID", "invalid capability scope")
			}
		}
		c.Capabilities = append(c.Capabilities, Capability{name, scope})
	}
	key := regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	for k, v := range s.Influence {
		n, ok := number(v)
		if !key.MatchString(k) || !ok || negative(n) {
			return c, model.Err("SCHEMA_INVALID", "invalid influence")
		}
		c.Influence[k] = n
	}
	return c, nil
}

type Limits struct {
	Files     int64 `json:"workspace_max_files"`
	Bytes     int64 `json:"workspace_max_bytes"`
	Single    int64 `json:"single_file_max_bytes"`
	Path      int64 `json:"path_max_bytes"`
	Stdout    int64 `json:"stdout_max_bytes"`
	Stderr    int64 `json:"stderr_max_bytes"`
	Result    int64 `json:"result_max_bytes"`
	Export    int64 `json:"git_export_max_bytes"`
	ProcessMS int64 `json:"external_process_timeout_ms"`
}
type Profile struct {
	ID          string   `json:"id"`
	Command     []string `json:"command"`
	Card        string   `json:"actor_card"`
	Timeout     int64    `json:"timeout_ms"`
	Workspace   string   `json:"workspace"`
	Synthetic   bool     `json:"synthetic_git"`
	Unavailable []int    `json:"unavailable_exit_codes"`
}
type Config struct {
	Version   int               `json:"schema_version"`
	Database  string            `json:"database"`
	Artifacts string            `json:"artifact_store"`
	Operator  string            `json:"operator_actor_card"`
	RoleCards map[string]string `json:"role_cards"`
	WSL       struct {
		Distro string `json:"distro"`
		Root   string `json:"attempt_root"`
	} `json:"wsl"`
	Exploration struct {
		N int `json:"initial_option_generation_attempts"`
	} `json:"exploration"`
	Limits Limits `json:"limits"`
	Test   struct {
		Command []string `json:"command"`
		Timeout int64    `json:"timeout_ms"`
		Output  int64    `json:"output_limit_bytes"`
	} `json:"protected_test"`
	Workers    []Profile         `json:"workers"`
	Operations map[string]string `json:"operation_profiles"`
	Cards      map[string]Card   `json:"-"`
}

func Load(path string) (*Config, error) {
	data, e := os.ReadFile(path)
	if e != nil {
		return nil, model.Err("INVALID_CONFIG", "read config: %v", e)
	}
	var c Config
	if e = Strict(data, &c); e != nil {
		return nil, model.Err("INVALID_CONFIG", "%v", e)
	}
	fail := func(msg string) (*Config, error) { return nil, model.Err("INVALID_CONFIG", "%s", msg) }
	if c.Version != 0 || strings.TrimSpace(c.Database) == "" || strings.TrimSpace(c.Artifacts) == "" || c.WSL.Distro != "SCP-Worker" || c.WSL.Root != "/scp/attempt" || c.Exploration.N <= 0 {
		return fail("invalid version/paths/runner/exploration")
	}
	for _, v := range []int64{c.Limits.Files, c.Limits.Bytes, c.Limits.Single, c.Limits.Path, c.Limits.Stdout, c.Limits.Stderr, c.Limits.Result, c.Limits.Export, c.Limits.ProcessMS, c.Test.Timeout, c.Test.Output} {
		if v <= 0 || v > math.MaxInt64/1000000 {
			return fail("all limits must be positive, bounded integers")
		}
	}
	if !command(c.Test.Command) {
		return fail("protected test command is empty")
	}
	dir, e := filepath.Abs(filepath.Dir(path))
	if e != nil {
		return fail(e.Error())
	}
	resolve := func(p string) string {
		if !filepath.IsAbs(p) {
			return filepath.Join(dir, p)
		}
		return filepath.Clean(p)
	}
	c.Database = resolve(c.Database)
	c.Artifacts = resolve(c.Artifacts)
	c.Cards = map[string]Card{}
	for id, p := range c.RoleCards {
		b, e := os.ReadFile(resolve(p))
		if e != nil {
			return fail(fmt.Sprintf("role card %s: %v", id, e))
		}
		card, e := ParseCard(b)
		if e != nil {
			return fail(e.Error())
		}
		c.Cards[id] = card
	}
	if _, ok := c.Cards[c.Operator]; !ok {
		return fail("unknown operator card")
	}
	profiles := map[string]Profile{}
	for _, p := range c.Workers {
		if p.ID == "" || !command(p.Command) || p.Timeout <= 0 || p.Timeout > math.MaxInt64/1000000 || !member([]string{"none", "readonly", "writable"}, p.Workspace) {
			return fail("invalid worker profile")
		}
		if _, ok := profiles[p.ID]; ok {
			return fail("duplicate worker id")
		}
		if _, ok := c.Cards[p.Card]; !ok {
			return fail("unknown worker card")
		}
		codes := map[int]bool{}
		for _, code := range p.Unavailable {
			if code < 1 || code > 255 || codes[code] {
				return fail("invalid unavailable exit code")
			}
			codes[code] = true
		}
		if p.Synthetic && p.Workspace == "none" {
			return fail("synthetic Git requires a workspace")
		}
		profiles[p.ID] = p
	}
	modes := map[string]string{"mutation": "writable", "review": "readonly", "option_generation": "none", "merge_judge": "none", "merge_synth": "none"}
	if len(c.Operations) != len(modes) {
		return fail("exactly five operation profiles required")
	}
	for op, mode := range modes {
		p, ok := profiles[c.Operations[op]]
		if !ok || p.Workspace != mode {
			return fail("invalid operation profile: " + op)
		}
	}
	return &c, nil
}
func command(c []string) bool { return len(c) > 0 && strings.TrimSpace(c[0]) != "" }
func (c *Config) Profile(operation string) Profile {
	for _, p := range c.Workers {
		if p.ID == c.Operations[operation] {
			return p
		}
	}
	panic("validated operation profile missing")
}
