// carries: internal/aihub/core/owner.go, internal/aihub/core/queue.go, internal/aihub/core/session.go

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
