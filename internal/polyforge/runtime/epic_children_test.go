package runtime

// epic_children_test.go (polyforge#117 u4, AC1/Q5): the claim's children
// snapshot — an epic's first claim writes runtime-state/children.json
// (version 1, every child's id, slug, status and its landing record
// verbatim), a re-claim over unchanged children keeps the file byte for
// byte (a crash restart never refreshes a reviewed baseline), and a
// changed set writes a NEW VERSION beside a resume marker that orders
// the driver to re-run the steps from review onward.
//
// Expected values are the owner's settled Q5 and the plan's own snapshot
// shape, not the implementation's: version 1 then 2, the children sorted
// by seq, the landing record the hub carries verbatim.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// epicSnapChildren is the snapshot's children list as the test reads it.
type epicSnapChildren struct {
	Version  int `json:"version"`
	Children []struct {
		ID      string          `json:"id"`
		Slug    string          `json:"slug"`
		Status  string          `json:"status"`
		Landing json.RawMessage `json:"landing"`
	} `json:"children"`
}

// epicReadSnapshot reads and parses the capsule's children snapshot.
func epicReadSnapshot(t *testing.T, capDir string) (epicSnapChildren, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, capsule.DirRuntimeState, "children.json"))
	if err != nil {
		t.Fatalf("children.json: %v", err)
	}
	var snap epicSnapChildren
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("children.json does not parse: %v (%q)", err, raw)
	}
	return snap, raw
}

// TestEpicClaimSnapshot: the first claim snapshots the epic's children
// with their landing records; an unchanged re-claim keeps the file; a
// changed re-claim writes the next version and the resume marker.
func TestEpicClaimSnapshot(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	epicLanes(t, r, "epic: true\nchildren_branch: \"epic/{seq}\"\n")
	const epic = "wi_epic3"
	landing := map[string]any{"state": "landed_on_target", "target": "epic/3",
		"repos": []any{map[string]any{"repo": "app", "pr": "https://example/pr/11"}}}
	hub.items = map[string]*api.WorkItem{
		epic: {ID: epic, ProjectName: "proj", Slug: "polyforge#3", Seq: 3, BundleName: w80StrPtr("epic-lane")},
		"wi_c1": {ID: "wi_c1", ProjectName: "proj", ParentID: w80StrPtr(epic), Slug: "polyforge#1", Seq: 1,
			Status: api.StatusWrapped, Attrs: map[string]any{"integration": landing}},
		"wi_c2": {ID: "wi_c2", ProjectName: "proj", ParentID: w80StrPtr(epic), Slug: "polyforge#2", Seq: 2,
			Status: api.StatusWrapped},
	}
	capDir := epicClaim(t, r, epic, hub.items[epic], nil)
	snap, raw1 := epicReadSnapshot(t, capDir)
	if snap.Version != 1 || len(snap.Children) != 2 {
		t.Fatalf("the first claim writes version 1 with both children: %+v", snap)
	}
	if snap.Children[0].ID != "wi_c1" || snap.Children[0].Slug != "polyforge#1" || snap.Children[0].Status != api.StatusWrapped {
		t.Fatalf("the children sort by seq, the first is c1: %+v", snap.Children[0])
	}
	var landingBack map[string]any
	if err := json.Unmarshal(snap.Children[0].Landing, &landingBack); err != nil {
		t.Fatalf("the landing record is the hub's verbatim: %v (%s)", err, snap.Children[0].Landing)
	}
	if landingBack["state"] != "landed_on_target" || landingBack["target"] != "epic/3" {
		t.Fatalf("the landing record is the hub's verbatim: %v", landingBack)
	}
	// the resume marker is not written on the first claim: nothing was
	// reviewed yet, so nothing re-runs
	if _, err := os.Stat(filepath.Join(capDir, capsule.DirRuntimeState, "epic-resume.json")); !os.IsNotExist(err) {
		t.Fatalf("the first claim orders no resume: %v", err)
	}

	// an unchanged re-claim keeps the file byte for byte (a crash restart
	// never refreshes the reviewed baseline)
	if err := os.RemoveAll(filepath.Join(capDir, capsule.DirRepos)); err != nil {
		t.Fatal(err)
	}
	epicClaim(t, r, epic, hub.items[epic], nil)
	if _, raw2 := epicReadSnapshot(t, capDir); string(raw2) != string(raw1) {
		t.Fatalf("an unchanged re-claim keeps the snapshot byte for byte:\n%q\n%q", raw1, raw2)
	}

	// a changed re-claim: a new child lands, the snapshot moves to the
	// next version and the resume marker orders the driver to re-run
	// from review
	hub.items["wi_c3"] = &api.WorkItem{ID: "wi_c3", ProjectName: "proj", ParentID: w80StrPtr(epic), Slug: "polyforge#4", Seq: 4,
		Status: api.StatusWrapped}
	if err := os.RemoveAll(filepath.Join(capDir, capsule.DirRepos)); err != nil {
		t.Fatal(err)
	}
	epicClaim(t, r, epic, hub.items[epic], nil)
	snap, _ = epicReadSnapshot(t, capDir)
	if snap.Version != 2 || len(snap.Children) != 3 {
		t.Fatalf("a changed re-claim writes the next version with every child: %+v", snap)
	}
	rawMarker, err := os.ReadFile(filepath.Join(capDir, capsule.DirRuntimeState, "epic-resume.json"))
	if err != nil {
		t.Fatalf("a changed re-claim writes the resume marker: %v", err)
	}
	var marker struct {
		ResetFrom       string `json:"reset_from"`
		SnapshotVersion int    `json:"snapshot_version"`
	}
	if err := json.Unmarshal(rawMarker, &marker); err != nil || marker.ResetFrom != "review" || marker.SnapshotVersion != 2 {
		t.Fatalf("the resume marker orders the re-run from review at the snapshot's version: %q", rawMarker)
	}

	// a non-epic item's claim writes no snapshot at all (its own lane
	// declares no epic)
	hub.items["wi_plain"] = &api.WorkItem{ID: "wi_plain", ProjectName: "proj", Slug: "polyforge#9", Seq: 9, BundleName: w80StrPtr("plain-lane")}
	plainDir := epicClaimPlain(t, r, "wi_plain", hub.items["wi_plain"])
	if _, err := os.Stat(filepath.Join(plainDir, capsule.DirRuntimeState, "children.json")); !os.IsNotExist(err) {
		t.Fatalf("a non-epic claim writes no snapshot: %v", err)
	}
}

// epicClaimPlain materializes an item on the plain lane.
func epicClaimPlain(t *testing.T, r *Runtime, wi string, item *api.WorkItem) string {
	t.Helper()
	j := &job{
		wi:         wi,
		project:    "proj",
		bundleName: "plain-lane",
		capsuleDir: testCapsuleDir(r, wi),
		item:       item,
	}
	if err := r.materialize(context.Background(), j); err != nil {
		t.Fatalf("materialize %s: %v", wi, err)
	}
	return j.capsuleDir
}
