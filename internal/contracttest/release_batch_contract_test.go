package test

// carries: workflows/release/workflow.yaml, workflows/release/steps/release.md, workflows/release/steps/verify.md, workflows/release/README.md, workflows/_shared/scripts/release.py, internal/contracttest/release_batch_contract_test.go

// release_batch_contract_test.go (polyforge#154): the release lane's object
// is the project's most recent green integrate batch - resolved by the
// lane's own command, refused with named codes before any version is
// claimed, handed to the procedure with --pins, recorded with the batch's
// id and whole pins, and audited by verify against the pins (never a branch
// head). The step texts carry each fact; the release bundle stays
// project-generic (no private procedure markers, its five files).

import (
	"regexp"
	"strings"
	"testing"
)

func TestReleaseBatchContract(t *testing.T) {
	rel := w68Flat(t, "workflows/release/steps/release.md")

	// the resolver: the lane's own command, its verdicts mapped
	for _, want := range []string{
		`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/release.py" --capsule "$(pwd)" --json --out artifacts/release-batch.json --pins-out artifacts/release-pins.json`,
		"the project's most recent **green integrate batch**",
		"verdict pass, decision `fresh`",
		"verdict pass, decision `resume`",
		"hand off `fail` with a `--reason` that begins with that code, BEFORE any version is claimed",
		"verdict error (exit 2)",
		"never guess a release object the resolver refused to resolve",
		"reads the project's batches through the step's own hub",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md must state %q", want)
		}
	}
	// the named refusals come before the procedure runs
	codes := strings.Index(rel, "`PF-RELEASE-NO_GREEN_BATCH`")
	for _, want := range []string{"`PF-RELEASE-ALREADY_RELEASED`", "`PF-RELEASE-PIN_OFF_DEFAULT`", "`PF-RELEASE-UNPINNED_OWNER`", "`PF-RELEASE-AMBIGUOUS_OWNER`"} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md must name the refusal code %q", want)
		}
	}
	run := strings.Index(rel, "## 4. Run the procedure")
	if codes < 0 || codes > run {
		t.Error("the named refusals must come before the procedure runs")
	}

	// the procedure contract probe: --help must declare --pins
	for _, want := range []string{
		"`.polyforge/release.sh --help | grep -q -- '--pins'`",
		"PF-RELEASE-PROCEDURE_CONTRACT",
		"an old convention script would read the pins file as a bump argument or ignore it and release a branch head",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md must state the procedure-contract probe %q", want)
		}
	}

	// the owner repo sits at its pin; the procedure receives the pins
	for _, want := range []string{
		"**Sit the owner repo at its pinned commit.**",
		"`git checkout --detach <pin>`",
		"`git rev-parse HEAD` equal to the pin",
		"a pinned commit the default branch has moved past still releases",
		"`set -o pipefail; cd repos/<owner> && .polyforge/release.sh [<bump>] --pins ../../artifacts/release-pins.json 2>&1 | tee ../../artifacts/release.log`",
		"the reported commit is not equal to the owner pin",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md must state %q", want)
		}
	}

	// the record: the batch and its whole pins ride it
	for _, want := range []string{
		`{"version": "...", "commit": "...", "default_branch": "...", "batch": "...", "pins": {...},`,
		"`batch` is the green batch's id and `pins` its whole pinned group",
		"`resumed` is `true` exactly when the resolver's decision was `resume`",
		"`verify` accepts either shape only for a resumed release",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md section 5 must state %q", want)
		}
	}

	// the resume branch: the pinned commit, whoever claimed it
	for _, want := range []string{
		"**`resume`, `V`** - the pinned commit already carries exactly one release tag `V`, whoever claimed it",
		"never cut a second version of the pinned commit and never refuse it as already released",
		"**Exit zero - `V` is complete.** Do not run the procedure",
		"`https://<host>/<owner>/<repo>/releases/tag/<V>`",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md's resume branch must state %q", want)
		}
	}

	// verify reads the pins, not a branch head
	ver := w68Flat(t, "workflows/release/steps/verify.md")
	for _, want := range []string{
		"- **(a) release commit and the batch pin.**",
		"`C` must equal the owner repo's pin of the recorded `pins`",
		"the release is the batch, not the head",
		"the record must parse as JSON and name `version`, `commit`, `batch` and `pins`",
	} {
		if !strings.Contains(ver, want) {
			t.Errorf("verify.md must state %q", want)
		}
	}

	// the step's hub whitelist: the read-only verbs the resolver needs
	wf := w68Raw(t, "workflows/release/workflow.yaml")
	if !strings.Contains(wf, "hub: { verbs: [get, list] }") {
		t.Error("the release step must declare its read-only hub verbs (the resolver lists the project's batches through the step's hub)")
	}
	if !strings.Contains(wf, "most recent green integrate batch") {
		t.Error("the workflow's description must name the release object (the most recent green integrate batch)")
	}

	// the lane README: the refusals and the new procedure contract
	readme := w68Flat(t, "workflows/release/README.md")
	for _, want := range []string{
		"### What the lane refuses before any version is claimed (polyforge#154)",
		"`.polyforge/release.sh [bump] --pins <file>`",
		"a script whose `--help` does not is refused `PF-RELEASE-PROCEDURE_CONTRACT` before it runs",
		"no second version of one pinned commit is ever cut",
		"On a pinned commit that already carries its release tag it RESUMES that version",
		"A capsule repo the green batch does not cover is not part of the release",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("the release lane's README.md must state %q", want)
		}
	}
	for _, old := range []string{
		"releases the origin default-branch head",
		"no second version of that head is ever cut",
		"sized for a procedure that runs the full test gate itself",
	} {
		if strings.Contains(readme, old) {
			t.Errorf("the release lane's README.md still carries the origin-head rule %q", old)
		}
	}

	// the bundle stays project-generic
	files := map[string]string{
		"workflows/release/workflow.yaml":    wf,
		"workflows/release/README.md":        w68Raw(t, "workflows/release/README.md"),
		"workflows/release/steps/release.md": w68Raw(t, "workflows/release/steps/release.md"),
		"workflows/release/steps/verify.md":  w68Raw(t, "workflows/release/steps/verify.md"),
		"workflows/release/steps/deploy.md":  w68Raw(t, "workflows/release/steps/deploy.md"),
	}
	if len(files) != 5 {
		t.Fatalf("the release bundle is five files (workflow.yaml, README, three steps), got %d", len(files))
	}
	for file, raw := range files {
		low := strings.ToLower(raw)
		for _, m := range []string{"aihub", "docker", "ghcr.io", "intranet", "checksum"} {
			if strings.Contains(low, m) {
				t.Errorf("%s carries the private marker %q", file, m)
			}
		}
	}
	if ip := regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+\b`).FindString(w68Raw(t, "workflows/release/README.md")); ip != "" {
		t.Errorf("the README names a concrete host %q", ip)
	}
}
