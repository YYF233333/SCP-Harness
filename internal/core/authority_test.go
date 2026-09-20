package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scp-harness/internal/config"
	"scp-harness/internal/model"
	"scp-harness/internal/worker"
)

func TestInfluenceCannotChangeSchedulingAuthorizationOrLease(t *testing.T) {
	c, taskID, id := claimFixture(t, "option.propose", "option.allocate", "sandbox.write", "process.execute", "repository.read")
	owner := "test-owner"
	p, e := c.Next(owner)
	if e != nil {
		t.Fatal(e)
	}
	a, e := c.PrepareAttempt(*p, owner)
	if e != nil {
		t.Fatal(e)
	}
	lease := a.Lease
	if e = c.Complete(Completion{AttemptID: a.ID, Step: *p, Valid: true, Status: "RETURNED", Elapsed: 0, Result: worker.Result{Disposition: "DROP_FINAL"}}); e != nil {
		t.Fatal(e)
	}
	card := c.Operator()
	card.Influence = map[string]json.Number{"priority": "1e1000", "authority": "9999999"}
	c.Config.Cards[c.Config.Operator] = card
	p, e = c.Next(owner)
	if e != nil || p == nil || p.TargetID != id {
		t.Fatalf("influence changed scheduling: %v", e)
	}
	b, e := c.PrepareAttempt(*p, owner)
	if e != nil {
		t.Fatal(e)
	}
	if b.Lease != lease || b.AnchorID != id || b.TaskID != taskID {
		t.Fatal("influence changed lease/anchor")
	}
	if e = c.Complete(Completion{AttemptID: b.ID, Step: *p, Valid: true, Status: "RETURNED", Elapsed: 0, Result: worker.Result{Disposition: "DROP_FINAL"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Extend(taskID, 1); model.Code(e) != "CAPABILITY_DENIED" {
		t.Fatal("influence granted mint authority")
	}
}
func TestFrozenSchemaCapabilityAndContextRegistries(t *testing.T) {
	data, e := os.ReadFile(filepath.Join("..", "..", "docs", "spec", "scp_harness_role_card_v0.schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	var schema map[string]any
	if e = json.Unmarshal(data, &schema); e != nil {
		t.Fatal(e)
	}
	properties := schema["properties"].(map[string]any)
	channels := properties["context"].(map[string]any)["items"].(map[string]any)["enum"].([]any)
	if len(channels) != len(config.Channels) {
		t.Fatal("context registry diverged")
	}
	for _, v := range channels {
		found := false
		for _, s := range config.Channels {
			found = found || s == v.(string)
		}
		if !found {
			t.Fatal("missing schema context")
		}
	}
	caps := map[string]bool{}
	variants := properties["capabilities"].(map[string]any)["items"].(map[string]any)["oneOf"].([]any)
	for _, v := range variants {
		name := v.(map[string]any)["properties"].(map[string]any)["name"].(map[string]any)
		if constant, ok := name["const"]; ok {
			caps[constant.(string)] = true
		} else {
			for _, s := range name["enum"].([]any) {
				caps[s.(string)] = true
			}
		}
	}
	if len(caps) != len(config.Capabilities) {
		t.Fatal("capability registry diverged")
	}
	for _, s := range config.Capabilities {
		if !caps[s] {
			t.Fatal("invented capability")
		}
	}
}
