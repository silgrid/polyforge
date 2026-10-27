package task

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStates(t *testing.T) {
	for _, s := range []State{StateCompleted, StateCapped, StateFailed, StateInterrupted} {
		if !s.Terminal() {
			t.Fatalf("%s must be terminal", s)
		}
	}
	if StateRunning.Terminal() {
		t.Fatal("running is not terminal")
	}
	// capped and failed are distinct values on every surface.
	if StateCapped == StateFailed {
		t.Fatal("capped must be distinguishable from failed")
	}
}

func TestBudgets(t *testing.T) {
	if DefaultMaxDuration != 20*time.Minute || DefaultMaxTokens != 300000 {
		t.Fatalf("defaults changed: %s / %d", DefaultMaxDuration, DefaultMaxTokens)
	}
	if MaxDuration != 60*time.Minute || MaxTokens != 1000000 {
		t.Fatalf("ceilings changed: %s / %d", MaxDuration, MaxTokens)
	}
	for _, d := range []time.Duration{time.Second, DefaultMaxDuration, MaxDuration} {
		if err := ValidateDuration(d); err != nil {
			t.Fatalf("%s must be accepted: %v", d, err)
		}
	}
	if err := ValidateDuration(2 * time.Hour); err == nil || !strings.Contains(err.Error(), "60m") {
		t.Fatalf("2h must refuse naming the 60m ceiling: %v", err)
	}
	if _, err := ParseDuration("2h"); err == nil || !strings.Contains(err.Error(), "60m") {
		t.Fatalf("ParseDuration(2h) must refuse naming the ceiling: %v", err)
	}
	if d, err := ParseDuration("5m"); err != nil || d != 5*time.Minute {
		t.Fatalf("ParseDuration(5m): %s, %v", d, err)
	}
	for _, n := range []int64{1, DefaultMaxTokens, MaxTokens} {
		if err := ValidateTokens(n); err != nil {
			t.Fatalf("%d must be accepted: %v", n, err)
		}
	}
	if err := ValidateTokens(2000000); err == nil || !strings.Contains(err.Error(), "1000000") {
		t.Fatalf("2000000 must refuse naming the ceiling: %v", err)
	}
	if _, err := ParseTokens("2000000"); err == nil || !strings.Contains(err.Error(), "1000000") {
		t.Fatalf("ParseTokens(2000000) must refuse naming the ceiling: %v", err)
	}
}

func TestID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidID(id) || !strings.HasPrefix(id, "task_") || len(id) != len("task_")+16 {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "task_", "task_0123456789abcde", "task_0123456789ABCDEF", "chat_0123456789abcdef", "task_0123456789abcdef0"} {
		if ValidID(bad) {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

func TestSummary(t *testing.T) {
	tk := Task{ID: "task_0123456789abcdef", State: StateCapped, Report: "hello world", Truncated: true}
	s := tk.Summary()
	for _, want := range []string{"task_0123456789abcdef", "capped", "hello world", "truncated", "pf task show task_0123456789abcdef"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary %q must contain %q", s, want)
		}
	}
	if strings.Contains(s, "\n") {
		t.Fatal("summary must be one line")
	}
	long := Task{ID: "task_0123456789abcdef", State: StateCompleted, Report: strings.Repeat("x", SummaryTailChars+100)}
	if got := long.Summary(); len(got) > SummaryTailChars+200 {
		t.Fatalf("summary not bounded: %d chars", len(got))
	}
}

// TestJSONDurationFieldsNameTheirUnit pins the machine-readable task
// document: time.Duration marshals as an integer count of nanoseconds, so
// the JSON names must say _ns (not _ms) and the emitted numbers must be
// those nanoseconds. The daemon routes (GET /task/list, GET /task/show),
// `pf task list/show --json` and the report store all share these tags.
func TestJSONDurationFieldsNameTheirUnit(t *testing.T) {
	const wall = 7*time.Second + 728*time.Millisecond + 480*time.Microsecond + 45*time.Nanosecond
	tk := Task{
		ID:           "task_0123456789abcdef",
		Spec:         Spec{Project: "demo", MaxDuration: DefaultMaxDuration, MaxTokens: DefaultMaxTokens},
		State:        StateCompleted,
		WallConsumed: wall,
	}
	raw, err := json.Marshal(tk)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Spec struct {
			MaxDurationNS *int64 `json:"max_duration_ns"`
		} `json:"spec"`
		WallConsumedNS *int64 `json:"wall_consumed_ns"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Spec.MaxDurationNS == nil || doc.WallConsumedNS == nil {
		t.Fatalf("duration JSON names must state the unit (nanoseconds): %s", raw)
	}
	if got, want := *doc.Spec.MaxDurationNS, int64(DefaultMaxDuration); got != want {
		t.Fatalf("max_duration_ns = %d, want %d (20m in ns)", got, want)
	}
	if got, want := *doc.WallConsumedNS, int64(wall); got != want {
		t.Fatalf("wall_consumed_ns = %d, want %d", got, want)
	}
	// The same names must decode back to the same durations for the CLI's
	// --json path and the report store.
	var back Task
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.MaxDuration != DefaultMaxDuration || back.WallConsumed != wall {
		t.Fatalf("round trip changed durations: max=%s wall=%s", back.MaxDuration, back.WallConsumed)
	}
}
