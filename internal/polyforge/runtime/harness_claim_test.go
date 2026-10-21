package runtime

// harness_claim_test.go: polyforge#175 on the claim path — every model
// candidate is `<harness>:<model>`: a mixed-harness chain rides the job
// and its daemon-private state record verbatim, an unprefixed candidate
// or a retired `harness` key in the workspace yaml refuses BEFORE the
// claim (DAEMON-414), a bundle with an unprefixed candidate ends the
// claim blocked naming the file and step.

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestClaimMixedHarnessChainRidesTheJob(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	r.cfg.WorkflowsDir = frontmatterBundle(t, w079Frontmatter)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [claude:claude-opus-5-5, pi:prov/flash] }\n")
	if err := r.Start(context.Background(), "wi_h175comp", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_h175comp")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	j.mu.Lock()
	got := j.stepCandidates
	j.mu.Unlock()
	if !reflect.DeepEqual(got["fix"], []string{"claude:claude-opus-5-5", "pi:prov/flash"}) || !reflect.DeepEqual(got["grill"], []string{"pi:prov/weak-a", "pi:prov/weak-b"}) {
		t.Fatalf("step candidates %v", got)
	}
	// the snapshot (each candidate with its harness) is persisted
	// daemon-side and rehydrates verbatim; no step_harness is written
	blob, _ := json.Marshal(j.record())
	if strings.Contains(string(blob), "step_harness") {
		t.Fatalf("a current record wrote the retired step_harness: %s", blob)
	}
	var back jobRecord
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jobFromRecord(&back).stepCandidates["fix"], got["fix"]) {
		t.Fatalf("state record lost the candidate snapshot: %s", blob)
	}
	if err := r.Stop(context.Background(), "wi_h175comp", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestCandidateFormatRefusesBeforeClaim(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"unprefixed candidate": {"steps:\n  proj:\n    fix: { models: [pi:prov/flash, sub2api-glm/glm-5.3] }\n",
			`steps.proj.fix.models[1]: "sub2api-glm/glm-5.3" needs a harness prefix (pi:… or claude:…)`},
		"unknown prefix": {"steps:\n  proj:\n    default: { models: [codex:gpt-6] }\n", `unknown harness prefix "codex"`},
		"empty model":    {"steps:\n  proj:\n    default: { models: [\"pi:\"] }\n", "names no model"},
		"retired harness key": {"steps:\n  proj:\n    fix: { models: [claude:claude-opus-5-5], harness: claude }\n",
			"steps.proj.fix.harness: the `harness` key was removed (polyforge#175)"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			hub := newFakeHub()
			r, ws, sp := newWorkspaceTestRuntime(t, hub, "proj")
			r.cfg.WorkflowsDir = frontmatterBundle(t, w079Frontmatter)
			writeWorkspaceYaml(t, ws, c.yaml)
			err := r.Start(context.Background(), "wi_h175bad", StartOpts{})
			ae, ok := err.(*api.Error)
			if !ok || ae.Code != api.ErrDaemonWorkspaceYaml || !strings.Contains(ae.Detail, c.want) {
				t.Fatalf("expected DAEMON-414 naming %q, got %v", c.want, err)
			}
			if n := hub.claimCount(); n != 0 {
				t.Fatalf("refused AFTER a claim (%d claim calls)", n)
			}
			if n := sp.callCount(); n != 0 {
				t.Fatalf("the refusal derived %d agents", n)
			}
		})
	}
}

// A bundle whose candidate carries no harness prefix is a load error
// naming the file and the step. With the bundle known from the probe it
// refuses BEFORE the claim (the work item stays queued) ...
func TestBundleUnprefixedCandidateRefusesBeforeClaim(t *testing.T) {
	hub := newFakeHub()
	r, _, sp := newWorkspaceTestRuntime(t, hub, "proj")
	r.cfg.WorkflowsDir = frontmatterBundle(t, strings.Replace(w079Frontmatter, "[pi:prov/flash, pi:prov/glm]", "[pi:prov/flash, prov/glm]", 1))
	bundle := "demo-bundle"
	hub.items = map[string]*api.WorkItem{"wi_h175pre": {ID: "wi_h175pre", ProjectName: "proj", BundleName: &bundle}}
	err := r.Start(context.Background(), "wi_h175pre", StartOpts{})
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonWorkspaceYaml || !strings.Contains(ae.Detail, `step fix: model_candidates[1]: "prov/glm" needs a harness prefix`) || !strings.Contains(ae.Detail, "stays queued") {
		t.Fatalf("expected a pre-claim refusal naming the bundle step, got %v", err)
	}
	if n := hub.claimCount(); n != 0 || sp.callCount() != 0 {
		t.Fatalf("refused AFTER a claim (%d claim calls, %d agents)", n, sp.callCount())
	}
}

// ... and when the probe names no bundle, the claim-time read is the
// backstop: the claim ends blocked with the error and no engine starts.
func TestBundleUnprefixedCandidateBlocksClaim(t *testing.T) {
	hub := newFakeHub()
	r, _, sp := newWorkspaceTestRuntime(t, hub, "proj")
	r.cfg.WorkflowsDir = frontmatterBundle(t, strings.Replace(w079Frontmatter, "[pi:prov/flash, pi:prov/glm]", "[pi:prov/flash, prov/glm]", 1))
	if err := r.Start(context.Background(), "wi_h175bundle", StartOpts{}); err == nil {
		t.Fatal("a bundle with an unprefixed candidate was claimed and started")
	}
	note := hub.lastEnd().Note
	for _, want := range []string{"demo-bundle/workflow.md", `step fix: model_candidates[1]: "prov/glm" needs a harness prefix (pi:… or claude:…)`} {
		if !strings.Contains(note, want) {
			t.Fatalf("blocked note %q does not name %q", note, want)
		}
	}
	if hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("session end %+v, want blocked", hub.lastEnd())
	}
	if n := sp.callCount(); n != 0 {
		t.Fatalf("the refusal derived %d agents", n)
	}
}

// polyforge#176: `<harness>:<model>[:<effort>]` on the claim path — an
// effort chain rides the job snapshot verbatim (canonical form), and an
// effort a harness does not accept refuses before the claim from the
// yaml (DAEMON-414 naming the path) and from the bundle.
func TestClaimEffortChainRidesTheJob(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	r.cfg.WorkflowsDir = frontmatterBundle(t, w079Frontmatter)
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [\"claude:claude-opus-5-5 : high\", pi:ollama/llama3:8b, pi:ollama/llama3:8b:low] }\n")
	if err := r.Start(context.Background(), "wi_h176comp", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_h176comp")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	j.mu.Lock()
	got := j.stepCandidates["fix"]
	j.mu.Unlock()
	if want := []string{"claude:claude-opus-5-5:high", "pi:ollama/llama3:8b", "pi:ollama/llama3:8b:low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fix candidates %v, want %v", got, want)
	}
	if err := r.Stop(context.Background(), "wi_h176comp", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestEffortRefusesBeforeClaim(t *testing.T) {
	cases := map[string]struct{ yaml, bundle, want string }{
		"yaml claude off": {yaml: "steps:\n  proj:\n    fix: { models: [pi:prov/flash, claude:claude-opus-5-5:off] }\n",
			want: `steps.proj.fix.models[1]: "claude:claude-opus-5-5:off": effort "off" is not accepted by claude (it accepts low|medium|high|xhigh|max)`},
		"yaml two efforts": {yaml: "steps:\n  proj:\n    default: { models: [pi:prov/flash:high:low] }\n", want: "names two efforts"},
		"bundle claude minimal": {bundle: strings.Replace(w079Frontmatter, "[pi:prov/flash, pi:prov/glm]", "[pi:prov/flash, claude:claude-opus-5-5:minimal]", 1),
			want: `step fix: model_candidates[1]: "claude:claude-opus-5-5:minimal": effort "minimal" is not accepted by claude`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			hub := newFakeHub()
			r, ws, sp := newWorkspaceTestRuntime(t, hub, "proj")
			fm := w079Frontmatter
			if c.bundle != "" {
				fm = c.bundle
				bundle := "demo-bundle"
				hub.items = map[string]*api.WorkItem{"wi_h176bad": {ID: "wi_h176bad", ProjectName: "proj", BundleName: &bundle}}
			}
			r.cfg.WorkflowsDir = frontmatterBundle(t, fm)
			if c.yaml != "" {
				writeWorkspaceYaml(t, ws, c.yaml)
			}
			err := r.Start(context.Background(), "wi_h176bad", StartOpts{})
			ae, ok := err.(*api.Error)
			if !ok || ae.Code != api.ErrDaemonWorkspaceYaml || !strings.Contains(ae.Detail, c.want) {
				t.Fatalf("expected DAEMON-414 naming %q, got %v", c.want, err)
			}
			if n := hub.claimCount(); n != 0 || sp.callCount() != 0 {
				t.Fatalf("refused AFTER a claim (%d claim calls, %d agents)", n, sp.callCount())
			}
		})
	}
}

// Pre-#175 shapes are no longer converted (decision 2026-10-16 G5): a
// state record's bare candidate ids — with or without the #159
// step_harness — and a manual --models list of bare ids reach the job
// VERBATIM, so the driver's start check refuses each by name ("<id>"
// needs a harness prefix, naming the claim-time list; pinned by
// driver.TestNewRefusesUnprefixedCandidates) instead of the daemon
// re-prefixing it into a different candidate.
func TestPre175ShapesReachTheDriverVerbatim(t *testing.T) {
	var rec jobRecord
	if err := json.Unmarshal([]byte(`{"work_item_id":"wi_old","state":"running",
		"step_candidates":{"fix":["claude-opus-5-5","sub2api-glm/glm-5.3"]},
		"step_harness":{"fix":"claude"}}`), &rec); err != nil {
		t.Fatal(err)
	}
	if got := jobFromRecord(&rec).stepCandidates["fix"]; !reflect.DeepEqual(got, []string{"claude-opus-5-5", "sub2api-glm/glm-5.3"}) {
		t.Fatalf("a pre-#175 record was converted: %v", got)
	}

	hub := newFakeHub()
	r, ws := newManualRuntime(t, hub)
	writeWorkspaceYaml(t, ws, w079ValidYaml)
	r.saveManualModels("wi_bare", []string{"m/one"})
	if err := r.Start(context.Background(), "wi_bare", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), "wi_bare", protocol.FinishWrapped) })
	j := r.jobOf("wi_bare")
	if j == nil {
		t.Fatal("no job")
	}
	for step, list := range j.stepCandidates {
		if !reflect.DeepEqual(list, []string{"m/one"}) {
			t.Fatalf("step %s: the pre-#175 manual list was not passed verbatim: %v", step, list)
		}
	}
	if got := r.loadManualModels("wi_bare"); !reflect.DeepEqual(got, []string{"m/one"}) {
		t.Fatalf("the recorded manual list was dropped: %v", got)
	}
}
