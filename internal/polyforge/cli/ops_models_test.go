package cli

// ops_models_test.go: `pf ops models` and the model-health
// summary in `pf ops status`, driven through the real CLI over a fake
// daemon socket.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
)

const w14ProfileJSON = `{"window":{"duration":"720h0m0s","from":"2026-09-25T03:00:00Z","to":"2026-10-25T03:00:00Z"},` +
	`"cells":[` +
	`{"model":"glm-5.3-flash","step":"verification","dispatches":6,"successes":1,"model_deaths":2,"non_model_deaths":2,"excluded_deaths":1,"rate":0.3333333333333333,"mean_wall_ms":60000,"last_cause":"mystery_cause"},` +
	`{"model":"foo","step":"fix","dispatches":1,"successes":0,"model_deaths":0,"non_model_deaths":0,"excluded_deaths":0,"rate":0,"mean_wall_ms":0}` +
	`],"warnings":[{"model":"glm-5.3-flash","step":"","rule":"3 model deaths in 24h","model_deaths":3}]}`

func TestOpsModelsTableAndJSON(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("GET", "/models", w14ProfileJSON)
	env := map[string]string{"PF_DAEMON_SOCKET": d.sock}

	code, out, stderr := runPf(t, env, "ops", "models")
	if code != 0 {
		t.Fatalf("ops models: code=%d stderr=%s", code, stderr)
	}
	low := strings.ToLower(out)
	for _, want := range []string{"window", "model", "non-model", "last-cause", "glm-5.3-flash", "verification", "mystery_cause"} {
		if !strings.Contains(low, strings.ToLower(want)) {
			t.Fatalf("ops models output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "33.3%") {
		t.Fatalf("rate must render as a percentage:\n%s", out)
	}
	if strings.Contains(out, "foo:") {
		t.Fatalf("an empty effort must render as the model alone, no trailing colon:\n%s", out)
	}

	code, jsonOut, stderr := runPf(t, env, "--json", "ops", "models")
	if code != 0 {
		t.Fatalf("ops models --json: code=%d stderr=%s", code, stderr)
	}
	var fromRoute, fromCLI any
	if err := json.Unmarshal([]byte(w14ProfileJSON), &fromRoute); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &fromCLI); err != nil {
		t.Fatalf("--json output undecodable: %v (%s)", err, jsonOut)
	}
	if !reflect.DeepEqual(fromRoute, fromCLI) {
		t.Fatalf("--json must re-emit the route document unchanged; route=%v cli=%v", fromRoute, fromCLI)
	}
}

func TestOpsStatusModelHealthSummary(t *testing.T) {
	d := newDaemonFixture(t)
	d.answer("GET", "/status", `{"machine_id":"m1","auto":false,"workspaces":[],"shutting_down":false,"jobs":[],`+
		`"queue":{"pending":0,"running":0,"max_concurrent":2},`+
		`"model_health":{"window":{"duration":"720h0m0s","from":"f","to":"t"},`+
		`"warnings":[{"model":"glm-5.3-flash","rule":"3 model deaths in 24h","model_deaths":3}]}}`)
	code, out, stderr := runPf(t, map[string]string{"PF_DAEMON_SOCKET": d.sock}, "ops", "status")
	if code != 0 {
		t.Fatalf("ops status: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(out, "glm-5.3-flash") || !strings.Contains(out, "pf ops models") {
		t.Fatalf("warning summary must name the model and point at `pf ops models`:\n%s", out)
	}

	silent := newDaemonFixture(t)
	silent.answer("GET", "/status", `{"machine_id":"m1","auto":false,"workspaces":[],"shutting_down":false,"jobs":[],`+
		`"queue":{"pending":0,"running":0,"max_concurrent":2},"model_health":{"window":{"duration":"720h0m0s","from":"f","to":"t"},"warnings":[]}}`)
	code, out, stderr = runPf(t, map[string]string{"PF_DAEMON_SOCKET": silent.sock}, "ops", "status")
	if code != 0 {
		t.Fatalf("ops status (silent): code=%d stderr=%s", code, stderr)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "no model") || !strings.Contains(low, "warning") {
		t.Fatalf("no warnings must be stated explicitly:\n%s", out)
	}
}

func TestRenderModelsEmptyEffortKey(t *testing.T) {
	// The key helper is the single rendering rule; the table uses it.
	if got := modelhealth.Key("foo", ""); got != "foo" {
		t.Fatalf("empty effort key = %q, want foo", got)
	}
	if got := modelhealth.Key("foo", "high"); got != "foo:high" {
		t.Fatalf("key = %q, want foo:high", got)
	}
}
