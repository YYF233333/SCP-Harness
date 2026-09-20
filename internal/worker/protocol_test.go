package worker

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResultSchemas(t *testing.T) {
	valid := map[string]string{
		"mutation":          `{"schema_version":0,"operation":"mutation","disposition":"DROP_FINAL","claims":[],"new_options":[]}`,
		"review":            `{"schema_version":0,"operation":"review","verdict":"APPROVE","findings":[],"claims":[]}`,
		"option_generation": `{"schema_version":0,"operation":"option_generation","options":[{"text":"route"}]}`,
		"merge_judge":       `{"schema_version":0,"operation":"merge_judge","groups":[["a"]]}`,
		"merge_synth":       `{"schema_version":0,"operation":"merge_synth","text":"route"}`,
	}
	for op, data := range valid {
		t.Run(op, func(t *testing.T) {
			if _, e := Parse([]byte(data), op, 4096); e != nil {
				t.Fatal(e)
			}
			var m map[string]any
			json.Unmarshal([]byte(data), &m)
			for key := range m {
				var v map[string]any
				json.Unmarshal([]byte(data), &v)
				delete(v, key)
				b, _ := json.Marshal(v)
				if _, e := Parse(b, op, 4096); e == nil {
					t.Fatalf("missing %s accepted", key)
				}
				v[key] = nil
				b, _ = json.Marshal(v)
				if _, e := Parse(b, op, 4096); e == nil {
					t.Fatalf("null %s accepted", key)
				}
			}
			for _, bad := range []string{strings.Replace(data, `"schema_version":0`, `"schema_version":0,"schema_version":0`, 1), strings.TrimSuffix(data, "}") + `,"issuer_actor_id":"O5-1"}`} {
				if _, e := Parse([]byte(bad), op, 4096); e == nil {
					t.Fatal("unknown/duplicate field accepted")
				}
			}
			if _, e := Parse([]byte(data), op, 1); e == nil {
				t.Fatal("size limit bypass")
			}
		})
	}
	for _, data := range []string{`{"schema_version":0,"operation":"mutation","disposition":"drop_final","claims":[],"new_options":[]}`, `{"schema_version":0,"operation":"mutation","disposition":"DROP_FINAL","claims":[],"new_options":[{"text":" "}]}`, `{"schema_version":0,"operation":"review","verdict":"APPROVE","findings":[" "],"claims":[]}`} {
		op := "mutation"
		if strings.Contains(data, `"review"`) {
			op = "review"
		}
		if _, e := Parse([]byte(data), op, 4096); e == nil {
			t.Fatal("invalid enum/text accepted")
		}
	}
}
func TestPartitionClosedSet(t *testing.T) {
	for _, groups := range [][][]string{{{"a", "b"}, {"c"}}, {{"c"}, {"a"}, {"b"}}} {
		if e := Partition(groups, []string{"a", "b", "c"}); e != nil {
			t.Fatal(e)
		}
	}
	for _, groups := range [][][]string{{{"a", "b"}}, {{"a", "b", "c", "a"}}, {{"a", "b", "foreign-task-id"}}, {{"a", "b", "c"}, {}}, {{"a", "b", "unknown"}, {"c"}}} {
		if e := Partition(groups, []string{"a", "b", "c"}); e == nil {
			t.Fatal("invalid partition accepted")
		}
	}
}
