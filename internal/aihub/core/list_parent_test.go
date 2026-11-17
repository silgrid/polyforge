package core

// list_parent_test.go (polyforge#117 u4): the list query gains a parent
// filter — `GET /work-items?parent=<id>` answers exactly the work items
// whose parent_id is that id, newest first, beside every other filter.
// The epic lane's daemon lists an epic's children through it (the claim
// snapshot and the running re-check).
//
// Expected values are the fixture's own: two children under one parent,
// a third work item under another, and a parentless one — the parent
// filter answers the two, nothing else, and the plain list is unchanged.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// lpChild files one child under parent (polyforge#77's shape: a child
// carries its parent's id).
func lpChild(t *testing.T, env *testEnv, parent, goal string) string {
	t.Helper()
	wi, err := env.WIs.Create(CreateRequest{
		Project:  "alpha",
		Goal:     goal,
		Reporter: env.UserID,
		ParentID: strPtrTest(parent),
	})
	if err != nil {
		t.Fatalf("create child %q: %v", goal, err)
	}
	return wi.ID
}

// TestListFiltersParent: the parent filter narrows the list to exactly the
// parent's children; the filter combines with the project one and an
// unknown parent answers an empty page.
func TestListFiltersParent(t *testing.T) {
	env := newEnv(t)
	parent := mustCreateWI(t, env, "the epic", nil, "")
	childA := lpChild(t, env, parent, "child a")
	childB := lpChild(t, env, parent, "child b")
	otherParent := mustCreateWI(t, env, "another epic", nil, "")
	_ = lpChild(t, env, otherParent, "unrelated child")
	stray := mustCreateWI(t, env, "no parent", nil, "")

	wis, err := env.WIs.List(ListFilters{Parent: parent, Project: "alpha"})
	if err != nil {
		t.Fatalf("list by parent: %v", err)
	}
	got := map[string]bool{}
	for _, wi := range wis {
		got[wi.ID] = true
	}
	if len(wis) != 2 || !got[childA] || !got[childB] {
		t.Fatalf("the parent filter answers exactly its children: %v (want %s and %s)", got, childA, childB)
	}
	for _, wi := range wis {
		if wi.ParentID == nil || *wi.ParentID != parent {
			t.Fatalf("a listed work item names the wrong parent: %+v", wi.ParentID)
		}
	}
	// an unknown parent answers an empty page, and a parentless id is
	// never listed under a parent
	wis, err = env.WIs.List(ListFilters{Parent: "wi_nobody"})
	if err != nil || len(wis) != 0 {
		t.Fatalf("an unknown parent answers an empty page, got %v, %v", wis, err)
	}
	// a work item that is nobody's parent answers an empty page: the
	// filter matches parent_id exactly, never a grandchild chain
	wis, err = env.WIs.List(ListFilters{Parent: childA, Project: "alpha"})
	if err != nil || len(wis) != 0 {
		t.Fatalf("a work item with no children answers an empty page, got %v, %v", wis, err)
	}
	// the plain list is unchanged: every item is there (six were created)
	wis, err = env.WIs.List(ListFilters{Project: "alpha"})
	if err != nil || len(wis) != 6 {
		t.Fatalf("the plain list still answers everything: %d, %v", len(wis), err)
	}
	seen := map[string]bool{childA: true, childB: true, parent: true, stray: true}
	for _, wi := range wis {
		delete(seen, wi.ID)
	}
	if len(seen) != 0 {
		t.Fatalf("the plain list lost %v", seen)
	}
	_ = api.StatusWrapped
}
