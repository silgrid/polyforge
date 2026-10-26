package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

// queue_fetch_denied edge semantics: an authorization-class denial of the
// auto-pull wave fetch (authDenied: typed error, HTTP 401/403/404) raises the
// typed denial signal exactly once per healthy→denied episode, the
// episode clears on the next successful fetch, and the event lands in
// the durable events log at daemon scope (wi="") with the payload the
// e2e pins (project + class=authorization + error). Transport-class
// losses stay silent.
func TestQueueFetchDeniedEdgeSemantics(t *testing.T) {
	td := newTestDaemon(t, nil)
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	eventsPath := filepath.Join(td.DataDirPath(), observe.EventsDir, "daemon.jsonl") // the daemon-scope file

	count := func() int {
		raw, err := os.ReadFile(eventsPath)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"`+observe.EvQueueFetchDenied+`"`) {
				n++
			}
		}
		return n
	}

	// Authorization-class denial (the admin revoked the machine key):
	// exactly one queue_fetch_denied event lands, and further denied waves
	// do not re-emit (edge-triggered per episode).
	td.hub.mu.Lock()
	td.hub.queueErr = &api.Error{Code: "CORE-401", Detail: "api key revoked"}
	td.hub.mu.Unlock()
	waitUntilCount(t, 5*time.Second, count, 1)
	time.Sleep(300 * time.Millisecond) // many denied waves at DrainInterval 10ms
	if n := count(); n != 1 {
		t.Fatalf("denied waves re-emitted: %d queue_fetch_denied events, want 1", n)
	}

	// The event is daemon-scope with the pinned payload shape.
	raw, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	var hit map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, observe.EvQueueFetchDenied) {
			continue
		}
		var e struct {
			WI   string          `json:"work_item"`
			Kind string          `json:"kind"`
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("event line undecodable: %v (%s)", err, line)
		}
		if e.Kind != observe.EvQueueFetchDenied {
			continue
		}
		if e.WI != "" {
			t.Fatalf("queue_fetch_denied must be daemon-scope (wi=\"\"), got wi=%q", e.WI)
		}
		hit = map[string]any{}
		if err := json.Unmarshal(e.Body, &hit); err != nil {
			t.Fatalf("payload undecodable: %v", err)
		}
		break
	}
	if hit == nil {
		t.Fatal("no queue_fetch_denied event found")
	}
	if hit["project"] != "proj" || hit["class"] != "authorization" {
		t.Fatalf("payload wrong: %v", hit)
	}
	if s, _ := hit["error"].(string); !strings.Contains(s, "revoked") {
		t.Fatalf("payload error summary wrong: %v", hit["error"])
	}

	// A successful fetch clears the episode: the NEXT denial emits a
	// second event (denied→denied=1, denied→ok→denied=2).
	td.hub.mu.Lock()
	td.hub.queueErr = nil
	td.hub.mu.Unlock()
	time.Sleep(200 * time.Millisecond) // ~20 clean fetches at DrainInterval 10ms
	if n := count(); n != 1 {
		t.Fatalf("clean fetches must not emit: %d queue_fetch_denied events, want 1", n)
	}
	td.hub.mu.Lock()
	td.hub.queueErr = &api.Error{Code: "CORE-403", Detail: "membership removed"}
	td.hub.mu.Unlock()
	waitUntilCount(t, 5*time.Second, count, 2)

	// Transport-class loss stays silent idle: no new event kind, no
	// extra emission, count unchanged after many failing waves.
	td.hub.mu.Lock()
	td.hub.queueErr = &api.Error{Code: "CORE-500", Detail: "backend down"}
	td.hub.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	if n := count(); n != 2 {
		t.Fatalf("transport-class loss emitted events: %d, want 2", n)
	}
}

// DataDirPath exposes the daemon's data directory to the test (the
// events log lives next to the runtime state there).
func (d *Daemon) DataDirPath() string { return d.cfg.DataDir }

func waitUntilCount(t *testing.T, bound time.Duration, count func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if n := count(); n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("count never reached %d (now %d)", want, count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
