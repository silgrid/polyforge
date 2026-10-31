package verifyrun

// w137_deadline_clamp_test.go: reproduction — `pf verify-run`
// honours whatever `--deadline` the verification step passes, however far
// past the daemon's supervision tool window (Y, 30m by default) it lies.
//
// On 2026-10-29 the pi candidates' verification steps passed 3480s / 55m /
// 72m / 78m; the daemon's rule 3 judged the single long tool hung at
// exactly Y (10:59:47 -> 11:29:49 for, 11:27:40 for #134),
// killed the run mid-command, result.json was never written and the round
// restarted from fetch/build. The deadline is bounded by what the step's
// own environment says: a claude step carries BASH_MAX_TIMEOUT_MS = Y - 1m
// (1740000 for the default window), a pi step carries no shell-call bound
// at this base (w137_tool_window_env_test.go in internal/polyforge/runtime
// pins that gap).
//
// Red on the pre-fix tree: `--deadline 1h18m` is honoured and recorded
// verbatim (result.json "deadline": "1h18m0s"), whether or not the
// environment implies a smaller bound.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// w137ReportedDeadline is the largest deadline the reported runs passed
// (2026-10-29 10:57:37).
const w137ReportedDeadline = 78 * time.Minute

// w137StepEnvs are the step environments the reported runs came from.
func w137StepEnvs() map[string][]string {
	return map[string][]string{
		// claude: the daemon threads the 30m window in as a 29m Bash max.
		"claude step (BASH_MAX_TIMEOUT_MS = Y - 1m)": append(os.Environ(), "BASH_MAX_TIMEOUT_MS=1740000"),
		// pi: the variable is absent; the value is appended last so a
		// host-set copy cannot mask the reported shape.
		"pi step (no shell-call bound)": append(os.Environ(), "BASH_MAX_TIMEOUT_MS="),
	}
}

// TestW137ExplicitDeadlineClampedToStepBound: an explicit --deadline above
// the bound the step environment implies is clamped to it, so the run can
// never outlive the daemon's tool rule.
func TestW137ExplicitDeadlineClampedToStepBound(t *testing.T) {
	for name, env := range w137StepEnvs() {
		bound := DefaultDeadlineFor(env)
		o := Options{
			CapsuleDir: "/capsule",
			Engine:     "/engine",
			Env:        env,
			Deadline:   w137ReportedDeadline,
		}.withDefaults()
		if o.Deadline > bound {
			t.Errorf("%s: --deadline %s stays %s, above the %s bound the step environment implies",
				name, w137ReportedDeadline, o.Deadline, bound)
		}
	}
}

// TestW137RecordedDeadlineStaysUnderTheStepBound: the deadline the run
// actually uses — the one result.json records for the step — is the
// clamped value, for an explicit --deadline and for the derived default
// alike.
func TestW137RecordedDeadlineStaysUnderTheStepBound(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("unit", "full", "true"))})
	for name, env := range w137StepEnvs() {
		bound := DefaultDeadlineFor(env)
		res := runFixture(t, f, eng, func(o *Options) {
			o.Env = env
			o.Deadline = w137ReportedDeadline
		})
		got, err := time.ParseDuration(res.Deadline)
		if err != nil {
			t.Fatalf("%s: result deadline %q: %v", name, res.Deadline, err)
		}
		if got > bound {
			t.Errorf("%s: the run ran under %s, above the %s bound the step environment implies", name, res.Deadline, bound)
		}
		raw, err := os.ReadFile(filepath.Join(f.capsule, filepath.FromSlash(res.OutDir), "result.json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var doc struct {
			Deadline string `json:"deadline"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: result.json: %v", name, err)
		}
		if doc.Deadline != res.Deadline {
			t.Errorf("%s: result.json records deadline %q, the run ran under %q", name, doc.Deadline, res.Deadline)
		}
		if got, err := time.ParseDuration(doc.Deadline); err != nil || got > bound {
			t.Errorf("%s: result.json deadline %q, want one at or below %s", name, doc.Deadline, bound)
		}
	}
}
