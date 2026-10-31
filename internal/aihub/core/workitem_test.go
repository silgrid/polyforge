package core

// workitem_test.go — the w140 (polyforge#140 split a) unit tests for the
// bundle-reference write face: the shared create/PATCH persist path
// (plan step shared_bundle_path_r2_window) and the R2 window gate.
// Every refusal is typed; every acceptance re-derives the digest through
// bundleRef — the ONE persist path both entries share.

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// mustCreateBound creates a queued execution ticket with the given
// bundle (no explicit digest → stand-in) and returns its id.
func mustCreateBound(t *testing.T, env *testEnv, goal, bundle string) string {
	t.Helper()
	wi, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: goal, Reporter: env.UserID,
		BundleName: strPtrTest(bundle),
	})
	if err != nil {
		t.Fatalf("create bound wi (%s): %v", goal, err)
	}
	return wi.ID
}

// mustClaim claims a WI with a fresh session (core face).
func mustClaim(t *testing.T, env *testEnv, wiID string) string {
	t.Helper()
	secret := "w140-unit-session-secret"
	m := NewSessionManager(env.DB)
	if _, err := m.Claim(wiID, api.SessionClaimRequest{SessionID: secret}, env.UserID); err != nil {
		t.Fatalf("claim %s: %v", wiID, err)
	}
	return secret
}

// patchBundle is the typed PATCH helper for the bundle name field.
func patchBundle(t *testing.T, env *testEnv, id string, name *string) (*WIView, error) {
	t.Helper()
	fields := map[string]json.RawMessage{}
	if name != nil {
		raw, _ := json.Marshal(*name)
		fields["bundle_name"] = raw
	} else {
		fields["bundle_name"] = json.RawMessage("null")
	}
	return env.WIs.Patch(id, fields, env.UserID)
}

// TestPatchBundleR2ThreeWay walks NULL→X / X→Y / X→NULL inside the R2
// window: the name follows the shared persist path and the unbind
// clears the reference. The digest chain is deleted (polyforge#149): a
// PATCH body
// carrying bundle_digest is refused as an unpatchable field.
func TestPatchBundleR2ThreeWay(t *testing.T) {
	env := newEnv(t)

	// NULL→X: the bind.
	id := mustCreateWI(t, env, "w140 three-way target", nil, "")
	got, err := patchBundle(t, env, id, strPtrTest("w140-x"))
	if err != nil {
		t.Fatalf("NULL→X refused inside the R2 window: %v", err)
	}
	if got.BundleName == nil || *got.BundleName != "w140-x" {
		t.Fatalf("NULL→X: bundle name = %v, want w140-x", got.BundleName)
	}
	// X→Y: the rename.
	got, err = patchBundle(t, env, id, strPtrTest("w140-y"))
	if err != nil {
		t.Fatalf("X→Y refused inside the R2 window: %v", err)
	}
	if got.BundleName == nil || *got.BundleName != "w140-y" {
		t.Fatalf("X→Y: bundle name = %v, want w140-y", got.BundleName)
	}

	// X→NULL: the unbind; status stays queued.
	got, err = patchBundle(t, env, id, nil)
	if err != nil {
		t.Fatalf("X→NULL (unbind) refused inside the R2 window: %v", err)
	}
	if got.BundleName != nil {
		t.Fatalf("unbind left the reference set: %v", got.BundleName)
	}
	if got.Status != api.StatusQueued {
		t.Fatalf("unbind wrote a status: %s", got.Status)
	}
	// a digest-carrying body and an empty-name shape are typed bad input.
	if _, err := env.WIs.Patch(id, map[string]json.RawMessage{"bundle_digest": json.RawMessage(`"x"`)}, env.UserID); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("bundle_digest field: want CORE-400 (not patchable), got %v", err)
	}
	if _, err := patchBundle(t, env, id, strPtrTest("")); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("empty bundle_name: want CORE-400, got %v", err)
	}
}

// TestPatchBundleWindowRefusals: outside the R2 window the PATCH is
// refused with the IMMUTABLE/TERMINAL_STATE family and writes nothing —
// running (live session), failed (terminal after a claim), canceled
// (terminal without a claim), and force-ended (the claim event outlives
// the cleared session row).
func TestPatchBundleWindowRefusals(t *testing.T) {
	env := newEnv(t)
	m := NewSessionManager(env.DB)

	// running (claimed, live session) → IMMUTABLE.
	running := mustCreateBound(t, env, "w140 running refusal", "w140-w-r")
	mustClaim(t, env, running)
	if _, err := patchBundle(t, env, running, strPtrTest("w140-w-r2")); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "IMMUTABLE" {
		t.Fatalf("running: want CORE-400/IMMUTABLE, got %v", err)
	}

	// failed (terminal after a claim) → TERMINAL_STATE.
	failed := mustCreateBound(t, env, "w140 failed refusal", "w140-w-f")
	secret := mustClaim(t, env, failed)
	if err := m.End(failed, api.SessionEndRequest{SessionID: secret, Status: api.StatusFailed}, env.UserID); err != nil {
		t.Fatalf("fail %s: %v", failed, err)
	}
	if _, err := patchBundle(t, env, failed, strPtrTest("w140-w-f2")); !IsCode(err, api.ErrCoreGone) || ErrorReason(err) != "TERMINAL_STATE" {
		t.Fatalf("failed: want CORE-410/TERMINAL_STATE, got %v", err)
	}

	// canceled (terminal, never claimed) → TERMINAL_STATE.
	canceled := mustCreateBound(t, env, "w140 canceled refusal", "w140-w-c")
	if _, err := env.WIs.Cancel(canceled, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatalf("cancel %s: %v", canceled, err)
	}
	if _, err := patchBundle(t, env, canceled, strPtrTest("w140-w-c2")); !IsCode(err, api.ErrCoreGone) || ErrorReason(err) != "TERMINAL_STATE" {
		t.Fatalf("canceled: want CORE-410/TERMINAL_STATE, got %v", err)
	}

	// force-ended back to queued: the session row is cleared but the
	// claim event remains — the window stays closed (never-claimed means
	// no claim event, not merely no live session).
	requeued := mustCreateBound(t, env, "w140 force-end refusal", "w140-w-r3")
	mustClaim(t, env, requeued)
	if err := m.ForceEnd(requeued, "w140 unit force-end", env.UserID); err != nil {
		t.Fatalf("force-end %s: %v", requeued, err)
	}
	if _, err := patchBundle(t, env, requeued, strPtrTest("w140-w-r4")); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "IMMUTABLE" {
		t.Fatalf("force-ended (claim event remains): want CORE-400/IMMUTABLE, got %v", err)
	}

	// every refusal above wrote nothing: the surviving references are
	// the create-time ones.
	for id, want := range map[string]string{running: "w140-w-r", failed: "w140-w-f", canceled: "w140-w-c", requeued: "w140-w-r3"} {
		wi, err := env.WIs.Get(id, GetOptions{})
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if wi.BundleName == nil || *wi.BundleName != want {
			t.Fatalf("refused PATCH wrote on %s: bundle_name = %v, want %s", id, wi.BundleName, want)
		}
	}
}

// TestPatchBundleWaitingOnNotRefused: waiting_on non-empty does not
// refuse the bundle PATCH on its own — the window looks only at queued
// ∧ never-claimed — and the PATCH leaves waiting_on untouched.
func TestPatchBundleWaitingOnNotRefused(t *testing.T) {
	env := newEnv(t)
	id := mustCreateWI(t, env, "w140 waiting_on patch", nil, "")
	if _, err := env.WIs.Patch(id, map[string]json.RawMessage{"waiting_on": json.RawMessage(`"owner"`)}, env.UserID); err != nil {
		t.Fatalf("patch waiting_on: %v", err)
	}
	got, err := patchBundle(t, env, id, strPtrTest("w140-wait-b"))
	if err != nil {
		t.Fatalf("bundle PATCH with waiting_on non-empty refused: %v", err)
	}
	if got.WaitingOn == nil || *got.WaitingOn != "owner" {
		t.Fatalf("waiting_on = %v, want owner (untouched)", got.WaitingOn)
	}
	if got.BundleName == nil || *got.BundleName != "w140-wait-b" {
		t.Fatalf("bundle_name = %v, want w140-wait-b", got.BundleName)
	}
}

// TestPatchBundleRepoInventoryUntouched: the shared persist path does
// not touch the §4.6 frozen repo triple — a WI created with an
// inventory keeps it verbatim through bundle binds and unbinds.
func TestPatchBundleRepoInventoryUntouched(t *testing.T) {
	env := newEnv(t)
	reposProject(t, env)
	wi, err := env.WIs.Create(CreateRequest{
		Project: "bravo", Goal: "w140 inventory untouched", Reporter: env.UserID,
		BundleName:    strPtrTest("w140-inv-a"),
		RepoInventory: names("app"),
	})
	if err != nil {
		t.Fatalf("create with inventory: %v", err)
	}
	before, err := repoInventoryOf(env.DB, wi.ID)
	if err != nil {
		t.Fatalf("inventory read: %v", err)
	}
	if _, err := patchBundle(t, env, wi.ID, strPtrTest("w140-inv-b")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := patchBundle(t, env, wi.ID, nil); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	after, err := repoInventoryOf(env.DB, wi.ID)
	if err != nil {
		t.Fatalf("inventory read: %v", err)
	}
	if len(after) != len(before) || len(after) != 1 ||
		after[0] != before[0] {
		t.Fatalf("bundle PATCH mutated the frozen inventory: before %+v after %+v", before, after)
	}
}

// TestPatchBundleWithChildrenRefused: the symmetric children-side rule —
// a ticket WITH children never gains a bundle, children counting in any
// status (terminal included), and the refusal writes nothing.
func TestPatchBundleWithChildrenRefused(t *testing.T) {
	// The children side: file a real child under the parent.
	env2 := newEnv(t)
	p2, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 parent", Reporter: env2.UserID,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	c2, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 child", Reporter: env2.UserID, ParentID: strPtrTest(p2.ID),
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := patchBundle(t, env2, p2.ID, strPtrTest("w140-sym-p")); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "HAS_CHILDREN_NO_BUNDLE" {
		t.Fatalf("bind on parent with children: want CORE-400/HAS_CHILDREN_NO_BUNDLE, got %v", err)
	}
	// ... and a terminal child still counts as a child.
	if _, err := env2.WIs.Cancel(c2.ID, env2.UserID, api.CancelRequest{}); err != nil {
		t.Fatalf("cancel child: %v", err)
	}
	if _, err := patchBundle(t, env2, p2.ID, strPtrTest("w140-sym-p")); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "HAS_CHILDREN_NO_BUNDLE" {
		t.Fatalf("bind on parent whose child is terminal: want CORE-400/HAS_CHILDREN_NO_BUNDLE, got %v", err)
	}
	got, err := env2.WIs.Get(p2.ID, GetOptions{})
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if got.BundleName != nil {
		t.Fatalf("refused PATCH wrote: bundle_name = %v, want null", got.BundleName)
	}

	// The create-side mirror: a bundle-bound ticket is never a parent.
	q, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 bound ticket", Reporter: env2.UserID,
		BundleName: strPtrTest("w140-sym-q"),
	})
	if err != nil {
		t.Fatalf("create bound ticket: %v", err)
	}
	before, err := env2.WIs.List(ListFilters{Project: "alpha"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 child under bound parent", Reporter: env2.UserID,
		ParentID: strPtrTest(q.ID),
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "PARENT_NOT_REGISTRY" {
		t.Fatalf("create child under bound parent: want CORE-400/PARENT_NOT_REGISTRY, got %v", err)
	}
	after, err := env2.WIs.List(ListFilters{Project: "alpha"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("refused create persisted a row: %d before, %d after", len(before), len(after))
	}

	// ... and the unbound parent accepts children (parent_id lands).
	r, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 unbound parent", Reporter: env2.UserID,
	})
	if err != nil {
		t.Fatalf("create unbound parent: %v", err)
	}
	rc, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 child under unbound parent", Reporter: env2.UserID,
		ParentID: strPtrTest(r.ID),
	})
	if err != nil {
		t.Fatalf("create child under unbound parent: %v", err)
	}
	if rc.ParentID == nil || *rc.ParentID != r.ID {
		t.Fatalf("child parent_id = %v, want %s", rc.ParentID, r.ID)
	}

	// Depth is one level (wi-kind-contract.md §2): a child is never a parent, and the
	// refused create persists nothing.
	before, err = env2.WIs.List(ListFilters{Project: "alpha"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if _, err := env2.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "w140 grandchild", Reporter: env2.UserID,
		ParentID: strPtrTest(rc.ID),
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "PARENT_DEPTH" {
		t.Fatalf("create grandchild: want CORE-400/PARENT_DEPTH, got %v", err)
	}
	after, err = env2.WIs.List(ListFilters{Project: "alpha"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("refused grandchild create persisted a row: %d before, %d after", len(before), len(after))
	}
}

// TestRaceBindBundleVsNewChild: PATCH bundle (NULL→X) vs create of a
// child under the same ticket (the w140 race pair 2) — exactly one wins
// per round and the final state never shows children ∧ bound bundle.
func TestRaceBindBundleVsNewChild(t *testing.T) {
	env := newEnv(t)
	patchWins, createWins := 0, 0
	for i := 0; i < 10; i++ {
		parent, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "w140 race2 parent", Reporter: env.UserID})
		if err != nil {
			t.Fatalf("create parent: %v", err)
		}
		var patchErr, createErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, patchErr = patchBundle(t, env, parent.ID, strPtrTest("w140-race2-bound"))
		}()
		go func() {
			defer wg.Done()
			<-start
			_, createErr = env.WIs.Create(CreateRequest{
				Project: "alpha", Goal: "w140 race2 child", Reporter: env.UserID, ParentID: strPtrTest(parent.ID),
			})
		}()
		close(start)
		wg.Wait()

		if (patchErr == nil) == (createErr == nil) {
			t.Fatalf("round %d: exactly one of bind-bundle / new-child must win (patchErr=%v createErr=%v)", i, patchErr, createErr)
		}
		got, err := env.WIs.Get(parent.ID, GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		children := 0
		kids, err := env.WIs.List(ListFilters{Project: "alpha"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, k := range kids {
			if k.ParentID != nil && *k.ParentID == parent.ID {
				children++
			}
		}
		bound := got.BundleName != nil
		if createErr == nil {
			createWins++
			if bound {
				t.Fatalf("round %d: create won but the parent is ALSO bundle-bound (children ∧ bundle must not coexist)", i)
			}
			if children == 0 {
				t.Fatalf("round %d: create won but no child row landed", i)
			}
		} else {
			patchWins++
			if !IsCode(createErr, api.ErrCoreBadInput) {
				t.Fatalf("round %d: losing create is not a typed refusal: %v", i, createErr)
			}
			if !bound {
				t.Fatalf("round %d: PATCH won but the parent has no bundle", i)
			}
			if children != 0 {
				t.Fatalf("round %d: PATCH won but a child landed anyway (children ∧ bundle must not coexist)", i)
			}
		}
	}
	t.Logf("w140 race2: PATCH won %d, create won %d", patchWins, createWins)
}

// TestPatchWaitingOnValueDomain (polyforge#149): the two LIVE values
// ('owner' and 'dependency:<slug>') are accepted and persist; the two
// dead branches ('gate' and 'runtime:*') are rejected with the
// validation error and write nothing. The empty clear stays valid.
func TestPatchWaitingOnValueDomain(t *testing.T) {
	env := newEnv(t)
	id := mustCreateWI(t, env, "waiting_on value domain", nil, "")

	patch := func(v string) (*WIView, error) {
		return env.WIs.Patch(id, map[string]json.RawMessage{
			"waiting_on": json.RawMessage(`"` + v + `"`),
		}, env.UserID)
	}
	// the retained values persist (read back the same value).
	for _, live := range []string{"owner", "dependency:demo-bundle"} {
		got, err := patch(live)
		if err != nil {
			t.Fatalf("waiting_on=%q: want accepted, got %v", live, err)
		}
		if got.WaitingOn == nil || *got.WaitingOn != live {
			t.Fatalf("waiting_on=%q did not persist (read back %v)", live, got.WaitingOn)
		}
	}
	// the dead branches are rejected and write nothing.
	for _, dead := range []string{"gate", "runtime:spawn", "runtime:anything"} {
		if _, err := patch(dead); !IsCode(err, api.ErrCoreBadInput) {
			t.Fatalf("waiting_on=%q: want CORE-400, got %v", dead, err)
		}
		got, gerr := env.WIs.Get(id, GetOptions{})
		if gerr != nil {
			t.Fatal(gerr)
		}
		if got.WaitingOn == nil || *got.WaitingOn == dead {
			t.Fatalf("the refused waiting_on=%q persisted (%v)", dead, got.WaitingOn)
		}
	}
	// the null clear is accepted.
	got, err := env.WIs.Patch(id, map[string]json.RawMessage{"waiting_on": json.RawMessage("null")}, env.UserID)
	if err != nil {
		t.Fatalf("null clear: %v", err)
	}
	if got.WaitingOn != nil {
		t.Fatalf("null clear left waiting_on=%v", *got.WaitingOn)
	}
}

// TestGoalLimitCountedInRunes (polyforge#149 D3): the goal cap counts
// CHARACTERS, not bytes — a 500-character CJK goal (1500 bytes) is
// accepted through create and PATCH; 501 characters is refused on both.
func TestGoalLimitCountedInRunes(t *testing.T) {
	env := newEnv(t)
	cjk := strings.Repeat("目", 500) // 500 characters, 1500 bytes
	wi, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: cjk, Reporter: env.UserID,
	})
	if err != nil {
		t.Fatalf("create with a 500-character (1500-byte) goal refused: %v", err)
	}
	id := wi.ID
	if _, err := env.WIs.Patch(id, map[string]json.RawMessage{"goal": json.RawMessage(`"` + cjk + `"`)}, env.UserID); err != nil {
		t.Fatalf("PATCH with a 500-character goal refused: %v", err)
	}
	long := strings.Repeat("目", 501)
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: long, Reporter: env.UserID,
	}); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("create with a 501-character goal: want CORE-400, got %v", err)
	}
	if _, err := env.WIs.Patch(id, map[string]json.RawMessage{"goal": json.RawMessage(`"` + long + `"`)}, env.UserID); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("PATCH with a 501-character goal: want CORE-400, got %v", err)
	}
}
