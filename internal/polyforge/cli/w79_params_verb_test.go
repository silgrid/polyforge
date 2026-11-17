package cli

// w79_params_verb_test.go (u2, polyforge#79): the `pf params <id>` verb
// over a fake daemon — the view's rendering (per step: the status and
// every declared param's final value with its source) and the one GET it
// makes.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestW79ParamsVerbView(t *testing.T) {
	d := newDaemonFixture(t)
	view := protocol.ParamsView{WorkItem: "wi_w79c", Posture: "queued", Steps: []protocol.ParamsStepView{
		{ID: "build", Status: "", Params: []protocol.ParamFinalView{
			{Name: "image", Value: "base-img", Source: "bundle demo step build params.image"},
		}},
		{ID: "review", Status: "", Params: []protocol.ParamFinalView{
			{Name: "flag", Value: "on", Source: "step override"},
			{Name: "image", Value: "reg/x:1", Source: "steps.proj.review.params.image"},
		}},
	}}
	raw, _ := json.Marshal(view)
	d.answer("GET", "/jobs/wi_w79c/params", string(raw))

	code, out, _ := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "params", "wi_w79c")
	if code != 0 {
		t.Fatalf("view exit = %d, output %q", code, out)
	}
	for _, want := range []string{
		"posture:    queued",
		"step build          status=not started",
		`param image="base-img" (bundle demo step build params.image)`,
		"step review         status=not started",
		`param flag="on" (step override)`,
		`param image="reg/x:1" (steps.proj.review.params.image)`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the view lacks %q:\n%s", want, out)
		}
	}
	if last := d.rec.last(); last.Method != "GET" || last.Path != "/jobs/wi_w79c/params" {
		t.Fatalf("the verb makes exactly the one view read: %+v", last)
	}
	// the daemon's typed refusal is the exit
	d.answers["GET /jobs/wi_w79c/params"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		io.WriteString(w, `{"code":"DAEMON-409","detail":"the lane does not load on this daemon"}`)
	}
	code, _, errs := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "params", "wi_w79c")
	if code != 1 {
		t.Fatalf("the typed refusal's exit = %d, want 1:\n%s", code, errs)
	}
	if !strings.Contains(errs, "the lane does not load on this daemon") {
		t.Fatalf("the refusal must surface the daemon's words:\n%s", errs)
	}
}
