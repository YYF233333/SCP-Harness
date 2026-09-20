package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictSchema(t *testing.T) {
	cfg, e := Load(filepath.Join("..", "..", "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Exploration.N != 1 {
		t.Fatal("example not parsed")
	}
	cardBytes, e := os.ReadFile(filepath.Join("..", "..", "testdata", "cards", "O5-1.json"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseCard(cardBytes); e != nil {
		t.Fatal(e)
	}
	cases := map[string]func(map[string]any){"unknown_context": func(m map[string]any) { m["context"] = []any{"secret"} }, "unknown_capability": func(m map[string]any) { m["capabilities"] = []any{map[string]any{"name": "superuser"}} }, "wrong_scope": func(m map[string]any) {
		m["capabilities"] = []any{map[string]any{"name": "repository.read", "scope": "everything"}}
	}, "missing_scope": func(m map[string]any) { m["capabilities"] = []any{map[string]any{"name": "sandbox.write"}} }, "extra_scope": func(m map[string]any) {
		m["capabilities"] = []any{map[string]any{"name": "claim.publish", "scope": "anything"}}
	}, "duplicate_name": func(m map[string]any) {
		m["capabilities"] = []any{map[string]any{"name": "claim.publish"}, map[string]any{"name": "claim.publish"}}
	}, "missing_lease": func(m map[string]any) { m["limits"] = map[string]any{} }, "unknown_limit": func(m map[string]any) { m["limits"] = map[string]any{"lease.wall_ms": 1, "cpu": 1} }, "null": func(m map[string]any) { m["influence"] = nil }, "unknown_field": func(m map[string]any) { m["model"] = "arbitrary" }}
	for name, modify := range cases {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			if e = json.Unmarshal(cardBytes, &m); e != nil {
				t.Fatal(e)
			}
			modify(m)
			b, _ := json.Marshal(m)
			if _, e = ParseCard(b); e == nil {
				t.Fatal("invalid role card accepted")
			}
		})
	}
	duplicate := strings.Replace(string(cardBytes), `"schema_version": 0`, `"schema_version": 0, "schema_version": 0`, 1)
	if _, e = ParseCard([]byte(duplicate)); e == nil {
		t.Fatal("duplicate key accepted")
	}
	for _, data := range []string{`{"name":"one","name":"two"}`, `{"name":null}`, `{}`, `{"name":"ok","other":1}`, `{"name":"ok","Name":"override"}`, `{"name":"ok"} {}`} {
		var dst struct {
			Name string `json:"name"`
		}
		if e = Strict([]byte(data), &dst); e == nil {
			t.Fatalf("strict accepted %s", data)
		}
	}
}
func TestConfigRejectsUnknownAndMissingBounds(t *testing.T) {
	b, e := os.ReadFile(filepath.Join("..", "..", "scp.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	var base map[string]any
	if e = json.Unmarshal(b, &base); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"unknown", "zero_timeout", "missing_limit", "duplicate_operation"} {
		t.Run(kind, func(t *testing.T) {
			var m map[string]any
			json.Unmarshal(b, &m)
			switch kind {
			case "unknown":
				m["extra"] = true
			case "zero_timeout":
				m["limits"].(map[string]any)["external_process_timeout_ms"] = 0
			case "missing_limit":
				delete(m["limits"].(map[string]any), "workspace_max_bytes")
			case "duplicate_operation":
				m["operation_profiles"].(map[string]any)["new_operation"] = "operator"
			}
			path := filepath.Join(t.TempDir(), "config.json")
			data, _ := json.Marshal(m)
			if e = os.WriteFile(path, data, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = Load(path); e == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
func TestRoleCardMathematicalNumbersMatchJSONSchema(t *testing.T) {
	for _, wall := range []string{"1", "1.0", "1e0", "1e1000", "9223372036854775808"} {
		data := `{"schema_version":0.0,"id":"numbers","context":[],"capabilities":[],"limits":{"lease.wall_ms":` + wall + `},"influence":{"opaque":1e10000,"zero":-0.0}}`
		c, e := ParseCard([]byte(data))
		if e != nil || c.LeaseMS() <= 0 {
			t.Fatalf("schema-valid %s: %v", wall, e)
		}
	}
	for _, wall := range []string{"0", "-1", "0.1", "\"1\"", "null"} {
		data := `{"schema_version":0,"id":"numbers","context":[],"capabilities":[],"limits":{"lease.wall_ms":` + wall + `},"influence":{}}`
		if _, e := ParseCard([]byte(data)); e == nil {
			t.Fatalf("invalid limit %s accepted", wall)
		}
	}
}
