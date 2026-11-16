// carries: docs/design/polyforge-vnext/wi-kind-contract.md, docs/design/polyforge-vnext/v2-converge.md, docs/design/polyforge-vnext/reference.md, skills/polyforge/verbs/file.md, skills/polyforge/verbs/wrap.md, skills/polyforge/verbs/queue.md, skills/polyforge/verbs/submit.md, docs/reference/commands.md, docs/upgrade.md

package test

// w77_bound_parent_contract_test.go (polyforge#77, AC7): a text
// contract — the acceptance is the documentation itself. polyforge#77
// deleted the w140 symmetric rule (PARENT_NOT_REGISTRY at create,
// HAS_CHILDREN_NO_BUNDLE at a PATCH bind): a bundle-bound ticket may be
// a parent, and a ticket with children may bind a bundle. A bound parent
// waits for every child to merge (only a wrapped child releases; the
// claim is refused DEPENDENCY_BLOCKED, waiting_for names the child with
// via "child"); cycle detection walks that wait (DEPENDENCY_CYCLE); a
// child is detached by clearing parent_id (never re-parented; a running
// child is refused CHILD_RUNNING; the write records wi_detached); a
// claimed child freezes the parent's bundle (PARENT_BUNDLE_FROZEN). The
// design documents carry it in English (w256), the skills and the
// commands reference carry the CLI faces (the WAITING-FOR child entry,
// the 需要交互：子票失败 mark and child_failed, the via-split guidance),
// and docs/upgrade.md gains a section: deploy aihub first, and the new
// rollback limit. Each pin below is a sentence the change writes; the
// stale list names the deleted rule's words, which no surface may keep.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w77Raw reads a repo file as it is (the section cut needs its line breaks).
func w77Raw(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// w77Section answers the flattened text of doc from heading to the next
// level-two heading.
func w77Section(doc, heading string) string {
	i := strings.Index(doc, heading)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return strings.Join(strings.Fields(rest), " ")
}

func w77Want(t *testing.T, rel, text string, wants []string, why string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s must carry %q — %s", rel, w, why)
		}
	}
}

func w77Stale(t *testing.T, rel, text string, stale []string) {
	t.Helper()
	for _, s := range stale {
		if strings.Contains(text, s) {
			t.Errorf("%s still carries %q — polyforge#77 deleted the registry-only parent rule (a bundle-bound ticket may be a parent)", rel, s)
		}
	}
}

func TestW77BoundParentContract(t *testing.T) {
	oldRule := []string{"PARENT_NOT_REGISTRY", "HAS_CHILDREN_NO_BUNDLE"}

	// The interface contract (English, w256): §2–§4.
	const kindRel = "docs/design/polyforge-vnext/wi-kind-contract.md"
	kind := w68Flat(t, kindRel)
	w77Want(t, kindRel, kind, []string{
		"a bundle-bound ticket may be a parent",
		"a ticket with children may bind a bundle",
		"`PARENT_DEPTH`",
		"waits for its children to merge",
		"only a wrapped child releases",
		"`DEPENDENCY_BLOCKED`",
		`"via": "child"`,
		"a child added later joins the wait",
		"`DEPENDENCY_CYCLE`",
		"`CHILD_RUNNING`",
		"never re-parented",
		"`wi_detached`",
		"`PARENT_BUNDLE_FROZEN`",
		"`OPEN_CHILDREN`",
		"`NOT_CLAIMABLE`",
		"deploy the new aihub before the new pf",
		"the old aihub loses the wait",
	}, "the bound-parent rules of polyforge#77 (§2–§4)")
	w77Stale(t, kindRel, kind, append(oldRule,
		"no longer usable as a parent",
		"rollback is just rolling back the aihub binary",
		`never shows "has children ∧ bundle non-empty"`))

	// The ruling record: v2-converge §12 marks the symmetric rule superseded.
	const convRel = "docs/design/polyforge-vnext/v2-converge.md"
	conv := w68Flat(t, convRel)
	sec12 := w77Section(w77Raw(t, convRel), "## 12. polyforge#140 rulings")
	if sec12 == "" {
		t.Fatalf("%s lost its §12 heading", convRel)
	}
	w77Want(t, convRel+" §12", sec12, []string{
		"superseded by polyforge#77",
		"a bundle-bound parent waits for its children",
		"`wi-kind-contract.md#§2`",
	}, "§12 registers that the symmetric rule is gone")
	w77Stale(t, convRel+" §12", sec12, append(oldRule,
		"A bundle lands only on leaves",
		"an epic/parent ticket is always a registry ticket",
		"exactly one side succeeds"))
	w77Stale(t, convRel, conv, oldRule)

	// The aihub reference: §2.2, the PATCH row, the code table.
	const refRel = "docs/design/polyforge-vnext/reference.md"
	ref := w68Flat(t, refRel)
	w77Want(t, refRel, ref, []string{
		"a bundle-bound parent waits for its unwrapped children",
		"`parent_id` clear-only",
		"`PARENT_BUNDLE_FROZEN`",
		"CHILD_RUNNING",
	}, "the reference names the bound-parent wait, the detach and the frozen bundle")
	w77Stale(t, refRel, ref, oldRule)

	// The filing skill: the detach form, the rule, the failure rows.
	const fileRel = "skills/polyforge/verbs/file.md"
	file := w68Flat(t, fileRel)
	w77Want(t, fileRel, file, []string{
		`pf file edit <child> --parent ""`,
		"re-parenting is not supported",
		"A bundle-bound parent waits for its children",
		"| `CORE-409` `CHILD_RUNNING` |",
		"| `CORE-400` `PARENT_BUNDLE_FROZEN` |",
		"| `CORE-409` `DEPENDENCY_CYCLE` |",
	}, "the detach flag, the bound-parent rule and its refusals")
	w77Stale(t, fileRel, file, []string{"a registration ticket or an epic runs nothing", "a registration ticket or an epic |"})

	// The wrap skill: an epic is no longer bundle-less by definition.
	const wrapRel = "skills/polyforge/verbs/wrap.md"
	wrap := w68Flat(t, wrapRel)
	w77Want(t, wrapRel, wrap, []string{
		"an unbound parent only once all its children are terminal",
		"a bundle-bound parent",
	}, "wrap's hand close covers the unbound parent only")
	w77Stale(t, wrapRel, wrap, []string{"and an epic only once all its children are terminal"})

	// The queue skill: the child entry and the child-failed mark.
	const queueRel = "skills/polyforge/verbs/queue.md"
	queue := w68Flat(t, queueRel)
	w77Want(t, queueRel, queue, []string{
		"(child, <status>)",
		"需要交互：子票失败",
		"`\"child_failed\": true`",
		`pf file edit <child> --parent ""`,
	}, "WAITING-FOR names a child by via, and a failed or canceled child marks its parent")

	// The submit skill: DEPENDENCY_BLOCKED guidance by via.
	const submitRel = "skills/polyforge/verbs/submit.md"
	submit := w68Flat(t, submitRel)
	w77Want(t, submitRel, submit, []string{
		`or detach the child: pf file edit <child> --parent ""`,
		"a child of a bundle-bound parent",
	}, "a refused claim's guidance follows each waiting_for entry's via")

	// The commands reference: the queue's marks.
	const cmdRel = "docs/reference/commands.md"
	cmd := w68Flat(t, cmdRel)
	w77Want(t, cmdRel, cmd, []string{
		"需要交互：子票失败",
		"\"child_failed\": true",
		`pf file edit <child> --parent ""`,
	}, "the reference documents the child-failed mark beside the other interaction marks")

	// The upgrade note: the appended section.
	const upRel = "docs/upgrade.md"
	up := w77Section(w77Raw(t, upRel), "## v0.2.1, a bundle-bound ticket can be a parent")
	if up == "" {
		t.Fatalf("%s must end with the section %q (polyforge#77)", upRel, "## v0.2.1, a bundle-bound ticket can be a parent")
	}
	w77Want(t, upRel, up, []string{
		"Deploy the new aihub first",
		"then upgrade the pf clients",
		"Rollback",
		"loses the wait",
		"PARENT_NOT_REGISTRY",
		"HAS_CHILDREN_NO_BUNDLE",
	}, "the deploy order and the rollback limit")
}
