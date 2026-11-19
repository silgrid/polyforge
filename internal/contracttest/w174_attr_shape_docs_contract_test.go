package test

// carries: skills/polyforge/verbs/file.md, skills/polyforge/verbs/queue.md, docs/reference/commands.md, docs/design/polyforge-vnext/reference.md, internal/contracttest/w174_attr_shape_docs_contract_test.go

// w174_attr_shape_docs_contract_test.go (polyforge#174, AC4): the four
// doc faces carry the ONE attrs shape this change settles — a dotted
// `--attr` key nests as the path it names, the same dot path `pf queue
// --attr` filters on, and a create never stores a flat dotted key. The
// pins are scoped to the passages that state it (pf118 r1#2's lesson:
// whole-file pins pass on base text), and the split section's
// pf118-pinned tokens must survive the edit (checked by the pf118
// tests, run beside this one).

import (
	"strings"
	"testing"
)

// TestW174AttrShapeDocsContract pins the one-shape statement across the
// skill and reference faces.
func TestW174AttrShapeDocsContract(t *testing.T) {
	fileRaw := w68Raw(t, "skills/polyforge/verbs/file.md")

	// file.md — the create flag: the path form and the nesting it means.
	flags := pf118Bullet(t, fileRaw, "`--attr <path>=json-value`")
	for _, want := range []string{"a dotted path nests", "pf queue --attr"} {
		if !strings.Contains(flags, want) {
			t.Errorf("the file create --attr flag line must state %q (the path form and the filter that reads it back): %s", want, flags)
		}
	}

	// file.md — the split section: the create's stored shape IS the
	// check's filter shape, with the concrete JSON form.
	split := pf118Section(t, fileRaw, "### Splitting a ticket into an epic", "## ")
	for _, want := range []string{
		"the nested dot paths they name",
		`{"split":{"origin":"<id>"}}`,
		"one shape, write and read",
	} {
		if !strings.Contains(split, want) {
			t.Errorf("the split section of skills/polyforge/verbs/file.md must state %q — the stored shape the duplicate check reads (polyforge#174 AC4)", want)
		}
	}

	// file.md — the failure table: the CORE-400 row names the dotted-key
	// refusal.
	core := pf118Bullet(t, fileRaw, "| `CORE-400` | Invalid field or type")
	if !strings.Contains(core, "carries a dot") {
		t.Errorf("the file verb's CORE-400 row must name the top-level dotted-key refusal (polyforge#174 AC4): %s", core)
	}

	// queue.md — the filter face states the same convention.
	queue := w68Flat(t, "skills/polyforge/verbs/queue.md")
	for _, want := range []string{"the nested path it names", "one shape, write and read"} {
		if !strings.Contains(queue, want) {
			t.Errorf("skills/polyforge/verbs/queue.md must state %q — the filter reads the shape the create writes (polyforge#174 AC4)", want)
		}
	}

	// commands.md — the index carries the one-shape section with the
	// concrete example and the skill links (no flag table duplicated).
	commands := w68Raw(t, "docs/reference/commands.md")
	for _, want := range []string{
		"## `pf file create` / `pf queue` — one `--attr` shape",
		"`--attr split.origin=X`",
		"skills/polyforge/verbs/file.md",
		"skills/polyforge/verbs/queue.md",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md must carry %q — the one attrs shape, one place, routed to the skills (polyforge#174 AC4)", want)
		}
	}

	// reference.md — the design contract: the create boundary refuses a
	// top-level dotted key, the detail spells the nested path.
	refRaw := w68Raw(t, "docs/design/polyforge-vnext/reference.md")
	s23 := pf118Section(t, refRaw, "### 2.3 attrs", "### ")
	for _, want := range []string{"CORE-400", "carries a dot", "the nested path it names"} {
		if !strings.Contains(s23, want) {
			t.Errorf("docs/design/polyforge-vnext/reference.md §2.3 must state %q — the create boundary of the one attrs shape (polyforge#174 AC4)", want)
		}
	}
	create := pf118Bullet(t, refRaw, "| — → queued |")
	if !strings.Contains(create, "CORE-400") || !strings.Contains(create, "carries a dot") {
		t.Errorf("reference.md's create row must name the dotted-key CORE-400 refusal (polyforge#174 AC4): %s", create)
	}
}
