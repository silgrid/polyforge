package test

// w80_prompt_trim_contract_test.go: text contract for the w80 gate-step
// prompt trim.
//
//  1. The default-branch fallback strips the remote prefix: `git
//     symbolic-ref --short refs/remotes/origin/HEAD` prints `origin/main`,
//     so a bare fallback turns `origin/<default>` into `origin/origin/main`.
//     Every step file that resolves `<default>` this way pipes it through
//     `sed 's|^origin/||'`. Red on the pre-change tree: the three files
//     carry the bare form.
//  2. One source per repeated block: each shared fragment is included by
//     the step files that need it (at least two — a repeated block), its
//     text appears exactly once in each rendered definition, and no raw
//     step file carries a second copy. The driver-rendered rare-path
//     sections have no copy in any step file either.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

const (
	w80BareFallback  = "`git symbolic-ref --short refs/remotes/origin/HEAD`"
	w80FixedFallback = "`git symbolic-ref --short refs/remotes/origin/HEAD | sed 's|^origin/||'`"
)

func w80Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestW80DefaultBranchFallbackStripsRemote(t *testing.T) {
	for _, rel := range []string{
		"workflows/_shared/steps/deliver.md",
		"workflows/_shared/steps/verification.md",
		"workflows/release/steps/release.md",
	} {
		text := w80Read(t, rel)
		if !strings.Contains(text, w80FixedFallback) {
			t.Errorf("%s: the default-branch fallback must strip the remote prefix (%s)", rel, w80FixedFallback)
		}
		if strings.Contains(text, w80BareFallback) {
			t.Errorf("%s: still carries the un-stripped fallback %s (yields origin/origin/main)", rel, w80BareFallback)
		}
	}
}

// w80StepFiles answers every step definition file under workflows/
// (bundle-own and _shared/steps alike), repo-relative.
func w80StepFiles(t *testing.T) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(v1Root(t), "workflows", "*", "steps", "*.md"))
	if err != nil || len(m) == 0 {
		t.Fatalf("no step files: %v", err)
	}
	var out []string
	for _, p := range m {
		rel, _ := filepath.Rel(v1Root(t), p)
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func TestW80SharedFragmentsAreTheOneSource(t *testing.T) {
	files := w80StepFiles(t)
	for _, frag := range []string{"fast_checks", "fast_checks_build", "batch_questions", "handoff_duty"} {
		text := strings.TrimSpace(w80Read(t, "workflows/_shared/"+frag+".md"))
		var users []string
		for _, rel := range files {
			raw := w80Read(t, rel)
			if strings.Contains(raw, text) {
				t.Errorf("%s carries a second copy of the %s fragment", rel, frag)
			}
			if strings.Contains(raw, "{{"+frag+"}}") {
				users = append(users, rel)
			}
		}
		if len(users) < 2 {
			t.Errorf("fragment %s is included by %d step files (%v), want a repeated block (>= 2)", frag, len(users), users)
		}
		for _, rel := range users {
			// rel is workflows/<bundle>/steps/<id>.md; the users carry no vars
			parts := strings.Split(rel, "/")
			bundle, step := parts[1], strings.TrimSuffix(parts[3], ".md")
			out, err := workflow.ReadStepDefinition(filepath.Join(v1Root(t), "workflows", bundle), step, nil)
			if err != nil {
				t.Fatalf("render %s: %v", rel, err)
			}
			if n := strings.Count(string(out), text); n != 1 {
				t.Errorf("%s renders the %s fragment %d times, want exactly 1", rel, frag, n)
			}
		}
	}
	// The driver-rendered rare-path sections live once: no step file
	// carries their distinctive lines.
	for frag, line := range map[string]string{
		"scoped_verification": "- **Any engine failure falls back to the full tier.**",
		"scoped_review":       "**Answer `scope_adequate` and record your `scope` when the work item opted",
		"review_reentry":      "On a re-entry, run two additive faces over the current tree:",
	} {
		if !strings.Contains(w80Read(t, "workflows/_shared/"+frag+".md"), line) {
			t.Fatalf("workflows/_shared/%s.md lost its pinned line %q", frag, line)
		}
		for _, rel := range files {
			if strings.Contains(w80Read(t, rel), line) {
				t.Errorf("%s carries a copy of the driver-rendered %s section", rel, frag)
			}
		}
	}
}
