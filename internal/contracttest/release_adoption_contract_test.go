package test

// release_adoption_contract_test.go: a release step that finds its version's
// tag already on origin's head resumes it: a complete release
// (release.sh --verify passes) is adopted with its release page as the
// checkable notes and resumed: true, a partial one runs the procedure, which
// resumes it; only two tags on the head, or a tag a kept list lacks on another
// commit, still refuse with PF-RELEASE-ALREADY_RELEASED (two tags: a `fail`
// hand-off whose body names every one). verify accepts the
// tag in the pre-release list only for a resumed release.

import (
	"strings"
	"testing"
)

func TestReleaseAdoptionContract(t *testing.T) {
	release := w68Flat(t, "workflows/release/steps/release.md")
	for _, want := range []string{
		"A version is claimed once and never burned.",
		"**Exactly one, `V`** - resume `V`; never cut a second version of this head and never refuse it as already released.",
		"run the read-only audit `.polyforge/release.sh --verify V` in the owner repo.",
		"**Exit zero - `V` is complete.** Do not run the procedure",
		"`notes` = its published release page URL, `https://<host>/<owner>/<repo>/releases/tag/<V>`",
		"`verify` audits it again under its existing checks",
		"**Nonzero - part of `V` is missing.** Continue with section 4 and record `resumed: true`",
		"`PF-RELEASE-INCOMPLETE`",
		"hand off `fail` with a `--reason` that begins with `PF-RELEASE-ALREADY_RELEASED` and a body that names every one",
		"`resumed` is `true` exactly when section 3 found the version's tag already on `origin_head`",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("release.md must state %q", want)
		}
	}
	// The already-released check (repo review 2026-10-16 V-6), before the
	// procedure runs: the tag is a release tag on origin_head; two or more
	// refuse, and so does a tag a kept list lacks on another commit; the
	// kept list is never overwritten; a normal first run has neither.
	// (Moved from the driver's TestReleaseBundleShape, one
	// place per pinned text fact.)
	guard := strings.Index(release, "PF-RELEASE-ALREADY_RELEASED")
	for _, want := range []string{
		"Resume, never re-cut", "name starts with `v`", "equals `origin_head`", "**Two or more**",
		"never overwrite it", "NOT on `origin_head`", "comm -13",
		"A normal first run has no release tag on `origin_head` and no earlier list",
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
		"no second version of that head is ever cut",
		"On a head that already carries its release tag it RESUMES that version",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("the release lane's README.md must state %q", want)
		}
	}
}
