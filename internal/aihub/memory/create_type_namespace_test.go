package memory

// Repro test for polyforge#210 (memory type taxonomy drift).
//
// Observed in production aihub (2026-10-18): entries stored with invented
// top-level types `polyforge-learnings` and `code-learnings`, written by a
// credentialed caller outside step mode (the operator's chat agent via
// `pf remember`); steps are already confined to experience.wrap by
// stepmem.Check. aihub Create accepted them because it validates only that the
// type is non-empty (store.go Create: `if req.Type == ""`); there is no
// single type authority.
//
// Intended behavior (the fix's contract, encoded here so verification can
// run it mechanically): Create validates the type's namespace — the kind
// before the first dot must be one of experience|fact|doc|rule|methodology
// — and rejects anything else loudly, naming the valid kinds. Valid dotted
// types keep being accepted.

import (
	"strings"
	"testing"
)

// TestCreateRejectsInventedTypeNamespace: the exact drifted types observed
// in production must be refused by Create with MEM-400, the rejection
// naming the rejected type and the valid kinds.
func TestCreateRejectsInventedTypeNamespace(t *testing.T) {
	e := newEnv(t)

	for _, typ := range []string{"polyforge-learnings", "code-learnings"} {
		entry, err := e.store.Create(CreateRequest{
			Project: "alpha", Type: typ,
			Visibility: "project", Content: "body of a drifted memory",
		}, e.user)
		if err == nil {
			t.Fatalf("type %q: Create accepted an invented top-level kind (stored entry %s); want a loud MEM-400 rejection naming the valid kinds",
				typ, entry.ID)
		}
		apiErr, ok := err.(*APIError)
		if !ok {
			t.Fatalf("type %q: error %v is not a memory APIError", typ, err)
		}
		if apiErr.Code != "MEM-400" {
			t.Fatalf("type %q: error code %q, want MEM-400", typ, apiErr.Code)
		}
		msg := strings.ToLower(apiErr.Error())
		if !strings.Contains(msg, strings.ToLower(typ)) {
			t.Fatalf("type %q: rejection %q does not name the rejected type", typ, apiErr.Error())
		}
		for _, kind := range []string{"experience", "fact", "doc", "rule", "methodology"} {
			if !strings.Contains(msg, kind) {
				t.Fatalf("type %q: rejection %q does not name valid kind %q", typ, apiErr.Error(), kind)
			}
		}
	}
}

// TestCreateAcceptsValidTypeNamespaces: every valid kind namespace, dotted
// as kind.name, must keep being accepted and stored verbatim.
func TestCreateAcceptsValidTypeNamespaces(t *testing.T) {
	e := newEnv(t)

	for _, typ := range []string{
		"experience.wrap", "experience.debug", "fact.note",
		"doc.review", "doc.design", "doc.decision", "doc.audit",
		"rule.release", "methodology.review",
	} {
		entry, err := e.store.Create(CreateRequest{
			Project: "alpha", Type: typ,
			Visibility: "project", Content: "body",
		}, e.user)
		if err != nil {
			t.Fatalf("type %q: Create rejected a valid kind: %v", typ, err)
		}
		if entry.Type != typ {
			t.Fatalf("type %q: stored entry carries type %q", typ, entry.Type)
		}
	}
}
