package daemon

// w432_integration_lane_test.go ( AC2): the
// daemon's integration hub never guesses a lane: a fix ticket is filed on
// the lane its `pf step hub file --lane` names, looked up on that lane
// before filing, and a ticket naming none is refused at the call rather
// than filed on a guessed lane.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

func TestW432HubListsAndFilesOnTheNamedLanes(t *testing.T) {
	var mu sync.Mutex
	var listed, created []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Query().Get("bundle") != "":
			listed = append(listed, r.URL.Query().Get("bundle"))
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		case r.Method == "GET":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		case r.Method == "POST":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			b, _ := body["bundle_name"].(string)
			created = append(created, b)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "wi_fix", "slug": "p#101"})
		}
	}))
	t.Cleanup(hub.Close)
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k"; c.Hub = aihubclient.New(hub.URL, "k") })
	if _, err := (stepHubSink{td.Daemon}).File(context.Background(), stepcall.Op{Verb: stepcall.HubFile, Lane: "builder", Key: "k1", Project: "p", Goal: "fix it", Content: "c"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if strings.Join(listed, ",") != "builder" || strings.Join(created, ",") != "builder" {
		t.Fatalf("listed %v, created %v: the ticket looked up and filed on its lane", listed, created)
	}
	mu.Unlock()
	if err := stepcall.CheckOp(stepcall.Op{Verb: stepcall.HubFile, Key: "k2", Goal: "fix it", Content: "c"}); err == nil || !strings.Contains(err.Error(), "--lane is required") {
		t.Fatalf("a ticket naming no lane is refused at the call: %v", err)
	}
}
