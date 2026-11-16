// carries: internal/aihub/core/owner.go, internal/aihub/core/queue.go, internal/aihub/core/session.go, internal/aihub/core/workitem.go

package core

// parent_waiting_test.go — polyforge#77: a bundle-bound parent waits for
// its children. The "unfinished blocker" judgment is one relation —
// `blocks` edges ∪ the children of a bundle-bound parent (not wrapped) —
// read by the claim gate, the ready queue and waiting_for alike.

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// pwChild files an execution child under parent.
func pwChild(t *testing.T, env *testEnv, parent, goal string) string {
	t.Helper()
	wi, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: goal, Reporter: env.UserID,
		ParentID: strPtrTest(parent), BundleName: strPtrTest("demo-bundle"),
	})
	if err != nil {
		t.Fatalf("create child %q: %v", goal, err)
	}
	return wi.ID
}

// pwSlug is the display slug of a work item (waiting_for names blockers by it).
func pwSlug(t *testing.T, env *testEnv, id string) string {
	t.Helper()
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return wi.Slug
}

// pwWaiting is the waiting_for list of id as its JSON face: one map per
// entry (slug, status and — when present — via).
func pwWaiting(t *testing.T, env *testEnv, id string) []map[string]string {
	t.Helper()
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	raw, err := json.Marshal(wi.WaitingFor)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("waiting_for shape: %v (%s)", err, raw)
	}
	return out
}

// pwInItems reports whether id is in the executable segment of alpha's ready queue.
func pwInItems(t *testing.T, env *testEnv, id string) bool {
	t.Helper()
	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("ready queue: %v", err)
	}
	for _, wi := range q.Items {
		if wi.ID == id {
			return true
		}
	}
	return false
}

// pwEnd claims id and ends its session with status.
func pwEnd(t *testing.T, m *SessionManager, env *testEnv, id, secret, status string) {
	t.Helper()
	mustSessionClaim(t, m, env, id, secret)
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: status}, env.UserID); err != nil {
		t.Fatalf("end %s %s: %v", id, status, err)
	}
}

// pwWantWaiting asserts the exact waiting_for list: slug → {status, via}
// (via "" means the key is absent — a `blocks` edge entry).
func pwWantWaiting(t *testing.T, label string, got []map[string]string, want map[string][2]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: waiting_for = %v, want %d entries %v", label, got, len(want), want)
	}
	for _, e := range got {
		w, ok := want[e["slug"]]
		if !ok {
			t.Fatalf("%s: waiting_for names %q, want only %v", label, e["slug"], want)
		}
		if e["status"] != w[0] {
			t.Fatalf("%s: %s status = %q, want %q", label, e["slug"], e["status"], w[0])
		}
		via, has := e["via"]
		if w[1] == "" && has {
			t.Fatalf("%s: %s carries via %q, want no via (a blocks edge)", label, e["slug"], via)
		}
		if via != w[1] {
			t.Fatalf("%s: %s via = %q, want %q", label, e["slug"], via, w[1])
		}
	}
}

// TestBoundParentWaitsForChildren (polyforge#77, AC2): a bundle-bound
// parent is not claimable (DEPENDENCY_BLOCKED) and stays out of the ready
// queue while any child is not wrapped — a failed or canceled child keeps
// it waiting too; waiting_for lists the children with via "child"; a
// child filed later joins the wait; dependencies between children are
// ordinary `blocks` edges; an unbound parent keeps its old faces.
func TestBoundParentWaitsForChildren(t *testing.T) {
	env, m := sessionEnv(t)
	blocked := func(label, id, secret string) {
		t.Helper()
		if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: secret}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "DEPENDENCY_BLOCKED" {
			t.Fatalf("%s: claim want CORE-409/DEPENDENCY_BLOCKED, got %v", label, err)
		}
		if pwInItems(t, env, id) {
			t.Fatalf("%s: in the ready queue's executable segment", label)
		}
	}

	p := mustCreateExecWI(t, env, "bound parent", nil, "")
	c1 := pwChild(t, env, p, "child one")
	s1 := pwSlug(t, env, c1)
	blocked("parent with one queued child", p, "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1")
	pwWantWaiting(t, "one queued child", pwWaiting(t, env, p), map[string][2]string{
		s1: {api.StatusQueued, "child"},
	})
	if !pwInItems(t, env, c1) {
		t.Fatalf("the child itself is not executable")
	}

	// A child filed later joins the wait.
	c2 := pwChild(t, env, p, "child two")
	s2 := pwSlug(t, env, c2)
	pwWantWaiting(t, "a later child", pwWaiting(t, env, p), map[string][2]string{
		s1: {api.StatusQueued, "child"},
		s2: {api.StatusQueued, "child"},
	})

	// Dependencies between children are ordinary `blocks` edges.
	if err := env.WIs.AddDependency(c1, c2, "blocks"); err != nil {
		t.Fatalf("child-to-child edge: %v", err)
	}
	pwWantWaiting(t, "child behind a sibling", pwWaiting(t, env, c2), map[string][2]string{
		s1: {api.StatusQueued, ""},
	})
	blocked("child behind a sibling", c2, "a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2")
	if w := pwWaiting(t, env, c1); len(w) != 0 {
		t.Fatalf("a child waits on %v; children never wait on their parent", w)
	}

	// c1 wraps: c2 is released, the parent still waits on c2.
	pwEnd(t, m, env, c1, "a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3", api.StatusWrapped)
	pwWantWaiting(t, "one child wrapped", pwWaiting(t, env, p), map[string][2]string{
		s2: {api.StatusQueued, "child"},
	})
	blocked("parent with one child left", p, "a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4")

	// A failed child keeps the parent waiting (only wrapped releases).
	pwEnd(t, m, env, c2, "a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5", api.StatusFailed)
	pwWantWaiting(t, "failed child", pwWaiting(t, env, p), map[string][2]string{
		s2: {api.StatusFailed, "child"},
	})
	blocked("parent behind a failed child", p, "a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6a6")

	// ... and so does a canceled one.
	c3 := pwChild(t, env, p, "child three")
	if _, err := env.WIs.Cancel(c3, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatalf("cancel child: %v", err)
	}
	pwWantWaiting(t, "failed and canceled children", pwWaiting(t, env, p), map[string][2]string{
		s2:                 {api.StatusFailed, "child"},
		pwSlug(t, env, c3): {api.StatusCanceled, "child"},
	})
	blocked("parent behind a canceled child", p, "a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7a7")

	// Every child wrapped: the bound parent is released.
	q := mustCreateExecWI(t, env, "bound parent released", nil, "")
	d1 := pwChild(t, env, q, "released child")
	blocked("parent before its child wraps", q, "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1")
	pwEnd(t, m, env, d1, "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", api.StatusWrapped)
	if w := pwWaiting(t, env, q); len(w) != 0 {
		t.Fatalf("all children wrapped: waiting_for = %v, want none", w)
	}
	if !pwInItems(t, env, q) {
		t.Fatalf("all children wrapped: the parent is not in the ready queue")
	}
	mustSessionClaim(t, m, env, q, "b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3")

	// An unbound parent keeps its faces: NOT_CLAIMABLE, no waiting_for.
	r := mustCreateWI(t, env, "unbound parent", nil, "")
	pwChild(t, env, r, "child of the unbound parent")
	if _, err := m.Claim(r, api.SessionClaimRequest{SessionID: "c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_CLAIMABLE" {
		t.Fatalf("unbound parent claim: want CORE-409/NOT_CLAIMABLE, got %v", err)
	}
	if w := pwWaiting(t, env, r); len(w) != 0 {
		t.Fatalf("unbound parent waiting_for = %v, want none", w)
	}
}

// TestChildParentDependencyCycle (polyforge#77, AC3): cycle detection
// walks the child → bound-parent wait too. Refused CORE-409
// DEPENDENCY_CYCLE, nothing written: the parent ordered before its own
// child (AddDependency and create --after), a child filed after a work
// item its parent (via other edges) blocks, and an edge closing the loop
// through the implicit child → parent wait. Legal edges still land.
func TestChildParentDependencyCycle(t *testing.T) {
	env := newEnv(t)
	cycle := func(label string, err error) {
		t.Helper()
		if !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "DEPENDENCY_CYCLE" {
			t.Fatalf("%s: want CORE-409/DEPENDENCY_CYCLE, got %v", label, err)
		}
	}
	count := func() int {
		t.Helper()
		all, err := env.WIs.List(ListFilters{Project: "alpha"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		return len(all)
	}
	blockedBy := func(id string) int {
		t.Helper()
		deps, err := env.WIs.Dependencies(id)
		if err != nil {
			t.Fatalf("deps: %v", err)
		}
		return len(deps.BlockedBy)
	}

	p := mustCreateExecWI(t, env, "bound parent", nil, "")
	c := pwChild(t, env, p, "child")

	// The parent ordered before its own child.
	cycle("parent blocks its child", env.WIs.AddDependency(p, c, "blocks"))
	if n := blockedBy(c); n != 0 {
		t.Fatalf("refused edge landed: child blocked by %d", n)
	}
	before := count()
	_, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "child after its own parent", Reporter: env.UserID,
		ParentID: strPtrTest(p), BundleName: strPtrTest("demo-bundle"), After: []string{p},
	})
	cycle("create child --after its parent", err)

	// The parent blocks x (a legal edge: x is not its child) ...
	x := mustCreateExecWI(t, env, "after the parent", nil, "")
	if err := env.WIs.AddDependency(p, x, "blocks"); err != nil {
		t.Fatalf("parent blocks an unrelated ticket: %v", err)
	}
	// ... so a child filed --after x would wait on x, which waits on the parent.
	_, err = env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "child after the parent's dependent", Reporter: env.UserID,
		ParentID: strPtrTest(p), BundleName: strPtrTest("demo-bundle"), After: []string{x},
	})
	cycle("create child --after a ticket its parent blocks", err)
	if n := count(); n != before+1 {
		t.Fatalf("refused creates persisted rows: %d rows, want %d", n, before+1)
	}
	// ... and an existing child ordered after x closes the same loop.
	cycle("child after the parent's dependent", env.WIs.AddDependency(x, c, "blocks"))
	// One more hop: y after x, the child after y.
	y := mustCreateExecWI(t, env, "after x", nil, "")
	if err := env.WIs.AddDependency(x, y, "blocks"); err != nil {
		t.Fatalf("x blocks y: %v", err)
	}
	cycle("child two hops behind its parent", env.WIs.AddDependency(y, c, "blocks"))
	if n := blockedBy(c); n != 0 {
		t.Fatalf("refused edges landed: child blocked by %d", n)
	}

	// Legal edges still land: child-to-child, a child after an unrelated
	// ticket, the parent's own create-time blocker.
	c2 := pwChild(t, env, p, "second child")
	if err := env.WIs.AddDependency(c, c2, "blocks"); err != nil {
		t.Fatalf("child-to-child edge: %v", err)
	}
	z := mustCreateExecWI(t, env, "unrelated", nil, "")
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "child after an unrelated ticket", Reporter: env.UserID,
		ParentID: strPtrTest(p), BundleName: strPtrTest("demo-bundle"), After: []string{z},
	}); err != nil {
		t.Fatalf("child --after an unrelated ticket: %v", err)
	}
	if err := env.WIs.AddDependency(z, p, "blocks"); err != nil {
		t.Fatalf("an unrelated ticket blocks the parent: %v", err)
	}
}

// pwPatchParent PATCHes parent_id with the raw JSON value.
func pwPatchParent(env *testEnv, id, raw string) (*WIView, error) {
	return env.WIs.Patch(id, map[string]json.RawMessage{"parent_id": json.RawMessage(raw)}, env.UserID)
}

// pwParent reads the stored parent_id ("" for none).
func pwParent(t *testing.T, env *testEnv, id string) string {
	t.Helper()
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if wi.ParentID == nil {
		return ""
	}
	return *wi.ParentID
}

// TestDetachChildFromParent (polyforge#77, AC5): PATCH parent_id may only
// clear it (null or ""); re-parenting is refused; a running child is
// refused; a detach records one wi_detached event naming the old parent;
// once detached the parent no longer waits for it. A terminal (failed)
// child can be detached — the way out of a parent held by a failed child.
func TestDetachChildFromParent(t *testing.T) {
	env, m := sessionEnv(t)
	p := mustCreateExecWI(t, env, "bound parent", nil, "")
	c1 := pwChild(t, env, p, "child that fails")
	c2 := pwChild(t, env, p, "child detached while queued")
	other := mustCreateWI(t, env, "another parent", nil, "")

	// Re-parenting is refused, both to another ticket and to the same one.
	for _, target := range []string{other, p} {
		if _, err := pwPatchParent(env, c1, `"`+target+`"`); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "IMMUTABLE" {
			t.Fatalf("re-parent to %s: want CORE-400/IMMUTABLE, got %v", target, err)
		}
	}
	if got := pwParent(t, env, c1); got != p {
		t.Fatalf("refused re-parent wrote: parent_id = %q, want %q", got, p)
	}

	// A running child is refused, nothing written.
	secret := "d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1"
	mustSessionClaim(t, m, env, c1, secret)
	if _, err := pwPatchParent(env, c1, `null`); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "CHILD_RUNNING" {
		t.Fatalf("detach a running child: want CORE-409/CHILD_RUNNING, got %v", err)
	}
	if got := pwParent(t, env, c1); got != p {
		t.Fatalf("refused detach wrote: parent_id = %q, want %q", got, p)
	}
	if n := countEvents(t, env, c1, "wi_detached"); n != 0 {
		t.Fatalf("refused detach recorded %d wi_detached events", n)
	}

	// The child fails: it holds the parent until detached.
	if err := m.End(c1, api.SessionEndRequest{SessionID: secret, Status: api.StatusFailed}, env.UserID); err != nil {
		t.Fatalf("end failed: %v", err)
	}
	pwWantWaiting(t, "failed child attached", pwWaiting(t, env, p), map[string][2]string{
		pwSlug(t, env, c1): {api.StatusFailed, "child"},
		pwSlug(t, env, c2): {api.StatusQueued, "child"},
	})
	got, err := pwPatchParent(env, c1, `null`)
	if err != nil {
		t.Fatalf("detach the failed child: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("detach answer parent_id = %v, want null", *got.ParentID)
	}
	if n := countEvents(t, env, c1, "wi_detached"); n != 1 {
		t.Fatalf("wi_detached events = %d, want 1", n)
	}
	events, err := env.WIs.Events(c1)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type != "wi_detached" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload["parent_id"] != p {
			t.Fatalf("wi_detached payload = %s, want parent_id %s", e.Payload, p)
		}
	}
	pwWantWaiting(t, "failed child detached", pwWaiting(t, env, p), map[string][2]string{
		pwSlug(t, env, c2): {api.StatusQueued, "child"},
	})

	// The empty string clears too; the parent is released.
	if _, err := pwPatchParent(env, c2, `""`); err != nil {
		t.Fatalf("detach with the empty string: %v", err)
	}
	if got := pwParent(t, env, c2); got != "" {
		t.Fatalf("detached child parent_id = %q, want none", got)
	}
	if n := countEvents(t, env, c2, "wi_detached"); n != 1 {
		t.Fatalf("wi_detached events = %d, want 1", n)
	}
	if w := pwWaiting(t, env, p); len(w) != 0 {
		t.Fatalf("every child detached: waiting_for = %v, want none", w)
	}
	if !pwInItems(t, env, p) {
		t.Fatalf("every child detached: the parent is not in the ready queue")
	}
	mustSessionClaim(t, m, env, p, "d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2d2")
}

// TestParentBundleFrozenAfterChildClaim (polyforge#77, AC6): once any
// child of a bundle-bound parent has been claimed, the parent's bundle
// can no longer change — rename and unbind are refused CORE-400
// PARENT_BUNDLE_FROZEN and nothing is written, even after that child
// wrapped. Before any child claim the parent's own R2 window still
// allows the change, and an unbound parent (registration ticket) may
// still bind a bundle after its child was claimed.
func TestParentBundleFrozenAfterChildClaim(t *testing.T) {
	env, m := sessionEnv(t)
	frozen := func(label string, err error) {
		t.Helper()
		if !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "PARENT_BUNDLE_FROZEN" {
			t.Fatalf("%s: want CORE-400/PARENT_BUNDLE_FROZEN, got %v", label, err)
		}
	}
	bundleOf := func(id string) string {
		t.Helper()
		wi, err := env.WIs.Get(id, GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if wi.BundleName == nil {
			return ""
		}
		return *wi.BundleName
	}

	p := mustCreateExecWI(t, env, "bound parent", nil, "")
	c1 := pwChild(t, env, p, "child one")
	pwChild(t, env, p, "child two")

	// No child claimed yet: rename and rename back pass.
	if _, err := patchBundle(t, env, p, strPtrTest("lane-b")); err != nil {
		t.Fatalf("rename before any child claim: %v", err)
	}
	if _, err := patchBundle(t, env, p, strPtrTest("demo-bundle")); err != nil {
		t.Fatalf("rename back before any child claim: %v", err)
	}

	// One child claimed: the parent's bundle is frozen.
	secret := "f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1"
	mustSessionClaim(t, m, env, c1, secret)
	_, err := patchBundle(t, env, p, strPtrTest("lane-b"))
	frozen("rename after a child claim", err)
	_, err = patchBundle(t, env, p, nil)
	frozen("unbind after a child claim", err)
	if got := bundleOf(p); got != "demo-bundle" {
		t.Fatalf("refused PATCH wrote: bundle_name = %q, want demo-bundle", got)
	}

	// The freeze outlives the claim: the child wrapped, still refused.
	if err := m.End(c1, api.SessionEndRequest{SessionID: secret, Status: api.StatusWrapped}, env.UserID); err != nil {
		t.Fatalf("end: %v", err)
	}
	_, err = patchBundle(t, env, p, strPtrTest("lane-b"))
	frozen("rename after the claimed child wrapped", err)

	// An unbound parent is not under the new check: it binds after its
	// child was claimed.
	r := mustCreateWI(t, env, "unbound parent", nil, "")
	rc := pwChild(t, env, r, "child of the unbound parent")
	mustSessionClaim(t, m, env, rc, "f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2f2")
	if _, err := patchBundle(t, env, r, strPtrTest("demo-bundle")); err != nil {
		t.Fatalf("bind an unbound parent after its child was claimed: %v", err)
	}
	if got := bundleOf(r); got != "demo-bundle" {
		t.Fatalf("bind did not land: bundle_name = %q", got)
	}
}
