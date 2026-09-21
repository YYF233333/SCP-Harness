package scheduler

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"scp-harness/internal/model"
)

// Windows refuses this actual CreateProcess call after the executable probe has
// succeeded. No fake runner or production test hook replaces the launch path.
func TestR2LaunchFailureRetainsArtifactStep(t *testing.T) {
	for _, operation := range []string{"mutation", "review"} {
		t.Run(operation, func(t *testing.T) {
			engine, task, option, submitted := candidate(t, "fake-reviewer-approve")
			c := engine.Core
			if operation == "mutation" {
				// Obtain an ordinary continuation using a configured executable.
				// Finish the first candidate's chain, then start a fresh CONTINUE route.
				step(t, engine)
				step(t, engine)
				step(t, engine)
				c.Config.Workers[0].Command = []string{"/opt/scp-workers/fake-worker", "continue-worker"}
				step(t, engine)
				s := state(t, c)
				submitted = s.Artifacts[s.Pending[task.ID].TargetID]
			} else {
				step(t, engine) // protected test -> pending review
			}
			before := state(t, c)
			pending := *before.Pending[task.ID]
			if pending.Operation != operation || pending.TargetType != "ARTIFACT" || pending.TargetID != submitted.ID {
				t.Fatalf("expected Artifact %s step: %+v", operation, pending)
			}
			profileIndex := 0
			if operation == "review" {
				profileIndex = 1
			}
			original := append([]string{}, c.Config.Workers[profileIndex].Command...)
			available, e := c.Runner.Executable(context.Background(), original[0], c.Config.WSL.Root+"/workspace")
			if e != nil || !available {
				t.Fatalf("executable probe must succeed: %v", e)
			}
			c.Config.Workers[profileIndex].Command = append(original, strings.Repeat("x", 40000))
			_, e = engine.Step(context.Background())
			if model.Code(e) != "RUNNER_UNAVAILABLE" {
				t.Fatalf("expected actual launch error: %v", e)
			}
			after := state(t, c)
			var failed *model.Attempt
			for id, a := range after.Attempts {
				if before.Attempts[id] == nil {
					failed = a
				}
			}
			if failed == nil || failed.Status != "TERMINATED" || failed.Reason == nil || *failed.Reason != "RUNNER_UNAVAILABLE" || failed.ExitCode != nil {
				t.Fatalf("launch failure misclassified: %+v", failed)
			}
			if !reflect.DeepEqual(after.Pending[task.ID], &pending) || !reflect.DeepEqual(before.Reviews, after.Reviews) || len(after.Claims) != len(before.Claims) {
				t.Fatal("launch failure lost target or created semantic/review effects")
			}
			if after.Slot.State != "IDLE" || after.Tasks[task.ID].Resources.Outstanding != 0 || after.Options[option.ID].Status != "OPEN" {
				t.Fatal("launch failure did not release/settle without closing work")
			}
			if ran, e := engine.Step(context.Background()); e != nil || ran {
				t.Fatalf("blocked launch automatically retried: %v", e)
			}
			c.Config.Workers[profileIndex].Command = original
			resolved := false
			for _, b := range after.Blockers {
				if b.Kind == "RUNNER_UNAVAILABLE" && b.Resolved == nil {
					if _, e = c.ResolveBlocker(b.ID); e != nil {
						t.Fatal(e)
					}
					resolved = true
				}
			}
			if !resolved {
				t.Fatal("runner blocker missing")
			}
			step(t, engine)
			resumed := state(t, c)
			var retried *model.Attempt
			for id, a := range resumed.Attempts {
				if after.Attempts[id] == nil {
					retried = a
				}
			}
			if retried == nil || retried.Operation != pending.Operation || retried.TargetType != pending.TargetType || retried.TargetID != pending.TargetID || retried.Status != "RETURNED" {
				t.Fatalf("did not resume exact pending step: %+v", retried)
			}
			if operation == "mutation" {
				if retried.ArtifactID == nil || artifactText(t, resumed.Artifacts[*retried.ArtifactID], "stage.txt") != "two\n" {
					t.Fatal("continuation restarted from authoritative state")
				}
			} else if resumed.Reviews[submitted.ID] == nil || resumed.Reviews[submitted.ID].Verdict != "APPROVE" {
				t.Fatal("review failed to resume")
			}
		})
	}
}
