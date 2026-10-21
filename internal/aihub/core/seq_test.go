package core

// seq_test.go: the per-project seq semantics (owner ruling). The legacy
// library numbers WIs per project (aiterm#1 and defswitch#1 both
// exist), so:
//
//   - create numbers each project from 1;
//   - slugs always resolved by (project, seq), which is unique.

import (
	"testing"
)

// mustProject creates a second project and returns its id.
func mustProject(t *testing.T, env *testEnv, name string) string {
	t.Helper()
	p, err := env.Admin.CreateProject(CreateProjectRequest{Name: name})
	if err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
	return p.ID
}

// TestCreateSeqPerProject: two projects each number from 1; slugs
// resolve by (project, seq); cross-project same-seq coexist.
func TestCreateSeqPerProject(t *testing.T) {
	env := newEnv(t)
	beta := mustProject(t, env, "beta")

	a1, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "alpha one", Reporter: env.UserID})
	if err != nil {
		t.Fatal(err)
	}
	b1, err := env.WIs.Create(CreateRequest{Project: beta, Goal: "beta one", Reporter: env.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if a1.Seq != 1 || b1.Seq != 1 {
		t.Fatalf("first seqs = alpha %d, beta %d; both must be 1 (per-project numbering)", a1.Seq, b1.Seq)
	}
	if a1.Slug != "alpha#1" || b1.Slug != "beta#1" {
		t.Fatalf("slugs drifted: %q %q", a1.Slug, b1.Slug)
	}
	a2, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "alpha two", Reporter: env.UserID})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := env.WIs.Create(CreateRequest{Project: beta, Goal: "beta two", Reporter: env.UserID})
	if err != nil {
		t.Fatal(err)
	}
	if a2.Seq != 2 || b2.Seq != 2 {
		t.Fatalf("second seqs = alpha %d, beta %d; both must be 2", a2.Seq, b2.Seq)
	}

	// Lookup by (project, seq) resolves each project's OWN #1/#2.
	gotA, err := env.WIs.Get("alpha#1", GetOptions{})
	if err != nil || gotA.ID != a1.ID {
		t.Fatalf("alpha#1 resolved to %v (%v)", gotA, err)
	}
	gotB, err := env.WIs.Get("beta#1", GetOptions{})
	if err != nil || gotB.ID != b1.ID {
		t.Fatalf("beta#1 resolved to %v (%v)", gotB, err)
	}
	// A foreign prefix does not leak: beta#2 is beta's second, never
	// alpha's.
	gotB2, err := env.WIs.Get("beta#2", GetOptions{})
	if err != nil || gotB2.ID != b2.ID {
		t.Fatalf("beta#2 resolved to %v (%v)", gotB2, err)
	}
}
