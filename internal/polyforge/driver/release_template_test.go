package driver

// release_template_test.go: content pins for the release workflow bundle
// (workflows/release) and for its absence from the delivery bundles.
//
// The release bundle is project-generic: the procedures live in each
// project's repo (.polyforge/release.sh, .polyforge/deploy.sh), so the
// shared text must carry no polyforge-private procedure markers. And
// releases happen ONLY through this bundle: no delivery lane (feature-full,
// bug-fix, autopilot, chore) may carry a release step.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bundlePath(bundle string, elem ...string) string {
	return filepath.Join(append([]string{"..", "..", "..", "workflows", bundle}, elem...)...)
}

func readBundleFile(t *testing.T, bundle string, elem ...string) string {
	t.Helper()
	raw, err := os.ReadFile(bundlePath(bundle, elem...))
	if err != nil {
		t.Fatalf("cannot read %s: %v", bundlePath(bundle, elem...), err)
	}
	return string(raw)
}

// privateProcedureMarkers are polyforge-private procedure markers that
// MUST NOT appear anywhere in the shared release bundle text.
// Case-insensitive.
var privateProcedureMarkers = []string{
	"releases_root",
	"gowork=off make release",
	"sha256sums",
	"polyforge release procedure",
	`mkdir "$releases_root`,
	"sort -vu",
	"git tag -l",
	"intranet",
	"aihub",
	"-ldflags",
	"pf upgrade",
	"checksum",
	"00-architecture.md",
	"07-deployment-pilot.md",
	"ghcr.io",
	"docker",
}

// TestReleaseBundleCarriesNoPrivateProcedure pins the project-generic
// posture of every file in the release bundle.
func TestReleaseBundleCarriesNoPrivateProcedure(t *testing.T) {
	files, err := filepath.Glob(bundlePath("release", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := filepath.Glob(bundlePath("release", "steps", "*.md"))
	files = append(files, steps...)
	if len(files) != 4 {
		t.Fatalf("release bundle has %d markdown files (%v), want workflow.md + 3 steps", len(files), files)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(string(raw))
		for _, m := range privateProcedureMarkers {
			if strings.Contains(low, m) {
				t.Fatalf("%s carries polyforge-private procedure text %q - the procedure lives in the repo's .polyforge/*.sh, never in the shared bundle", f, m)
			}
		}
	}
}

// TestReleaseBundleShape pins the step list, gates, context policy, the
// evidence flags (the V4 command-evidence gate), the absence of rewinds,
// the path-convention artifact hand-over and the per-step contract text.
// The bundle carries none of the retired frontmatter keys (inputs).
func TestReleaseBundleShape(t *testing.T) {
	wf, err := LoadWorkflow(bundlePath("release"))
	if err != nil {
		t.Fatalf("release bundle must load and validate: %v", err)
	}
	var ids []string
	for _, s := range wf.Steps {
		ids = append(ids, s.ID)
		if !s.Gate || s.ContextPolicy != ContextFresh || !s.RequiresCommandEvidence {
			t.Fatalf("step %q must be gate + fresh + requires_command_evidence (gate=%v ctx=%q evidence=%v)", s.ID, s.Gate, s.ContextPolicy, s.RequiresCommandEvidence)
		}
		if s.OnFail != nil {
			t.Fatalf("step %q must not declare on_fail: a failed release or deploy must terminate, never rewind into a re-release (immutability)", s.ID)
		}
	}
	if got, want := strings.Join(ids, ","), "release,verify,deploy"; got != want {
		t.Fatalf("release bundle steps = %s, want %s", got, want)
	}
	wfText := readBundleFile(t, "release", "workflow.md")
	for _, retired := range []string{"inputs: [", "handoff_ref", "sha256"} {
		if strings.Contains(wfText, retired) {
			t.Fatalf("release workflow.md still carries the retired %q", retired)
		}
	}

	rel := readBundleFile(t, "release", "steps", "release.md")
	for _, needle := range []string{
		"`.polyforge/release.sh`", "PF-RELEASE-AMBIGUOUS_OWNER", "--verify", "git fetch origin",
		"git ls-remote --tags origin", "verbatim", "enum: [pass, fail]", "artifacts/release-record.json", "a completed result without evidence is refused",
	} {
		if !strings.Contains(rel, needle) {
			t.Fatalf("release step lost its contract text %q", needle)
		}
	}
	// The already-released guard (repo review 2026-10-16 V-6), before the
	// procedure runs: a release tag (v*) already on origin_head refuses
	// whether or not an earlier attempt left its list; with a kept list,
	// ANY remote tag line it lacks refuses; the kept list is never
	// overwritten; a normal first run passes.
	guard := strings.Index(rel, "PF-RELEASE-ALREADY_RELEASED")
	for _, needle := range []string{
		"The head is already tagged", "whether or not any file from an earlier", "name starts with `v`", "equals `origin_head`",
		"never overwrite it", "any tag, on any commit", "comm -13", "A normal first run passes both",
	} {
		if guard < 0 || !strings.Contains(rel, needle) {
			t.Fatalf("the release step lost its already-released guard text %q (PF-RELEASE-ALREADY_RELEASED)", needle)
		}
	}
	if run := strings.Index(rel, "## 4. Run the procedure"); run < 0 || guard > run {
		t.Fatal("the already-released guard must come before the procedure runs")
	}
	if strings.Contains(rel, "enum: [pass, defer, fail]") || strings.Contains(rel, "enum: [defer]") {
		t.Fatal("the release step must not admit a defer verdict: a release WI without a procedure is an error")
	}

	ver := readBundleFile(t, "release", "steps", "verify.md")
	for _, needle := range []string{"release-record.json", "verified-version.json", "ls-remote", "merge-base --is-ancestor", "--verify V", "immutab", "gh release view", "a completed result without command evidence is refused"} {
		if !strings.Contains(ver, needle) {
			t.Fatalf("verify step lost its check text %q", needle)
		}
	}

	dep := readBundleFile(t, "release", "steps", "deploy.md")
	for _, needle := range []string{"`.polyforge/deploy.sh`", "--confirm V", "PF-DEPLOY-AMBIGUOUS_OWNER", "defer", `"latest"`, "enum: [pass, defer, fail]", "rolled_back", "artifacts/verified-version.json", "a completed result without evidence is refused"} {
		if !strings.Contains(dep, needle) {
			t.Fatalf("deploy step lost its contract text %q", needle)
		}
	}
}

// TestDeliveryBundlesCarryNoReleaseStep pins decision B: releases happen
// only via workflows/release.
func TestDeliveryBundlesCarryNoReleaseStep(t *testing.T) {
	for _, bundle := range []string{"feature-full", "bug-fix", "autopilot", "chore"} {
		if _, err := os.Stat(bundlePath(bundle, "steps", "release.md")); err == nil {
			t.Fatalf("%s still ships steps/release.md - releases happen only via the release bundle", bundle)
		}
		wf, err := LoadWorkflow(bundlePath(bundle))
		if err != nil {
			t.Fatalf("%s: %v", bundle, err)
		}
		for _, s := range wf.Steps {
			if s.ID == "release" || s.ID == "deploy" {
				t.Fatalf("%s declares a %q step", bundle, s.ID)
			}
			if s.OnFail != nil && s.OnFail.Target == "release" {
				t.Fatalf("%s step %q rewinds to release", bundle, s.ID)
			}
		}
	}
}
