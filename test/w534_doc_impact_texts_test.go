package test

// w534_doc_impact_texts_test.go: the lane texts check the
// documents a change affects, by judgment — no new step, script or
// mechanical test. Whitespace-flattened, so a rewrap keeps them pinned.
//
//  1. TestW534ReviewChecksTheDocImpact — the shared review's one
//     **Stale docs.** item carries the doc-impact check: its condition, the
//     documents, the blocking rule and its classes, and the record in
//     artifacts/review.md; the review skill lists that record; the
//     concepts page no longer calls such a document non-blocking.
//  2. TestW534ReleaseChecksTheDocsSinceTheLastTag — the release step reads
//     the README, install and upgrade docs against the range since the
//     previous release tag, after the already-released check and before the
//     procedure, edits nothing and asks the owner on a mismatch.

import (
	"strings"
	"testing"
)

func TestW534ReviewChecksTheDocImpact(t *testing.T) {
	step := w68Flat(t, "workflows/_shared/steps/review.md")
	for _, want := range []string{
		"The doc-impact check: when the change alters user-visible behaviour, a configuration key, a command or a step definition, the documents that describe it change in the same diff",
		"the repository's README, its reference documentation, its upgrade notes (appended to, never rewritten, where the repository keeps them append-only) and its skill documents",
		"blocks, naming the document and what it misses",
		"(`project_rule` for a coupled document `AGENTS.md` names, `guarantee` for any other document the doc-impact check covers)",
		"Record in `artifacts/review.md` every document you compared.",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("the review checklist must state %q", want)
		}
	}
	if n := strings.Count(step, "**Stale docs.**"); n != 1 {
		t.Errorf("the doc-impact check must live in the one **Stale docs.** item, found %d", n)
	}
	if old := "`guarantee` for user-visible wording that contradicts the behaviour) Docs not updated with the code they describe."; strings.Contains(step, old) {
		t.Errorf("the old Stale docs wording must be gone (one fact, one place): %q", old)
	}

	skill := w68Flat(t, "workflows/_shared/skills/review.md")
	if want := "the documents the doc-impact check compared (the step's **Stale docs.** item)"; !strings.Contains(skill, want) {
		t.Errorf("the review skill's review.md contents must list %q", want)
	}

	concepts := w68Flat(t, "docs/concepts.md")
	if want := "A document a user-visible change leaves out of sync does block (the checklist's doc-impact check: `project_rule` for a coupling `AGENTS.md` names, `guarantee` for any other)"; !strings.Contains(concepts, want) {
		t.Errorf("the concepts page must state %q", want)
	}
	if old := "stale docs outside the couplings `AGENTS.md` names do not block"; strings.Contains(concepts, old) {
		t.Errorf("the concepts page must no longer state %q", old)
	}
}

func TestW534ReleaseChecksTheDocsSinceTheLastTag(t *testing.T) {
	rel := w68Flat(t, "workflows/release/steps/release.md")
	const item = "**The docs against the changes since the last release** (a normal first run only: a resumed `V` was cut already, so skip this item)"
	for _, want := range []string{
		item,
		"`git describe --tags --abbrev=0 --match 'v*' <origin_head>`",
		"`git log --first-parent --oneline <prev>..<origin_head>`",
		"with no earlier release tag, read the documents against the tree as it stands",
		"the repository's README, its install instructions and its upgrade notes agree with those changes and name no command or configuration key the range removed",
		"Edit no document.",
		"`pf step ask` the owner once",
		"calling the release off (`cancel`, citing the reply",
		"the docs check's `<prev>`, its range and the documents you read",
	} {
		if !strings.Contains(rel, want) {
			t.Errorf("release.md must state %q", want)
		}
	}
	guard := strings.Index(rel, "**Already released? Resume, never re-cut.**")
	docs := strings.Index(rel, item)
	run := strings.Index(rel, "## 4. Run the procedure")
	if guard < 0 || docs < 0 || run < 0 || guard > docs || docs > run {
		t.Errorf("the docs check must come after the already-released check and before the procedure (guard=%d docs=%d run=%d)", guard, docs, run)
	}

	ref := w68Flat(t, "docs/reference/release.md")
	if want := "on a first run, a read of the README, install and upgrade docs against the changes since the previous release tag"; !strings.Contains(ref, want) {
		t.Errorf("the release reference must state %q", want)
	}
}
