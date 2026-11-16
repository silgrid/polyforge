package daemon

// w51_unclassified_route_test.go (polyforge#51): the Host-level list of
// fallback-classified error texts — the scan's window, dedup and
// classification filter, the cross-Cell aggregation of the route, and the
// user-only reach (a step or task agent's process tree is refused).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// w51StepFailed is one step_failed event line carrying the classification
// the driver recorded.
func w51StepFailed(at time.Time, cause, classifiedBy, errorText string) protocol.EventEntry {
	body, _ := json.Marshal(map[string]any{
		"step": "build", "cause": cause, "detail": "driver: ...",
		"classified_by": classifiedBy, "error_text": errorText,
	})
	return protocol.EventEntry{At: at.UTC().Format(time.RFC3339Nano), Kind: "step_failed", WI: "wi_x", Body: body}
}

// w51SeedEvents writes one work item's event log in a Cell's data dir.
func w51SeedEvents(t *testing.T, dataDir, wi string, entries ...protocol.EventEntry) {
	t.Helper()
	dir := filepath.Join(dataDir, "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "wi-"+wi+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// TestW51ScanUnclassifiedWindowDedup: only fallback-classified
// step_failed events of the window count, deduplicated by the raw error
// text; a step-agent-relevant field never appears (the texts are all the
// report carries).
func TestW51ScanUnclassifiedWindowDedup(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	twoDaysAgo := now.Add(-2 * 24 * time.Hour)
	nineDaysAgo := now.Add(-9 * 24 * time.Hour)
	w51SeedEvents(t, dir, "wi_1",
		w51StepFailed(twoDaysAgo, "model_error", "fallback", "gateway tantrum A"),
		w51StepFailed(twoDaysAgo.Add(time.Hour), "exit_model_class", "fallback", "gateway tantrum A"),
		w51StepFailed(twoDaysAgo, "rate_limited", "builtin", "429 Too Many Requests"),
		w51StepFailed(twoDaysAgo, "upstream_transient", "added", "gateway hiccup"),
		w51StepFailed(twoDaysAgo, "model_error", "fallback", ""), // no text: nothing to list
		w51StepFailed(nineDaysAgo, "model_error", "fallback", "old gateway tantrum"),
	)
	items := scanUnclassifiedEvents(dir, cli.UnclassifiedWindow, now)
	if len(items) != 1 {
		t.Fatalf("fallback texts of the window = %v, want exactly the one deduplicated text", items)
	}
	it := items["gateway tantrum A"]
	if it == nil || it.Count != 2 || it.Text != "gateway tantrum A" || it.LastAt == "" {
		t.Fatalf("the item must count both occurrences: %+v", it)
	}
	// an empty events dir is an empty report, not an error
	if items := scanUnclassifiedEvents(t.TempDir(), cli.UnclassifiedWindow, now); len(items) != 0 {
		t.Fatalf("no events = no items: %v", items)
	}
}

// TestW51UnclassifiedRouteAggregatesCells: the Host route sums every
// serving Cell's fallback texts (the same text in two Cells is one row
// with both counts), newest-first among equals, count-first overall.
func TestW51UnclassifiedRouteAggregatesCells(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	at := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339Nano)
	w51SeedEvents(th.cellDataDir("default"), "wi_d1",
		w51StepFailed(time.Now().Add(-24*time.Hour), "model_error", "fallback", "gateway tantrum A"))
	w51SeedEvents(th.cellDataDir("alpha"), "wi_a1",
		w51StepFailed(time.Now().Add(-25*time.Hour), "exit_model_class", "fallback", "gateway tantrum A"),
		w51StepFailed(time.Now().Add(-26*time.Hour), "model_error", "fallback", "upstream wailed mysteriously"))
	raw, err := th.client("").Unclassified()
	if err != nil {
		t.Fatalf("ops unclassified: %v", err)
	}
	var rep cli.UnclassifiedReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("the answer: %v (%s)", err, raw)
	}
	if rep.WindowDays != 7 {
		t.Fatalf("window_days = %d, want 7", rep.WindowDays)
	}
	if len(rep.Items) != 2 {
		t.Fatalf("items %+v, want the two deduplicated texts", rep.Items)
	}
	if rep.Items[0].Text != "gateway tantrum A" || rep.Items[0].Count != 2 {
		t.Fatalf("the cross-Cell text must count both Cells: %+v", rep.Items[0])
	}
	if rep.Items[0].LastAt != at {
		t.Fatalf("last_at = %s, want the newest occurrence %s", rep.Items[0].LastAt, at)
	}
	if rep.Items[1].Text != "upstream wailed mysteriously" || rep.Items[1].Count != 1 {
		t.Fatalf("the second row: %+v", rep.Items[1])
	}
}

// TestW51UnclassifiedRouteRefusesAgents: the route serves a human session
// only — a caller the Host resolves to a step process tree is refused
// DAEMON-403 (AC4: step agents cannot reach it).
func TestW51UnclassifiedRouteRefusesAgents(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	restore := runtimeStepOrigin
	runtimeStepOrigin = func(pid int, id string) (string, bool) { return "wi_a#7", true }
	t.Cleanup(func() { runtimeStepOrigin = restore })
	_, err := th.client("").Unclassified()
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a step agent's process tree must be refused, got %v", err)
	}
}
