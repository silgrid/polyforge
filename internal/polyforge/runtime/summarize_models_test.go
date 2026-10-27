package runtime

// summarize_models_test.go: the summarize lane's configuration is read
// LIVE from the workspace yaml (polyforge#33 supersedes the claim-time
// snapshot of polyforge#224/#243/#250): the workspace-wide root
// summarize.models pick (#242), the narration language and the per-WI
// call spacing — read at every lane request through Runtime.SummarizeLane,
// never carried by the job record.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// An edit to the yaml after the claim governs the very next lane request;
// the keys default (lane off / 中文 / 60) when absent. The yaml names no
// project at all: the root list governs a claim of ANY project.
func TestSummarizeLaneReadsLive(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")

	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w224/a, claude:w224/b] }\n")
	if err := r.Start(context.Background(), "wi_w224sum", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	models, lang, interval := r.SummarizeLane("proj")
	if want := []string{"pi:w224/a", "claude:w224/b"}; !reflect.DeepEqual(models, want) {
		t.Fatalf("SummarizeLane models = %v, want %v", models, want)
	}
	if lang != "中文" || interval != 60 { // polyforge#243/#250 defaults
		t.Fatalf("defaults: language %q interval %d, want 中文 / 60", lang, interval)
	}

	// the mid-flight edit reaches the running job's lane at once
	writeWorkspaceYaml(t, ws, "summarize: { models: [pi:w224/edited], language: English, interval_sec: 120 }\n")
	models, lang, interval = r.SummarizeLane("proj")
	if want := []string{"pi:w224/edited"}; !reflect.DeepEqual(models, want) || lang != "English" || interval != 120 {
		t.Fatalf("after the edit: %v %q %d, want [pi:w224/edited] English 120", models, lang, interval)
	}

	// a keyless yaml: the lane is silently off
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    default: { models: [pi:w79/code-a] }\n")
	if models, _, _ := r.SummarizeLane("proj"); models != nil {
		t.Fatalf("a keyless yaml yields a summarize list: %v", models)
	}

	// an unreadable yaml turns the lane off for the request (the dispatch is blocked on it anyway)
	writeWorkspaceYaml(t, ws, "summarize: [\n")
	if models, _, _ := r.SummarizeLane("proj"); models != nil {
		t.Fatalf("an invalid yaml yields a summarize list: %v", models)
	}
}

// The job record carries no summarize or candidate snapshot at all.
func TestRecordCarriesNoCompositionSnapshot(t *testing.T) {
	j := &job{wi: "wi_w33rec", project: "proj", sessionID: "s", state: StateRunning}
	blob, _ := json.Marshal(j.record())
	for _, key := range []string{"summarize_", "step_candidates"} {
		if strings.Contains(string(blob), key) {
			t.Fatalf("the record carries %q: %s", key, blob)
		}
	}
}
