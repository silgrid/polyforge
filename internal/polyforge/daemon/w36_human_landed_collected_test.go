package daemon

// w36_human_landed_collected_test.go (u9): a landed record whose
// merge_commits are the #36 objects (each landed commit with its merge
// flag — a person's squash or rebase landing, polyforge#36) is in the
// integrate lane's pool beside a pre-#36 string-shaped one: collect's
// pool read, through the step's hub on the real aihub fixture, takes
// every landed record whatever shape named its landed commits (the
// batch's take_batch — the step that drops a record naming no merge
// commit — is the stepscripts package's own test, u7).

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"sort"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// TestW36HumanLandedRecordIsCollected: the landing record of a human
// squash landing (one plain commit, merge false) and of a rebase landing
// (replayed commits) sit in integrate.py's pool read exactly as the
// lane's own merge-commit record does.
func TestW36HumanLandedRecordIsCollected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	prOf := func(r string) string { return "https://forge.invalid/demo/" + r + "/pull/7" }
	item := func(id, slug string, integ any) map[string]any {
		return map[string]any{"id": id, "slug": slug, "seq": 1, "project": "proj", "status": "wrapped", "priority": "normal",
			"bundle_name": "develop", "updated_at": "2026-11-12T08:00:00Z", "attrs": map[string]any{"integration": integ}}
	}
	squashLanded := map[string]any{
		"state": "landed",
		"repos": []any{map[string]any{"repo": "alpha", "pr": prOf("alpha"), "head": "h-s", "light_verified_head": "h-s", "merge_commit": "sq-1"}},
		// the person's Squash and merge: one plain commit
		"merge_commits": map[string]any{"alpha": []any{map[string]any{"sha": "sq-1", "merge": false}}},
	}
	rebaseLanded := map[string]any{
		"state": "landed",
		"repos": []any{map[string]any{"repo": "alpha", "pr": prOf("alpha"), "head": "h-r", "light_verified_head": "h-r", "merge_commit": "rb-2"}},
		// the person's Rebase and merge: the replayed commits
		"merge_commits": map[string]any{"alpha": []any{map[string]any{"sha": "rb-1", "merge": false}, map[string]any{"sha": "rb-2", "merge": false}}},
	}
	laneLanded := map[string]any{"state": "landed", "repos": []any{map[string]any{"repo": "alpha", "pr": prOf("alpha"), "head": "h-l", "merge_commit": "m-l"}},
		"merge_commits": map[string]any{"alpha": []any{"m-l"}}}
	_, srv := newC4Aihub(t, []map[string]any{
		item("wi_squash", "proj#36", squashLanded),
		item("wi_rebase", "proj#37", rebaseLanded),
		item("wi_lane", "proj#38", laneLanded),
		{"id": "wi_batch", "slug": "proj#56", "seq": 6, "project": "proj", "status": "running", "bundle_name": "integrate", "attrs": map[string]any{}},
	})
	f := c4BatchFixture(t, srv)
	f.as("collect", 1)
	f.td.stepHubAllow = func(stepJob) []string {
		return []string{stepcall.HubGet, stepcall.HubList, stepcall.HubBatch, stepcall.HubFile, stepcall.HubAttrs}
	}
	inStep(t, f)
	var pool struct {
		Items []struct {
			ID          string         `json:"id"`
			Integration map[string]any `json:"integration"`
		} `json:"items"`
	}
	raw := scriptPool(t, "integrate")
	if err := json.Unmarshal(raw, &pool); err != nil {
		t.Fatalf("the pool read: %v (%s)", err, raw)
	}
	var ids []string
	for _, it := range pool.Items {
		ids = append(ids, it.ID)
		if it.Integration["state"] != "landed" {
			t.Fatalf("collect's pool took a %v record: %v", it.Integration["state"], it)
		}
	}
	sort.Strings(ids)
	if !reflect.DeepEqual(ids, []string{"wi_lane", "wi_rebase", "wi_squash"}) {
		t.Fatalf("SYMPTOM: collect's pool is every landed record, the object shapes no less than the strings: %v", ids)
	}
}
