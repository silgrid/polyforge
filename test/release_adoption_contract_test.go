package test

// release_adoption_contract_test.go: a re-dispatched release adopts a
// published tag only under the three conservative conditions, records
// the release page URL as its checkable notes, and otherwise still
// refuses with PF-RELEASE-ALREADY_RELEASED.

import (
	"strings"
	"testing"
)

func TestReleaseAdoptionContract(t *testing.T) {
	release := w68Flat(t, "workflows/release/steps/release.md")
	for _, want := range []string{
		"**Adopt** a published tag only when ALL THREE hold: a release tag is on `origin_head`",
		"this capsule's `artifacts/pre-release-tags.txt` exists",
		"that tag is absent from the file",
		"`notes` = its published release page URL, `https://<host>/<owner>/<repo>/releases/tag/<V>`",
		"so `verify` audits it under its existing checks",
		"`PF-RELEASE-ALREADY_RELEASED`",
		"no capsule list - a hand release -, the tag already in the list, or the tag not on `origin_head` never adopt",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("release.md must state %q", want)
		}
	}
	verify := w68Flat(t, "workflows/release/steps/verify.md")
	if !strings.Contains(verify, "an adopted release's `notes` is its published release page URL") ||
		!strings.Contains(verify, "A notes location you cannot check mechanically is a `fail`, not a skip.") {
		t.Error("verify must check the adopted notes location and keep the uncheckable-notes failure")
	}
}
