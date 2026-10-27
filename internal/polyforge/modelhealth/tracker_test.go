package modelhealth

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

func writeEvents(t *testing.T, dataDir, wi string, entries []protocol.EventEntry) {
	t.Helper()
	dir := filepath.Join(dataDir, observe.EventsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	name := "daemon.jsonl"
	if wi != "" {
		name = "wi-" + wi + ".jsonl"
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func threeDeaths() []protocol.EventEntry {
	return []protocol.EventEntry{
		startEvent("wi", 1, at(-time.Hour), "glm", "", "code", 1),
		failEvent("wi", 2, at(-time.Hour+time.Minute), "code", "model_error", 1),
		startEvent("wi", 3, at(-50*time.Minute), "glm", "", "code", 2),
		failEvent("wi", 4, at(-50*time.Minute+time.Minute), "code", "model_error", 2),
		startEvent("wi", 5, at(-40*time.Minute), "glm", "", "code", 3),
		failEvent("wi", 6, at(-40*time.Minute+time.Minute), "code", "model_error", 3),
	}
}

func TestOpenRebuildsIdenticalFile(t *testing.T) {
	dir := t.TempDir()
	writeEvents(t, dir, "wi", threeDeaths())
	clock := func() time.Time { return w14Now }
	tr, err := Open(dir, 30*24*time.Hour, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ProfileFile)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first, []byte("glm")) || !bytes.Contains(first, []byte(`"window"`)) {
		t.Fatalf("snapshot must carry cells and the window: %s", first)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	tr2, err := Open(dir, 30*24*time.Hour, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tr2.Close()
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("rebuild differs from the deleted snapshot:\nfirst:  %s\nsecond: %s", first, second)
	}
	if tmp := tmpFiles(t, dir); len(tmp) != 0 {
		t.Fatalf("atomic replacement left temp files: %v", tmp)
	}
}

func TestLiveIngestAgreesWithFreshBuild(t *testing.T) {
	dir := t.TempDir()
	clock := func() time.Time { return w14Now }
	tr, err := Open(dir, 30*24*time.Hour, clock, nil)
	if err != nil {
		t.Fatal(err)
	}
	es := threeDeaths()
	for _, e := range es {
		tr.Ingest(e)
	}
	// sync waits for the tracker goroutine to fold the queue; the in-memory
	// view it serves must then match a fresh build over the same entries.
	tr.sync()
	live := tr.Profile(w14Now)
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, _ := Build(es, w14Now, 30*24*time.Hour)
	if len(live.Cells) != len(fresh.Cells) {
		t.Fatalf("cell count differs: live=%+v fresh=%+v", live.Cells, fresh.Cells)
	}
	for i := range live.Cells {
		if live.Cells[i] != fresh.Cells[i] {
			t.Fatalf("cell %d differs: live=%+v fresh=%+v", i, live.Cells[i], fresh.Cells[i])
		}
	}
}

func TestAlertOnceThenRearm(t *testing.T) {
	dir := t.TempDir()
	writeEvents(t, dir, "wi", threeDeaths())
	now := w14Now
	clock := func() time.Time { return now }
	var fired []Alert
	tr, err := Open(dir, 30*24*time.Hour, clock, func(a Alert) { fired = append(fired, a) })
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.sync()
	if len(fired) != 1 || fired[0].Model != "glm" {
		t.Fatalf("first crossing must fire exactly one alert: %+v", fired)
	}
	// Further deaths in the same episode do not re-fire.
	tr.Ingest(failEvent("wi", 7, at(-30*time.Minute), "code", "model_error", 4))
	tr.sync()
	if len(fired) != 1 {
		t.Fatalf("an ongoing episode must not re-fire: %+v", fired)
	}
	// The episode clears once the original deaths fall out of the 24h
	// window; a later deterioration reports afresh.
	now = w14Now.Add(25 * time.Hour)
	tr.Ingest(closeEvent("other", 1, now.Add(-time.Minute), kindStepDone, "code", 1))
	tr.sync()
	if len(fired) != 1 {
		t.Fatalf("clearing must not emit: %+v", fired)
	}
	// Re-arm with three new deaths inside the new 24h window.
	base := now
	for i, when := range []time.Time{base.Add(-time.Hour), base.Add(-50 * time.Minute), base.Add(-40 * time.Minute)} {
		start := int64(10 + i*2)
		tr.Ingest(startEvent("wi", start, when, "glm", "", "code", start))
		tr.Ingest(failEvent("wi", start+1, when.Add(time.Minute), "code", "model_error", start))
	}
	tr.sync()
	if len(fired) != 2 {
		t.Fatalf("a later deterioration must report afresh: %+v", fired)
	}
}

// TestObserverDoesNoRebuildOrEmitInCallerGoroutine pins the B1 fix: the
// append observer (Ingest) only enqueues. Holding the aggregation lock
// blocks the tracker goroutine's fold, so a synchronous rebuild or emit
// in the caller would be visible here.
func TestObserverDoesNoRebuildOrEmitInCallerGoroutine(t *testing.T) {
	dir := t.TempDir()
	clock := func() time.Time { return w14Now }
	fired := make(chan Alert, 4)
	tr, err := Open(dir, 30*24*time.Hour, clock, func(a Alert) { fired <- a })
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	tr.mu.Lock()
	for _, e := range threeDeaths() {
		tr.Ingest(e)
	}
	if len(tr.entries) != 0 {
		tr.mu.Unlock()
		t.Fatalf("Ingest rebuilt the profile in the caller's goroutine: %d entries", len(tr.entries))
	}
	select {
	case a := <-fired:
		tr.mu.Unlock()
		t.Fatalf("Ingest emitted in the caller's goroutine: %+v", a)
	default:
	}
	tr.mu.Unlock()

	tr.sync()
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("the tracker goroutine never emitted the crossing alert")
	}
}

func TestRestartSuppressesOngoingEpisode(t *testing.T) {
	dir := t.TempDir()
	es := append(threeDeaths(), entry("", kindUnhealthy, 1, at(-30*time.Minute),
		map[string]any{"model": "glm", "rule": RuleDeaths24h, "rules": []string{RuleDeaths24h}, "model_deaths": 3}))
	writeEvents(t, dir, "wi", es[:6])
	writeEvents(t, dir, "", es[6:])
	clock := func() time.Time { return w14Now }
	var fired []Alert
	tr, err := Open(dir, 30*24*time.Hour, clock, func(a Alert) { fired = append(fired, a) })
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.sync()
	if len(fired) != 0 {
		t.Fatalf("a restart over a still-unhealthy already-reported episode must emit nothing: %+v", fired)
	}
}

func tmpFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "."+ProfileFile+".*.tmp"))
	return m
}
