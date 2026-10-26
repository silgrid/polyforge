package observe

// view_test.go: the events-file JSONL shape.

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestEventsFileShapeIsJSONLEventEntry(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, nil)
	// a driver event lands in the same durable log through the daemon's
	// binding (the driver's EventLogger IS EventLog.Append)
	if err := m.log.Append("wi_test", "needs_input", map[string]any{"question_id": "q_file", "step": "code"}); err != nil {
		t.Fatal(err)
	}

	// The durable record: every entry exists as ONE JSONL line under
	// the daemon data dir, parseable as protocol.EventEntry.
	soon(t, time.Second, "needs_input event persisted", func() bool {
		entries, err := ReadEventsDir(filepath.Dir(m.log.dir))
		if err != nil {
			return false
		}
		for _, e := range entries {
			if e.Kind == "needs_input" && e.WI == "wi_test" && e.At != "" {
				return true
			}
		}
		return false
	})
	entries, err := ReadEventsDir(filepath.Dir(m.log.dir))
	if err != nil {
		t.Fatal(err)
	}
	var started bool
	for _, e := range entries {
		switch e.Kind {
		case EvJobStarted:
			started = true
		case "needs_input":
			var b struct {
				QuestionID string `json:"question_id"`
			}
			if err := json.Unmarshal(e.Body, &b); err != nil || b.QuestionID != "q_file" {
				t.Fatalf("needs_input body: %s %v", e.Body, err)
			}
		}
		if e.WI != "wi_test" {
			t.Fatalf("event must carry its work item: %+v", e)
		}
	}
	if !started {
		t.Fatal("job_started must be in the work item's file")
	}
}
