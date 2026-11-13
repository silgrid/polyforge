package daemon

// owner_routes_test.go: workflow contract 4 C3 — the
// daemon serves no owner waive or rewind route (`pf waive` / `pf rewind`
// were removed; the owner tells the step), while the doors `pf resume`
// takes stay; and the owner_rewind event a hand-off citing the owner lands
// still resolves the decision feed's open entries of the work item.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

func TestNoOwnerWaiveOrRewindRoute(t *testing.T) {
	mux := (&Daemon{}).routes()
	for _, path := range []string{"/jobs/wi_x/waive", "/jobs/wi_x/rewind"} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, path, nil)); pattern != "" {
			t.Fatalf("POST %s is served (%q); the route was removed", path, pattern)
		}
	}
	for _, path := range []string{"/jobs/wi_x/claim", "/jobs/wi_x/resume-paused"} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodPost, path, nil)); pattern == "" {
			t.Fatalf("POST %s (a door `pf resume` takes) is not served", path)
		}
	}
}

// The owner's rewind ends the dispatch it was handed off from: the
// question it had open and a block it lifted are over.
func TestOwnerRewindResolvesTheFeed(t *testing.T) {
	f := testFeed(t)
	f.Ingest("default", evAt("wi_1", "awaiting_owner", `{"question_id":"q-1"}`), nil)
	f.Ingest("default", evAt("wi_1", driver.EvOwnerRewind, `{"from":"review","to":"plan"}`), nil)
	if e := newest(f); !e.Resolved || f.List().Attention != 0 {
		t.Fatalf("the ended dispatch's question is still open: %+v", e)
	}
}
