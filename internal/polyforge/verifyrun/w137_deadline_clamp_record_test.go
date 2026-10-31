package verifyrun

// w137_deadline_clamp_record_test.go: AC2 — when an explicit
// --deadline above the step's tool bound is clamped, result.json says so:
// deadline is the value the run used, deadline_clamped_from the value the
// caller asked for. The deadline values themselves are pinned in
// w137_deadline_clamp_test.go; this pins the visible record.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

func TestW137ClampedRequestRecorded(t *testing.T) {
	f := touched(t)
	eng := fakeEngine(t, map[string]any{"app": report("full", cmdOf("unit", "full", "true"))})
	env := setEnv(setEnv(os.Environ(), "BASH_MAX_TIMEOUT_MS", ""), spawn.EnvToolWindow, "1800000")
	res := runFixture(t, f, eng, func(o *Options) {
		o.Env = env
		o.Deadline = 78 * time.Minute
	})
	if res.DeadlineClampedFrom != "1h18m0s" {
		t.Errorf("deadline_clamped_from = %q, want the requested 1h18m0s", res.DeadlineClampedFrom)
	}
	if got, err := time.ParseDuration(res.Deadline); err != nil || got != 28*time.Minute {
		t.Errorf("deadline = %q, want the 28m0s bound", res.Deadline)
	}
	raw, err := os.ReadFile(filepath.Join(f.capsule, filepath.FromSlash(res.OutDir), "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Deadline        string `json:"deadline"`
		DeadlineClamped string `json:"deadline_clamped_from"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Deadline != res.Deadline || doc.DeadlineClamped != "1h18m0s" {
		t.Errorf("result.json deadline=%q clamped_from=%q", doc.Deadline, doc.DeadlineClamped)
	}
}
