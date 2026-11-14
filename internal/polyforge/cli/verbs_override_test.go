package cli

// verbs_override_test.go (u5, polyforge#25): the `pf override` verb over
// a fake daemon — the view's rendering, the write's argv and body per
// action, the local refusals (no --step with an action, two actions, a
// malformed chain), and the daemon's typed refusal surfaced as the exit.

import (
	"encoding/json"
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
