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
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow"
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
	files = append(files, bundlePath("release", workflow.WorkflowFile))
	if len(files) != 5 {
		t.Fatalf("release bundle has %d files (%v), want workflow.yaml, README.md + 3 steps", len(files), files)
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

// TestReleaseBundleShape pins the step list, gates, the contract-2 shape
// of the lane (workflow contract v2 §4.4, change 9: the procedures run by
// the step's own agent; `release` and
// `deploy` hold the heavy-run admission, `release` under a 120-minute tool
// window inside a 135-minute wall, `deploy` under a 30-minute window,
// `verify` a 30-minute wall; no gate waivable), the absence of rewinds,
// the path-convention artifact hand-over and the per-step contract text —
// since workflow contract 4 what each step reads and checks before it
// starts is its definition's 开工前读什么、核对什么 (no verifies/inputs
// keys, no output schema).
func TestReleaseBundleShape(t *testing.T) {
	wf, err := LoadWorkflow(bundlePath("release"))
	if err != nil {
		t.Fatalf("release bundle must load and validate: %v", err)
	}
	var ids []string
	for _, s := range wf.Steps {
		ids = append(ids, s.ID)
		if !s.Gate {
			t.Fatalf("step %q must be a gate", s.ID)
		}
		if s.Waivable || wf.Waivable(s.ID) {
			t.Fatalf("step %q is waivable: no gate of the release lane takes a waiver (a FAIL ends the work item)", s.ID)
		}
		if s.OnFail != nil {
			t.Fatalf("step %q must not declare on_fail: a failed release or deploy must terminate, never rewind into a re-release (immutability)", s.ID)
		}
	}
	if got, want := strings.Join(ids, ","), "release,verify,deploy"; got != want {
		t.Fatalf("release bundle steps = %s, want %s", got, want)
	}
	// the admission and the windows (§4.4): release heavy, tool 120 under
	// wall 135 (the lane's own window must fit the full gate release.sh
	// may run itself); verify wall 30, no resources; deploy heavy, tool 30
	for _, c := range []struct {
		id              string
		heavy           bool
		tool, wall      int
		toolWindowAsDur time.Duration
	}{
		{"release", true, 120, 135, 120 * time.Minute},
		{"verify", false, 0, 30, 0},
		{"deploy", true, 30, 0, 30 * time.Minute},
	} {
		s := wf.step(c.id)
		if got := s.Resources != nil && s.Resources.Heavy(); got != c.heavy {
			t.Errorf("%s: heavy admission %v, want %v (resources: {admission: heavy} on release and deploy only)", c.id, got, c.heavy)
		}
		if s.Resources != nil && len(s.Resources.Locks) != 0 {
			t.Errorf("%s: declares locks %v; the release lane takes none", c.id, s.Resources.Locks)
		}
		if s.ToolMinutes != c.tool || s.WallMinutes != c.wall {
			t.Errorf("%s: budget tool %d / wall %d, want tool %d / wall %d", c.id, s.ToolMinutes, s.WallMinutes, c.tool, c.wall)
		}
		if got := s.toolWindow(); got != c.toolWindowAsDur {
			t.Errorf("%s: the dispatch's tool window %v, want %v", c.id, got, c.toolWindowAsDur)
		}
	}
	// §4.4's tier column, on the contract's own lane model: release and
	// deploy name the mid tier (a placeholder of the shipped pair
	// cheap/mid, PR 8); verify is a strong step (design §7.2 item 19) and
	// names NO tier — strong is not a tier key, a strong step resolves to
	// its workspace step key or the project default.
	lane, err := workflow.LoadStrict(bundlePath("release"))
	if err != nil {
		t.Fatalf("release bundle: %v", err)
	}
	if s := lane.Step("verify"); s.ModelTier != "" || s.ModelCandidates != nil {
		t.Errorf("verify: model_tier %q / candidates %v: the strong judgment gate names no tier and no list (its models are the workspace step key or the project default)", s.ModelTier, s.ModelCandidates)
	}
	for id, tier := range map[string]string{"release": "mid", "deploy": "mid"} {
		s := lane.Step(id)
		if s.ModelTier != tier || s.ModelCandidates != nil {
			t.Errorf("%s: model_tier %q (candidates %v), want the tier %q and no list", id, s.ModelTier, s.ModelCandidates, tier)
		}
		list := lane.TierCandidates(s)
		if len(list) == 0 {
			t.Errorf("%s: tier %q names no candidates in defaults.tiers", id, tier)
		}
		for _, c := range list {
			if !strings.Contains(c, ":example-") {
				t.Errorf("%s: tier %q carries %q: the shipped tiers are placeholders (the workspace sets the real models)", id, tier, c)
			}
		}
	}
	// §4.4's verify-first column (P1: downstream verifies upstream). Since
	// workflow contract 4 no step declares what it verifies: each
	// definition's 开工前读什么、核对什么 says what it reads and checks
	// before it starts, and a check that does not hold is the gate's fail.
	// release has no upstream step; verify checks release's record; deploy
	// checks the verified version against the version of release's own
	// record (contract 3's release.output.version), a check, never a source.
	for _, c := range []struct {
		id   string
		want []string
	}{
		{"release", []string{"You are the lane's first step: no upstream hand-off precedes you."}},
		{"verify", []string{"`artifacts/release-record.json` (the release step's record)",
			"the record must parse as JSON and name `version` and `commit`; one that does not is a `fail` naming what is missing"}},
		{"deploy", []string{"Read `artifacts/verified-version.json`", "If the file is missing, unreadable or empty, hand off `fail`.",
			"`V` must equal the `version` of the release step's record, `artifacts/release-record.json`. That value is a check, never a source.",
			"is a `fail` (production `not_changed`) naming both values: nothing is deployed"}},
	} {
		first := readsFirstSection(readBundleFile(t, "release", "steps", c.id+".md"))
		if first == "" {
			t.Fatalf("%s: no `## 开工前读什么、核对什么` section", c.id)
		}
		for _, w := range c.want {
			if !strings.Contains(first, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s: what it reads and checks first lacks %q:\n%s", c.id, w, first)
			}
		}
	}
	// (that the check renders into the deploy dispatch's prompt is
	// test/v2_9_release_direct_e2e_test.go's v29AssertDeployReadsFirst)
	for _, f := range []string{workflow.WorkflowFile, "README.md"} {
		wfText := readBundleFile(t, "release", f)
		for _, retired := range []string{"handoff_ref", "sha256", "evidence:", "notarized", "bar-run", "run_request", "run request"} {
			if strings.Contains(wfText, retired) {
				t.Fatalf("release %s still carries the retired %q", f, retired)
			}
		}
	}
	for _, step := range []string{"release", "verify", "deploy"} {
		text := readBundleFile(t, "release", "steps", step+".md")
		for _, retired := range []string{"pf bar-run", "bar_run", "command_evidence", "run_request", "run request", "needs_input"} {
			if strings.Contains(text, retired) {
				t.Errorf("release/%s still carries the retired notary text %q: the step runs its commands itself", step, retired)
			}
		}
	}

	rel := readBundleFile(t, "release", "steps", "release.md")
	for _, needle := range []string{
		"`.polyforge/release.sh`", "PF-RELEASE-AMBIGUOUS_OWNER", "--verify", "git fetch origin",
		"git ls-remote --tags origin", "verbatim", "artifacts/release-record.json",
		`set -o pipefail; cd repos/<owner> && .polyforge/release.sh [<bump>] 2>&1 | tee ../../artifacts/release.log`,
		"120 minutes", "heavy-run admission",
		// the preflight runs under the same PATH (the resume-path audit's
		// and the already-released guard's text is
		// test/release_adoption_contract_test.go's)
		"`.polyforge/deploy.sh --preflight` in that repo now", "under your own `PATH`, the command PATH the procedures run under",
	} {
		if !strings.Contains(rel, needle) {
			t.Fatalf("release step lost its contract text %q", needle)
		}
	}
	// (contract 3's output schema, whose verdict enum admitted no defer, is
	// gone: the zero-owner case is the step's own fail.)
	if !strings.Contains(strings.Join(strings.Fields(rel), " "), "A release WI without a procedure is an error, not a deferral: hand off `fail`") {
		t.Fatal("the release step must not defer: a release WI without a procedure is an error, its hand-off a fail")
	}

	ver := readBundleFile(t, "release", "steps", "verify.md")
	for _, needle := range []string{"release-record.json", "verified-version.json", "ls-remote", "merge-base --is-ancestor", "`.polyforge/release.sh --verify V` (under your own `PATH`, the daemon's command PATH,", "immutab", "gh release view", "run yourself"} {
		if !strings.Contains(ver, needle) {
			t.Fatalf("verify step lost its check text %q", needle)
		}
	}

	// (contract 3's steps.release.output.version — the release record's
	// version, a check never a source — is the read-first section's,
	// pinned above)
	dep := strings.Join(strings.Fields(readBundleFile(t, "release", "steps", "deploy.md")), " ")
	for _, needle := range []string{"`.polyforge/deploy.sh`", "--confirm V", "PF-DEPLOY-AMBIGUOUS_OWNER", `"latest"`, "rolled_back", "artifacts/verified-version.json",
		// contract 3's defer verdict: a project without a deploy procedure
		// hands off done, nothing deployed, and says so
		"Hand off `done`, the body saying no repo carries a deploy procedure, so nothing was deployed (production state `none`).",
		`set -o pipefail; cd repos/<owner> && .polyforge/deploy.sh V --confirm V 2>&1 | tee ../../artifacts/deploy.log`, "30 minutes", "heavy-run admission"} {
		if !strings.Contains(dep, needle) {
			t.Fatalf("deploy step lost its contract text %q", needle)
		}
	}
}

// readsFirstSection answers a step definition's `## 开工前读什么、核对什么`
// section (contract 4: what the step reads and checks before it starts),
// whitespace folded; "" when it has none.
func readsFirstSection(def string) string {
	i := strings.Index(def, "\n## 开工前读什么、核对什么")
	if i < 0 {
		return ""
	}
	sec := def[i+1:]
	if j := strings.Index(sec, "\n## "); j >= 0 {
		sec = sec[:j]
	}
	return strings.Join(strings.Fields(sec), " ")
}

// TestDeliveryBundlesCarryNoReleaseStep pins decision B: releases happen
// only via workflows/release.
func TestDeliveryBundlesCarryNoReleaseStep(t *testing.T) {
	for _, bundle := range []string{"develop", "micro"} {
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
