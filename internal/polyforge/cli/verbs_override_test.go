package cli

// verbs_override_test.go (u5, polyforge#25): the `pf override` verb over
// a fake daemon — the view's rendering, the write's argv and body per
// action, the local refusals (no --step with an action, two actions, a
// malformed chain), and the daemon's typed refusal surfaced as the exit.

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestPF25OverrideVerbViewAndActions(t *testing.T) {
	d := newDaemonFixture(t)
	view := protocol.OverrideView{WorkItem: "wi_ov5", Posture: "running", Steps: []protocol.OverrideStepView{
		{ID: "build", Status: "in_progress"},
		{ID: "merge", Skippable: true, Models: []string{"pi:ov/m-1", "pi:ov/m-2"}, ModelsBy: "usr_1",
			Effective: []string{"pi:ov/m-1", "pi:ov/m-2"}, Source: "step override"},
	}}
	raw, _ := json.Marshal(view)
	d.answer("GET", "/jobs/wi_ov5/overrides", string(raw))
	write := protocol.OverrideWriteResult{WorkItem: "wi_ov5", Changed: true, Posture: "running", Published: true,
		Step: protocol.OverrideStepView{ID: "merge", Models: []string{"pi:ov/m-1"}, ModelsBy: "usr_2"}}
	wraw, _ := json.Marshal(write)
	d.answer("POST", "/jobs/wi_ov5/overrides", string(wraw))

	// the view renders the steps, the overlay and the effective chain
	code, out, _ := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "override", "wi_ov5")
	if code != 0 {
		t.Fatalf("view exit = %d, stderr %q", code, out)
	}
	for _, want := range []string{
		"posture:    running",
		"step build          status=in_progress",
		"step merge          status=not started skippable",
		"overlay-models=pi:ov/m-1,pi:ov/m-2 (by usr_1)",
		"next dispatch: pi:ov/m-1,pi:ov/m-2 (step override)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the view lacks %q:\n%s", want, out)
		}
	}

	// one action per call, --step required, the chain validated locally
	for _, bad := range [][]string{
		{"override", "wi_ov5", "--skip"},
		{"override", "wi_ov5", "--step", "merge", "--skip", "--unskip"},
		{"override", "wi_ov5", "--step", "merge", "--models", "no-prefix"},
	} {
		code, _, errs := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, bad...)
		if code != 2 && code != 1 {
			t.Fatalf("%v: exit = %d, want 1 or 2:\n%s%s", bad, code, out, errs)
		}
		if !strings.Contains(errs, "override:") {
			t.Fatalf("%v: the refusal must name the verb: %s", bad, errs)
		}
	}

	// the write's argv and body per action
	cases := []struct {
		args []string
		body string
	}{
		{[]string{"override", "wi_ov5", "--step", "merge", "--models", "pi:ov/m-1,pi:ov/m-2", "--by", "usr_2"},
			`"step":"merge","operator":"usr_2","models":["pi:ov/m-1","pi:ov/m-2"]`},
		{[]string{"override", "wi_ov5", "--step", "merge", "--clear-models"}, `"clear_models":true`},
		{[]string{"override", "wi_ov5", "--step", "merge", "--skip"}, `"skip":true`},
		{[]string{"override", "wi_ov5", "--step", "merge", "--unskip"}, `"skip":false`},
	}
	for _, tc := range cases {
		code, out, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, tc.args...)
		if code != 0 {
			t.Fatalf("%v: exit = %d:\n%s", tc.args, code, out)
		}
		last := d.rec.last()
		if last.Method != "POST" || last.Path != "/jobs/wi_ov5/overrides" {
			t.Fatalf("%v: the last call was %+v, want the override write", tc.args, last)
		}
		if !strings.Contains(string(last.Body), tc.body) {
			t.Fatalf("%v: body %s lacks %s", tc.args, last.Body, tc.body)
		}
		if !strings.Contains(out, "published: the capsule chain carries it") {
			t.Fatalf("%v: the answer's rendering lacks the publish line:\n%s", tc.args, out)
		}
	}

	// the daemon's typed refusal is the exit (an unknown step: 400)
	d.answers["POST /jobs/wi_ov5/overrides"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"code":"DAEMON-400","detail":"the lane declares no step \"nosuch\""}`)
	}
	code, _, errs := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "override", "wi_ov5", "--step", "nosuch", "--skip")
	if code != 1 {
		t.Fatalf("the typed refusal's exit = %d, want 1:\n%s%s", code, out, errs)
	}
	if !strings.Contains(errs, "the lane declares no step") {
		t.Fatalf("the refusal must surface the daemon's words:\n%s", errs)
	}
}

// TestW79OverrideQueuedFace (polyforge#79 u3): the queued posture is the
// verb's face too — the Summary names it, and a queued write's answer
// (posture queued, published false) renders the daemon's note of where
// the change rides.
func TestW79OverrideQueuedFace(t *testing.T) {
	if !strings.Contains(cmdOverride.Summary, "queued") {
		t.Fatalf("the Summary must name the queued posture: %q", cmdOverride.Summary)
	}
	d := newDaemonFixture(t)
	write := protocol.OverrideWriteResult{WorkItem: "wi_w79q", Changed: true, Posture: "queued", Published: false,
		Note: "queued: the params ride the work item's step_params attrs key — the claim merges them into its capsule and clears the key once that publish succeeded",
		Step: protocol.OverrideStepView{ID: "review", Params: []protocol.OverrideParamView{{Name: "image", Value: "reg/x:1", By: "usr_1"}}}}
	raw, _ := json.Marshal(write)
	d.answer("POST", "/jobs/wi_w79q/overrides", string(raw))
	code, out, _ := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock},
		"override", "wi_w79q", "--step", "review", "--param", "image=reg/x:1", "--by", "usr_1")
	if code != 0 {
		t.Fatalf("the queued write's exit = %d, output %q", code, out)
	}
	for _, want := range []string{
		`overlay param image="reg/x:1"`,
		"(posture queued, by usr_1)",
		"the params ride the work item's step_params attrs key",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the queued answer's rendering lacks %q:\n%s", want, out)
		}
	}
}

// TestOverrideNextDispatchHelpAndView (polyforge#270 u4, AC3): the verb's
// help, the view and the write answer all say that a models/tier overlay is
// accepted on a step that already ran or is running and takes effect on
// that step's NEXT dispatch — never the finished round or the live one.
// The tier branch is pinned beside the models branch (a past render only
// handled models).
func TestOverrideNextDispatchHelpAndView(t *testing.T) {
	if !strings.Contains(cmdOverride.Summary, "next dispatch") {
		t.Fatalf("the verb's Summary must name the next dispatch: %q", cmdOverride.Summary)
	}
	fs := flag.NewFlagSet("override", flag.ContinueOnError)
	cmdOverride.Setup(fs)
	for _, name := range []string{"step", "models", "clear-models", "tier", "clear-tier"} {
		f := fs.Lookup(name)
		if f == nil {
			t.Fatalf("the verb declares no --%s", name)
		}
		if !strings.Contains(f.Usage, "next dispatch") {
			t.Fatalf("--%s must say when the overlay applies: %q", name, f.Usage)
		}
	}

	d := newDaemonFixture(t)
	view := protocol.OverrideView{WorkItem: "wi_nd5", Posture: "running", Steps: []protocol.OverrideStepView{
		{ID: "build", Status: "done", Models: []string{"claude:strong"}, ModelsBy: "usr_nd",
			Effective: []string{"claude:strong"}, Source: "step override",
			CanOverrideModels: true, ModelEffectScope: "next_dispatch"},
		{ID: "merge", Status: "in_progress", CanOverrideModels: false,
			ModelOverrideReason: "step merge will not be dispatched again from the work item's current position (no runnable later step can rewind to it): the model overlay is for a step that runs again"},
	}}
	raw, _ := json.Marshal(view)
	d.answer("GET", "/jobs/wi_nd5/overrides", string(raw))
	code, out, _ := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "override", "wi_nd5")
	if code != 0 {
		t.Fatalf("the view exit = %d, stderr %q", code, out)
	}
	for _, want := range []string{
		"step build          status=done",
		"model-overlay=next-dispatch",
		"next dispatch: claude:strong (step override)",
		"step merge          status=in_progress",
		"model-overlay=refused",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the view lacks %q:\n%s", want, out)
		}
	}

	// a done step's models write: the answer says the next dispatch takes it
	write := protocol.OverrideWriteResult{WorkItem: "wi_nd5", Changed: true, Posture: "running", Published: true,
		Note: "the change applies on the step's next dispatch; the round that already ran is unchanged",
		Step: protocol.OverrideStepView{ID: "build", Status: "done", Models: []string{"claude:strong"}, ModelsBy: "usr_nd",
			CanOverrideModels: true, ModelEffectScope: "next_dispatch"}}
	wraw, _ := json.Marshal(write)
	d.answer("POST", "/jobs/wi_nd5/overrides", string(wraw))
	code, out, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock},
		"override", "wi_nd5", "--step", "build", "--models", "claude:strong", "--by", "usr_nd")
	if code != 0 {
		t.Fatalf("the models write exit = %d, stderr %q", code, out)
	}
	for _, want := range []string{
		"overlay models=claude:strong",
		"published: the capsule chain carries it",
		"next dispatch:",
		"the round that already ran is unchanged",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the models answer lacks %q:\n%s", want, out)
		}
	}

	// a tier write: the same next-dispatch line, and the tier's own operator
	tier := protocol.OverrideWriteResult{WorkItem: "wi_nd5", Changed: true, Posture: "running", Published: true,
		Note: "the change applies on the step's next dispatch; the live dispatch is not interrupted",
		Step: protocol.OverrideStepView{ID: "build", Status: "in_progress", Tier: "strong", TierBy: "usr_nd",
			CanOverrideModels: true, ModelEffectScope: "next_dispatch"}}
	traw, _ := json.Marshal(tier)
	d.answer("POST", "/jobs/wi_nd5/overrides", string(traw))
	code, out, _ = runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock},
		"override", "wi_nd5", "--step", "build", "--tier", "strong", "--by", "usr_nd")
	if code != 0 {
		t.Fatalf("the tier write exit = %d, stderr %q", code, out)
	}
	for _, want := range []string{
		"overlay tier=strong",
		"(posture running, by usr_nd)",
		"next dispatch:",
		"the live dispatch is not interrupted",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the tier answer lacks %q:\n%s", want, out)
		}
	}
}
