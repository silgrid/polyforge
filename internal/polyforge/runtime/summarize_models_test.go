package runtime

// summarize_models_test.go: the summarize lane's configuration is the
// daemon's live cli.toml [summarize] snapshot (polyforge#33
// already superseded the claim-time snapshot of polyforge#224/#243/#250):
// the machine-level models list, the narration language and the per-WI
// call spacing — read at every lane request through Runtime.SummarizeLane,
// never carried by the job record.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/liveconfig"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// The lane answers from the daemon's live cli.toml snapshot: a swap of
// the [summarize] table (a `pf config set` / `pf config reload`) governs
// the very next lane request of a running job, the optional keys default
// to 中文 / 60, an absent table turns the lane off, and no workspace yaml
// is read at all (the root list governs a claim of ANY project).
func TestSummarizeLaneReadsLive(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	r.cfg.Live = liveconfig.New(r.cfg.Mapping, "", 0, 0)
	r.cfg.Live.SetSummarize(&workspace.SummarizeSettings{Models: []string{"pi:w224/a", "claude:w224/b"}})

	if err := r.Start(context.Background(), "wi_w224sum", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	models, lang, interval := r.SummarizeLane()
	if want := []string{"pi:w224/a", "claude:w224/b"}; !reflect.DeepEqual(models, want) {
		t.Fatalf("SummarizeLane models = %v, want %v", models, want)
	}
	if lang != "中文" || interval != 60 { // polyforge#243/#250 defaults
		t.Fatalf("defaults: language %q interval %d, want 中文 / 60", lang, interval)
	}

	// a mid-flight swap reaches the running job's lane at once
	r.cfg.Live.SetSummarize(&workspace.SummarizeSettings{Models: []string{"pi:w224/edited"}, Language: "English", IntervalSec: 120})
	models, lang, interval = r.SummarizeLane()
	if want := []string{"pi:w224/edited"}; !reflect.DeepEqual(models, want) || lang != "English" || interval != 120 {
		t.Fatalf("after the swap: %v %q %d, want [pi:w224/edited] English 120", models, lang, interval)
	}

	// a yaml on disk is no source (no per-request yaml read)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    default: { models: [pi:w79/code-a] }\n")
	if models, _, _ := r.SummarizeLane(); !reflect.DeepEqual(models, []string{"pi:w224/edited"}) {
		t.Fatalf("the lane must not read the workspace yaml: %v", models)
	}

	// an absent table: the lane is silently off
	r.cfg.Live.SetSummarize(nil)
	if models, _, _ := r.SummarizeLane(); models != nil {
		t.Fatalf("an absent [summarize] table yields a summarize list: %v", models)
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
