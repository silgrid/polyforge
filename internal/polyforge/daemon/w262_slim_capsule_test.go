package daemon

// w262_slim_capsule_test.go: `pf doctor --prune-capsules`
// slims a wrapped work item's capsule to artifacts/ + capsule.json. The
// daemon's read faces must keep answering for such a capsule: `pf watch`
// (GET /jobs/{wi}/watch) serves the hub record's status and slug — with
// runtime-state/ gone there is no execution to show, and none is
// fabricated — and the artifact face answers a terminal work item's
// pointer while the artifact itself still resolves, under the same
// path guard the holder read uses, in the capsule the daemon names for it.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestW262SlimmedCapsuleStillAnswersWatchAndArtifacts(t *testing.T) {
	const wi, slug = "wi_slim", "proj#7"
	td := newTestDaemon(t, nil)
	td.hub.mu.Lock()
	if td.hub.slugs == nil {
		td.hub.slugs = map[string]string{}
	}
	td.hub.slugs[wi] = slug
	td.hub.status[wi] = api.StatusWrapped
	td.hub.mu.Unlock()

	// The slimmed capsule: capsule.json and artifacts/ only.
	ws, err := td.Daemon.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(ws, "capsules", api.PathName(slug, wi))
	if err := mkdirWrite(dir, capsule.CapsuleJSON, `{"work_item_id":"`+wi+`"}`); err != nil {
		t.Fatal(err)
	}
	if err := mkdirWrite(filepath.Join(dir, capsule.DirArtifacts), "plan.md", "# plan\n"); err != nil {
		t.Fatal(err)
	}

	snap, err := td.client.WatchSnapshot(wi, 0, false)
	if err != nil {
		t.Fatalf("watch of a slimmed capsule: %v", err)
	}
	if snap.HubStatus != api.StatusWrapped || snap.Slug != slug {
		t.Fatalf("watch snapshot: hub status %q slug %q, want %q %q", snap.HubStatus, snap.Slug, api.StatusWrapped, slug)
	}
	if snap.Execution != nil {
		t.Fatalf("a capsule without runtime-state/ reported an execution: %+v", snap.Execution)
	}

	code, body := udsGet(t, td.sock, "/jobs/"+wi+"/artifact?path="+url.QueryEscape("plan.md"))
	if code != http.StatusOK {
		t.Fatalf("artifact of a slimmed capsule: %d %s", code, body)
	}
	var ans api.ArtifactAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if !ans.Pointer || ans.Status != api.StatusWrapped || ans.Slug != slug || ans.Path != "plan.md" {
		t.Fatalf("artifact answer: %+v, want the wrapped work item's pointer", ans)
	}

	item, err := td.hub.GetWorkItem(wi)
	if err != nil {
		t.Fatal(err)
	}
	if got := td.Daemon.capsuleDirOf(wi, item); got != dir {
		t.Fatalf("capsule dir = %q, want %q", got, dir)
	}
	target, err := safeArtifactPath(dir, "plan.md")
	if err != nil {
		t.Fatalf("artifact path in the slimmed capsule: %v", err)
	}
	if content, err := os.ReadFile(target); err != nil || string(content) != "# plan\n" {
		t.Fatalf("artifact content = %q (%v), want the file the slim kept", content, err)
	}
}
