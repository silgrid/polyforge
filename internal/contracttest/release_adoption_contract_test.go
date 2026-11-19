package test

// release_adoption_contract_test.go: a release step whose resolver finds its
// version's tag already on the green batch's pinned commit resumes it,
// whoever claimed it: a complete release
// (release.sh --verify passes) is adopted with its release page as the
// checkable notes and resumed: true, a partial one runs the procedure, which
// resumes it; two tags on the pin, or a tag a kept list lacks on another
// commit, refuse with PF-RELEASE-ALREADY_RELEASED at the resolver's own
// check, before the step hands anything to the procedure. verify accepts the
// tag in the pre-release list only for a resumed release.

import (
	"strings"
	"testing"
)

func TestReleaseAdoptionContract(t *testing.T) {
	release := w68Flat(t, "workflows/release/steps/release.md")
	for _, want := range []string{
		"A version is claimed once and never burned.",
		"**`resume`, `V`** - the pinned commit already carries exactly one release tag `V`, whoever claimed it",
		"never cut a second version of the pinned commit and never refuse it as already released",
		"run the read-only audit `.polyforge/release.sh --verify V` in the owner repo.",
		"**Exit zero - `V` is complete.** Do not run the procedure",
		"`notes` = its published release page URL, `https://<host>/<owner>/<repo>/releases/tag/<V>`",
		"`verify` audits it again under its existing checks",
		"**Nonzero - part of `V` is missing.** Continue with section 4 and record `resumed: true`",
		"`PF-RELEASE-INCOMPLETE`",
		"Two or more release tags on the owner pin, a release tag the kept pre-release list lacks on another commit, or a pin no longer on the default branch already refused at the resolver's own check",
		"`resumed` is `true` exactly when the resolver's decision was `resume`",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("release.md must state %q", want)
		}
	}
	// The already-released check (repo review 2026-10-16 V-6), before the
	// procedure runs: the resolver classifies the owner pin's release tags
	// (two or more refuse, and so does a tag a kept list lacks on another
	// commit); the kept list is never overwritten; a normal first run has
	// neither. (Moved from the driver's TestReleaseBundleShape, one
	// place per pinned text fact; polyforge#154 re-points it at the pin.)
	guard := strings.Index(release, "PF-RELEASE-ALREADY_RELEASED")
	for _, want := range []string{
		"Resume, never re-cut", "whoever claimed it", "Two or more release tags on the owner pin",
		"First record the pre-release list (item 7) when it does not exist yet - it then lists `V`",
		"a list an earlier attempt of this work item kept may lack it",
	} {
		if guard < 0 || !strings.Contains(release, want) {
			t.Errorf("release.md lost its already-released text %q (PF-RELEASE-ALREADY_RELEASED)", want)
		}
	}
	if run := strings.Index(release, "## 4. Run the procedure"); run < 0 || guard > run {
		t.Error("the already-released guard must come before the procedure runs")
	}
	for _, gone := range []string{"no capsule list - a hand release -", "**Adopt** a published tag only when ALL THREE hold"} {
		if strings.Contains(release, gone) {
			t.Errorf("release.md still carries the pre-resume rule %q", gone)
		}
	}
	verify := w68Flat(t, "workflows/release/steps/verify.md")
	if !strings.Contains(verify, "an adopted release's `notes` is its published release page URL") ||
		!strings.Contains(verify, "A notes location you cannot check mechanically is a `fail`, not a skip.") {
		t.Error("verify must check the adopted notes location and keep the uncheckable-notes failure")
	}
	// both shapes of a resumed V: absent from the list (this work item's own
	// earlier attempt recorded it, then claimed V), or present unchanged
	for _, want := range []string{
		"unless the record says `\"resumed\": true`",
		"A resumed `V` passes in either shape: absent from the list (an earlier attempt of THIS work item recorded the list, then claimed `V` and died)",
		"or present with exactly the current lines",
		"Present with other lines is a `fail`.",
	} {
		if !strings.Contains(verify, want) {
			t.Errorf("verify.md must state %q", want)
		}
	}
	if !strings.Contains(release, "`verify` accepts either shape only for a resumed release") {
		t.Error("release.md section 5 must say the list may lack or carry a resumed tag")
	}
	wf := w68Flat(t, "workflows/release/README.md")
	for _, want := range []string{
		"Retrying is a property of the `release` step, not a rewind",
		"On a pinned commit that already carries its release tag it RESUMES that version",
		"no second version of one pinned commit is ever cut",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("the release lane's README.md must state %q", want)
		}
	}
}
