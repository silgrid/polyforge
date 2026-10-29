package test

// w121_release_gh_repo_repro_test.go: repro for.
//
// Reported failure (#120): with RELEASE_GH_REPO unset, .polyforge/release.sh
// silently falls back to the public mirror silgrid/polyforge, so a release
// aimed at the internal repo publishes/verifies against the wrong repo — the
// v2.20.10 tag was pushed to the internal repo but `gh release create`
// defaulted to the public one. The root fix is fail-loud: an unset
// RELEASE_GH_REPO refuses before any work and names the internal example.
// The projection follows: the internal example line must stay under the
// public projection's REWRITE rules, which map it to silgrid/polyforge. The
// owner's ruling replaced the original "an OVERRIDE row must
// exist" requirement with that projection assertion: the existing REWRITE row
// already produced the identical mapping, while an OVERRIDE row would only
// have exempted the file from REWRITE and the identifier invariant.
//
// The fail-loud half is red on the defective tree (the script silently picks
// the public repo and pushes the tag); the projection subtest also fails there
// because release.sh has no internal example for REWRITE to map yet.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// w121InternalRepo is the internal release channel the fail-loud refusal
	// must name as the correct example.
	w121InternalRepo = "silgrid/polyforge"
	// w121PublicRepo is the public mirror the defective default silently used.
	w121PublicRepo = "silgrid/polyforge"
)

// w121RunWithoutGHRepo runs release.sh with RELEASE_GH_REPO absent from the
// environment (the defect's precondition), keeping every other fake-env knob
// the release-script tests set.
func w121RunWithoutGHRepo(t *testing.T, w *relWorld, extra []string, args ...string) (string, error) {
	t.Helper()
	env := w.env(extra...)
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "RELEASE_GH_REPO=") {
			continue
		}
		kept = append(kept, kv)
	}
	c := exec.Command("sh", append([]string{w.script}, args...)...)
	c.Dir = w.work
	c.Env = kept
	out, err := c.CombinedOutput()
	return string(out), err
}

// TestW121ReleaseGHRepoFailLoud pins the fail-loud half: with
// RELEASE_GH_REPO unset, neither release mode nor --verify may run against
// the public default; both must refuse, naming the variable and the internal
// example, before any publish/audit work.
func TestW121ReleaseGHRepoFailLoud(t *testing.T) {
	t.Run("release mode refuses before any work when unset", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		out, err := w121RunWithoutGHRepo(t, w, nil)
		if err == nil {
			t.Fatalf("release succeeded with RELEASE_GH_REPO unset — it must fail loud instead of silently publishing to %s:\n%s", w121PublicRepo, out)
		}
		for _, want := range []string{"RELEASE_GH_REPO", w121InternalRepo} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal must name %q:\n%s", want, out)
			}
		}
		if calls := w.calls(); len(calls) != 0 {
			t.Fatalf("work ran before the RELEASE_GH_REPO refusal: %v", calls)
		}
		if got := w.originTags(); strings.Contains(got, "v1.2.1") {
			t.Fatalf("a tag was pushed despite the refusal: %q", got)
		}
	})

	t.Run("verify mode refuses when unset", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		out, err := w121RunWithoutGHRepo(t, w, nil, "--verify", "v1.2.0")
		if err == nil {
			t.Fatalf("--verify succeeded with RELEASE_GH_REPO unset — it must fail loud instead of auditing %s:\n%s", w121PublicRepo, out)
		}
		for _, want := range []string{"RELEASE_GH_REPO", w121InternalRepo} {
			if !strings.Contains(out, want) {
				t.Errorf("the refusal must name %q:\n%s", want, out)
			}
		}
		if calls := w.calls(); len(calls) != 0 {
			t.Fatalf("work ran before the RELEASE_GH_REPO refusal: %v", calls)
		}
	})
}

// TestW121ReleaseGHRepoOverrideRow pins the projection half (the historical
// name keeps repro.md's focused invocation meaningful; the owner's
// ruling replaced the OVERRIDE requirement with this projection assertion).
// .polyforge/release.sh must carry the internal example and stay under the
// public projection's REWRITE rules, which must map that example to
// silgrid/polyforge. It applies the REWRITE programs exactly as
// tools/sync/project.sh does — one `sed -E` program per row, in order, over the
// file's content — and refuses an EXCLUDE/ALLOWED/OVERRIDE listing that would
// hide the file from those rules and the identifier invariant.
func TestW121ReleaseGHRepoOverrideRow(t *testing.T) {
	root := v1Root(t)
	list := filepath.Join(root, "docs", "sync", "desensitize.md")
	raw, err := os.ReadFile(list)
	if os.IsNotExist(err) {
		t.Skip("docs/sync/desensitize.md is internal-only; the public projection carries no sync list")
	}
	if err != nil {
		t.Fatal(err)
	}

	const path = ".polyforge/release.sh"

	// Parse the sections exactly like project.sh's section(): skip HTML
	// comments and blanks, switch section on `## <name>`.
	var rewrite []string
	exempt := ""
	inSection, inComment := "", false
	for _, line := range strings.Split(string(raw), "\n") {
		if inComment {
			if strings.Contains(line, "-->") {
				inComment = false
			}
			continue
		}
		if strings.Contains(line, "<!--") {
			if !strings.Contains(line, "-->") {
				inComment = true
			}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			fields := strings.Fields(line)
			inSection = ""
			if len(fields) > 1 {
				inSection = fields[1]
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch inSection {
		case "REWRITE":
			rewrite = append(rewrite, line)
		case "EXCLUDE", "ALLOWED", "OVERRIDE":
			if fields := strings.SplitN(line, "\t", 2); strings.TrimSpace(fields[0]) == path {
				exempt = inSection
			}
		}
	}
	if exempt != "" {
		t.Fatalf("%s is listed under %s, so the projection hides it from the REWRITE rules and the identifier invariant; the internal example %s must stay mapped to %s", path, exempt, w121InternalRepo, w121PublicRepo)
	}
	if len(rewrite) == 0 {
		t.Fatalf("docs/sync/desensitize.md's REWRITE section is empty; the projection cannot map %s", path)
	}

	src, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	internal := string(src)
	if !strings.Contains(internal, w121InternalRepo) {
		t.Fatalf("%s does not name the internal example %s, so the projection mapping it to %s is not exercised", path, w121InternalRepo, w121PublicRepo)
	}
	projected := internal
	for _, prog := range rewrite {
		cmd := exec.Command("sed", "-E", prog)
		cmd.Stdin = strings.NewReader(projected)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("applying the REWRITE rule %q to %s failed: %v\n%s", prog, path, err, out)
		}
		projected = string(out)
	}
	if strings.Contains(projected, w121InternalRepo) {
		t.Errorf("the projection's REWRITE rules leave the internal example %s in %s: got %q", w121InternalRepo, path, projected)
	}
	if !strings.Contains(projected, w121PublicRepo) {
		t.Errorf("the projection's REWRITE rules must map the internal example %s to %s in %s", w121InternalRepo, w121PublicRepo, path)
	}
}
