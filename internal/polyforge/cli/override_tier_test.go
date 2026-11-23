package cli

// override_tier_test.go (polyforge#81 u3, AC2): `pf override
// --tier/--clear-tier` over a fake daemon — the view renders the tier
// aspect, its source label and, when the effective chain is empty, the
// real source instead of hiding it (Q13 A); the write's argv and body
// per action; one action per call. The expected strings come from AC2
// and Q13 A, never from this implementation's output.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestOverrideTierCLI(t *testing.T) {
	d := newDaemonFixture(t)
	view := protocol.OverrideView{WorkItem: "wi_tier", Posture: "running", Steps: []protocol.OverrideStepView{
		{ID: "build", Status: "pending", Tier: "main", TierBy: "usr_1",
			Effective: []string{"pi:prov/main", "claude:main"}, Source: "step override tier main"},
		{ID: "review", Status: "pending", Tier: "cheap", TierBy: "usr_1", TierFallback: "missing",
			Effective: []string{"pi:prov/review"}, Source: "steps.proj.review.models"},
		{ID: "merge", Status: "pending", Tier: "default", TierBy: "usr_2"},
		// a set tier the explicit chain masks: both aspects show
		{ID: "sync", Status: "pending", Models: []string{"pi:prov/masked"}, ModelsBy: "usr_3",
			Tier: "main", TierBy: "usr_2", Effective: []string{"pi:prov/masked"}, Source: "step override"},
	}}
	raw, _ := json.Marshal(view)
	d.answer("GET", "/jobs/wi_tier/overrides", string(raw))
	// the answer carries the model chain too: a tier write must render the
	// TIER aspect, never "overlay models" (review r1#2).
	write := protocol.OverrideWriteResult{WorkItem: "wi_tier", Changed: true, Posture: "running", Published: true,
		Step: protocol.OverrideStepView{ID: "build", Models: []string{"pi:ov/m"}, ModelsBy: "usr_9", Tier: "main", TierBy: "usr_1"}}
	wraw, _ := json.Marshal(write)
	d.answer("POST", "/jobs/wi_tier/overrides", string(wraw))

	code, out, _ := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "override", "wi_tier")
	if code != 0 {
		t.Fatalf("view exit = %d, stderr %q", code, out)
	}
	for _, want := range []string{
		"overlay-tier=main (by usr_1)",
		"next dispatch: pi:prov/main,claude:main (step override tier main)",
		"[not in the catalog: falls to the next layer]",
		"next dispatch: pi:prov/review (steps.proj.review.models)",
		"overlay-tier=default (by usr_2)",
		"next dispatch: (none — the harness adapter's default model)",
		"overlay-tier=main (by usr_2) [masked by overlay-models]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the view lacks %q:\n%s", want, out)
		}
	}

	// one action per call, --step required
	for _, bad := range [][]string{
		{"override", "wi_tier", "--tier", "main"},
		{"override", "wi_tier", "--step", "build", "--tier", "main", "--clear-tier"},
		{"override", "wi_tier", "--step", "build", "--models", "pi:x", "--tier", "main"},
	} {
		code, _, errs := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, bad...)
		if code != 2 && code != 1 {
			t.Fatalf("%v: exit = %d, want 1 or 2:\n%s", bad, code, errs)
		}
		if !strings.Contains(errs, "override:") {
			t.Fatalf("%v: the refusal must name the verb: %s", bad, errs)
		}
	}

	cases := []struct {
		args []string
		body string
	}{
		{[]string{"override", "wi_tier", "--step", "build", "--tier", "main", "--by", "usr_1"}, `"step":"build","operator":"usr_1","tier":"main"`},
		{[]string{"override", "wi_tier", "--step", "build", "--clear-tier", "--by", "usr_1"}, `"step":"build","operator":"usr_1","clear_tier":true`},
	}
	for _, tc := range cases {
		code, out, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, tc.args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d:\n%s", tc.args, code, out)
		}
		last := d.rec.last()
		if last.Method != "POST" || last.Path != "/jobs/wi_tier/overrides" {
			t.Fatalf("%v: the last call was %+v, want the override write", tc.args, last)
		}
		if !strings.Contains(string(last.Body), tc.body) {
			t.Fatalf("%v: body %s lacks %s", tc.args, last.Body, tc.body)
		}
	}
	// the write's rendered aspect: a set shows the tier (and its operator),
	// never "overlay models"; a clear says the tier was cleared (r1#2).
	// The answer carries an explicit chain too, so the masked case is the
	// one a wrong branch would print.
	code, out, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "override", "wi_tier", "--step", "build", "--tier", "main", "--by", "usr_1")
	if code != 0 || !strings.Contains(out, "overlay tier=main (posture running, by usr_1)") || strings.Contains(out, "overlay models=") {
		t.Fatalf("a tier set must render the tier aspect, not the masked chain:\n%s", out)
	}
	code, out, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "override", "wi_tier", "--step", "build", "--clear-tier", "--by", "usr_1")
	if code != 0 || !strings.Contains(out, "overlay tier cleared (posture running, by usr_1)") {
		t.Fatalf("a tier clear must render its own aspect and operator:\n%s", out)
	}
}
