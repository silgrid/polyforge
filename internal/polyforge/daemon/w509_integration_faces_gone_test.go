package daemon

// w509_integration_faces_gone_test.go: the integration
// lane has no read-only face on the daemon — GET /status answers no
// `integration` block, with or without ?integration=1 (a query parameter
// nothing reads), on a Cell and on the Host aggregate; a work item's watch
// snapshot carries no `integration` block, even when its aihub record
// carries the landing record in its attrs.

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// topKeys answers a JSON object's top-level keys.
func topKeys(t *testing.T, body string) map[string]any {
	t.Helper()
	var top map[string]any
	if err := json.Unmarshal([]byte(body), &top); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, body)
	}
	return top
}

func TestW509StatusHasNoIntegrationFace(t *testing.T) {
	td := newTestDaemon(t, nil)
	for _, path := range []string{"/status", "/status?integration=1"} {
		code, body := w14Raw(t, td.sock, "GET", path)
		top := topKeys(t, body)
		if code != 200 || top["jobs"] == nil {
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
		if _, ok := top["integration"]; ok {
			t.Fatalf("GET %s answers an integration block: %s", path, body)
		}
	}
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	code, body := w14Raw(t, th.sock, "GET", "/status?integration=1")
	top := topKeys(t, body)
	if cells, _ := top["cells"].([]any); code != 200 || len(cells) != 2 {
		t.Fatalf("the Host aggregate: %d %s", code, body)
	}
	if _, ok := top["integration"]; ok {
		t.Fatalf("the Host aggregate answers an integration block: %s", body)
	}
}

func TestW509WatchSnapshotHasNoIntegrationFace(t *testing.T) {
	td := newTestDaemon(t, nil)
	item := &api.WorkItem{ID: "wi_pub", Slug: "demo#1", Status: api.StatusWrapped, Attrs: map[string]any{
		"integration": map[string]any{"state": "landed", "merge_commits": map[string]any{"app": []any{"def"}},
			"repos": []any{map[string]any{"repo": "app", "pr": "https://github.com/o/r/pull/7", "head": "abc", "light_verified_head": "abc", "merge_commit": "def"}}},
	}}
	raw, err := json.Marshal(td.watchSnapshot("wi_pub", item, true, 0))
	if err != nil {
		t.Fatal(err)
	}
	top := topKeys(t, string(raw))
	// the snapshot read the record (its status), and nothing of its attrs
	if top["hub_status"] != api.StatusWrapped || top["slug"] != "demo#1" {
		t.Fatalf("the snapshot did not read the work item's record: %s", raw)
	}
	if _, ok := top["integration"]; ok {
		t.Fatalf("the watch snapshot carries an integration block: %s", raw)
	}
}
