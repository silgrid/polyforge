package core

// queue_test.go — the ready queue's segment membership: only bound,
// queued, unblocked, non-waiting tickets execute; registration tickets
// and epics appear in no segment.

import (
	"encoding/json"
	"testing"
)

// queueIDs is the id list of one wire-level queue segment.
func queueIDs(t *testing.T, q *ReadyQueue, key string) []string {
	t.Helper()
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal queue: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal queue: %v", err)
	}
	rawSeg, ok := doc[key]
	if !ok {
		t.Fatalf("queue has no segment %q (wire keys: %v)", key, doc)
	}
	var entries []map[string]any
	if err := json.Unmarshal(rawSeg, &entries); err != nil {
		t.Fatalf("segment %q is not an array of objects: %v", key, err)
	}
	var ids []string
	for _, e := range entries {
		id, _ := e["id"].(string)
		ids = append(ids, id)
		// every entry carries the WI fields the CLI renders.
		for _, k := range []string{"id", "slug", "status", "goal", "priority", "bundle_name"} {
			if _, ok := e[k]; !ok {
				t.Fatalf("segment %q entry lacks the field %q: %v", key, k, e)
			}
		}
	}
	return ids
}

func hasQueuedID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestReadyQueueSegments: the executable segment holds exactly the
// bound, queued, unblocked, non-waiting tickets; registration tickets and
// epics appear in no segment; the wire top level is exactly
// project/items/running/paused.
func TestReadyQueueSegments(t *testing.T) {
	env := newEnv(t)
	mk := func(goal string, bundle, parent *string) string {
		wi, err := env.WIs.Create(CreateRequest{
			Project: "alpha", Goal: goal, Reporter: env.UserID,
			BundleName: bundle, ParentID: parent,
		})
		if err != nil {
			t.Fatalf("create %s: %v", goal, err)
		}
		return wi.ID
	}

	x := mk("executable bound", strPtrTest("q-x"), nil)    // bound → items
	reg := mk("registration ticket", nil, nil)             // no bundle → no segment
	w := mk("waiting excluded", strPtrTest("q-w"), nil)    // waiting_on → excluded
	bq := mk("blocker", strPtrTest("q-bq"), nil)           // bound blocker → items
	d := mk("dependency excluded", strPtrTest("q-d"), nil) // blocked → excluded
	e := mk("epic parent", nil, nil)                       // epic → no segment
	ec := mk("epic bound child", strPtrTest("q-ec"), strPtrTest(e))
	v := mk("later unbound", strPtrTest("q-v"), nil) // bound now, unbound below

	if _, err := env.WIs.Patch(w, map[string]json.RawMessage{"waiting_on": json.RawMessage(`"owner"`)}, env.UserID); err != nil {
		t.Fatalf("patch waiting_on: %v", err)
	}
	if err := env.WIs.AddDependency(bq, d, "blocks"); err != nil {
		t.Fatalf("add dependency: %v", err)
	}

	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("ready queue: %v", err)
	}

	raw, _ := json.Marshal(q)
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal queue: %v", err)
	}
	for _, k := range []string{"project", "items", "running", "paused"} {
		if _, ok := wire[k]; !ok {
			t.Fatalf("queue lost the top-level key %q", k)
		}
	}
	if len(wire) != 4 {
		t.Fatalf("queue top level = %d keys, want exactly project/items/running/paused", len(wire))
	}

	items := queueIDs(t, q, "items")
	for _, id := range []string{x, bq, ec, v} {
		if !hasQueuedID(items, id) {
			t.Fatalf("executable segment lost %s", id)
		}
	}
	for _, id := range []string{reg, w, d, e} {
		if hasQueuedID(items, id) {
			t.Fatalf("executable segment gained %s (must stay excluded)", id)
		}
	}
	for _, seg := range []string{"running", "paused"} {
		for _, id := range []string{reg, e} {
			if hasQueuedID(queueIDs(t, q, seg), id) {
				t.Fatalf("%s appears in segment %q, want no segment", id, seg)
			}
		}
	}

	// unbind: X→NULL removes the ticket from the executable segment.
	if _, err := env.WIs.Patch(v, map[string]json.RawMessage{"bundle_name": json.RawMessage("null")}, env.UserID); err != nil {
		t.Fatalf("unbind %s in R2 window: %v", v, err)
	}
	q2, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("ready queue: %v", err)
	}
	if hasQueuedID(queueIDs(t, q2, "items"), v) {
		t.Fatalf("unbound ticket %s still in the executable segment", v)
	}
}

// TestReadyQueueWaitingOnRetainedValueExclusion (polyforge#149): a
// queued bound ticket holding EITHER retained waiting_on value ('owner'
// or 'dependency:<slug>') is excluded from the executable `items`
// segment; clearing waiting_on re-includes it.
func TestReadyQueueWaitingOnRetainedValueExclusion(t *testing.T) {
	env := newEnv(t)
	mk := func(goal string, bundle *string) string {
		wi, err := env.WIs.Create(CreateRequest{
			Project: "alpha", Goal: goal, Reporter: env.UserID,
			BundleName: bundle,
		})
		if err != nil {
			t.Fatalf("create %s: %v", goal, err)
		}
		return wi.ID
	}
	wo := mk("waiting owner ticket", strPtrTest("q-wo"))      // waiting_on=owner
	wd := mk("waiting dependency ticket", strPtrTest("q-wd")) // waiting_on=dependency:slug

	patch := func(id, v string) {
		t.Helper()
		if _, err := env.WIs.Patch(id, map[string]json.RawMessage{
			"waiting_on": json.RawMessage(`"` + v + `"`),
		}, env.UserID); err != nil {
			t.Fatalf("patch waiting_on=%q on %s: %v", v, id, err)
		}
	}
	patch(wo, "owner")
	patch(wd, "dependency:demo-bundle")

	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("ready queue: %v", err)
	}
	for _, id := range []string{wo, wd} {
		if hasQueuedID(queueIDs(t, q, "items"), id) {
			t.Fatalf("waiting ticket %s appears in the executable segment", id)
		}
	}

	if _, err := env.WIs.Patch(wo, map[string]json.RawMessage{"waiting_on": json.RawMessage("null")}, env.UserID); err != nil {
		t.Fatalf("clear waiting_on on %s: %v", wo, err)
	}
	q2, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("ready queue 2: %v", err)
	}
	if !hasQueuedID(queueIDs(t, q2, "items"), wo) {
		t.Fatalf("cleared ticket %s not re-included in items", wo)
	}
	if hasQueuedID(queueIDs(t, q2, "items"), wd) {
		t.Fatalf("still-waiting ticket %s leaked into items", wd)
	}
}

// TestReadyQueueEmptyBundleIsRegistration: a legacy row carrying
// an empty-string bundle_name is a registration ticket like NULL — the claim gate
// refuses it NOT_CLAIMABLE, so the executable segment must not list it
// (else every auto-pull wave offers an unclaimable head).
func TestReadyQueueEmptyBundleIsRegistration(t *testing.T) {
	env := newEnv(t)
	id := mustCreateExecWI(t, env, "legacy empty bundle", nil, "")
	if _, err := env.DB.Exec(`UPDATE work_items SET bundle_name='' WHERE id=?1`, id); err != nil {
		t.Fatal(err)
	}
	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range q.Items {
		if it.ID == id {
			t.Fatalf("bundle_name='' ticket listed as executable")
		}
	}
}
