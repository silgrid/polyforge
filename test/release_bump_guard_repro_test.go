package test

// repro: the canonical .polyforge/release.sh defaults its bump
// argument to minor (BUMP="${1:-minor}"), so a no-argument release silently
// crosses the minor version line, and an explicit minor/major is accepted with
// no owner declaration at all. Owner policy (mem_TBUc1vUWK9nN, re-stated
// 2026-10-24): routine releases are patch bumps; a minor (middle-number) jump
// must be explicitly declared by the owner; major means an architecture
// rebuild. The guard must therefore be patch-only by default and fail loud on
// minor/major unless the owner declaration is present.
//
// These subtests assert the POLICY, so they are red on the defective script
// (the reported failure: floor v1.2.0, no args -> v1.3.0 minor crossing) and
// go green once the guard matches the pi-web- ruling.

import (
	"strings"
	"testing"
)

func TestReleaseScriptBumpGuardRepro(t *testing.T) {
	// The floor tag the worlds release from; a patch bump of v1.2.0 is
	// v1.2.1, a minor bump would cross the middle number to v1.3.0.
	const (
		floor = "v1.2.0"
		patch = "v1.2.1"
		minor = "v1.3.0"
	)

	t.Run("no args defaults to patch, never a silent minor crossing", func(t *testing.T) {
		w := newRelWorld(t, []string{floor}, nil)
		out, err := w.run(nil)
		if err != nil {
			t.Fatalf("a no-argument release must still succeed (as a patch bump): %v\n%s", err, out)
		}
		if !strings.Contains(out, "version "+patch) || strings.Contains(out, minor) {
			t.Fatalf("no-args run crossed the minor version: want %s (patch of floor %s), saw %s in\n%s",
				patch, floor, minor, out)
		}
		if got := w.originTags(); !strings.Contains(got, patch) || strings.Contains(got, minor) {
			t.Fatalf("origin tags = %q: want the patch tag %s published, not the minor tag %s", got, patch, minor)
		}
	})

	t.Run("explicit minor fails loud without an owner declaration", func(t *testing.T) {
		w := newRelWorld(t, []string{floor}, nil)
		out, err := w.run(nil, "minor")
		if err == nil {
			t.Fatalf("explicit minor must be refused without an owner declaration, but the run succeeded:\n%s", out)
		}
		if len(w.calls()) != 0 {
			t.Fatalf("a refused bump must publish nothing, calls=%v", w.calls())
		}
		if got := w.originTags(); strings.Contains(got, minor) {
			t.Fatalf("refused minor still pushed its tag to origin: %q", got)
		}
		low := strings.ToLower(out)
		if !strings.Contains(low, "minor") || !strings.Contains(low, "owner") {
			t.Fatalf("the refusal must name the bump (minor) and the owner-declaration requirement:\n%s", out)
		}
	})

	t.Run("explicit major fails loud without an owner declaration", func(t *testing.T) {
		w := newRelWorld(t, []string{floor}, nil)
		out, err := w.run(nil, "major")
		if err == nil {
			t.Fatalf("explicit major must be refused without an owner declaration, but the run succeeded:\n%s", out)
		}
		if len(w.calls()) != 0 {
			t.Fatalf("a refused bump must publish nothing, calls=%v", w.calls())
		}
		low := strings.ToLower(out)
		if !strings.Contains(low, "major") || !strings.Contains(low, "owner") {
			t.Fatalf("the refusal must name the bump (major) and the owner-declaration requirement:\n%s", out)
		}
	})

	t.Run("explicit patch stays allowed and publishes the patch tag", func(t *testing.T) {
		w := newRelWorld(t, []string{floor}, nil)
		out, err := w.run(nil, "patch")
		if err != nil {
			t.Fatalf("explicit patch must keep working: %v\n%s", err, out)
		}
		if !strings.Contains(out, "version "+patch) {
			t.Fatalf("patch run did not release %s:\n%s", patch, out)
		}
		if got := w.originTags(); !strings.Contains(got, patch) {
			t.Fatalf("origin tags = %q: want the patch tag %s", got, patch)
		}
	})
}
