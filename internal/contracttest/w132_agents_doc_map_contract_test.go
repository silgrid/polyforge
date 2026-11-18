package test

// carries: AGENTS.md, internal/contracttest/w132_agents_doc_map_contract_test.go

// w132_agents_doc_map_contract_test.go (polyforge#132, AC3): the repo's
// AGENTS.md names, in its coupled obligations, the change→document map —
// what a change alters maps to the documents that describe it, checked
// one document at a time: a document the change affects is modified in
// the same commit, one it does not gets an explicit none with the reason
// (the owner's Q2 ruling: a mandatory per-document check, never a
// blanket byte change nor a loose "have a look"). The four rows name the
// pf subcommands/flags/error codes, the conf and config keys, the step
// and skill behavior, and the pi-web files and routes; each points at a
// real path in this repository — the w111 shape: a rule that names its
// pin keeps it. The upgrade-history sentences stay as w111 and pf123
// pin them (their own tests keep those).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW132AgentsMDMapsChangesToDocuments(t *testing.T) {
	agents := w68Flat(t, "AGENTS.md")
	at := strings.Index(agents, "**Coupled obligations.**")
	if at < 0 {
		t.Fatal("AGENTS.md lost its coupled-obligations rule")
	}
	coupled := agents[at:]
	if i := strings.Index(coupled, "<!-- fork-only:begin -->"); i >= 0 {
		coupled = coupled[:i]
	}
	for _, want := range []string{
		// the map and its per-document strength (Q2)
		"what a change alters maps to the documents that describe it",
		"checked one document at a time",
		"a document the change affects is modified in the same commit",
		"one it does not gets an explicit none with the reason",
		// the four rows, one per change kind
		"`pf` subcommands, flags and error codes → `docs/reference/commands.md` and `skills/polyforge/verbs/<verb>.md`",
		"conf and config keys → `docs/reference/config.md`",
		"a step's or skill's behavior → `docs/reference/workflow-contract.md`, `docs/concepts.md` and `docs/bundles.md`",
		"a new pi-web file or route → the File Map in `pi-web/AGENTS.md`",
	} {
		if !strings.Contains(coupled, want) {
			t.Errorf("AGENTS.md coupled obligations must carry the change→document map (polyforge#132, AC3), missing %q", want)
		}
	}
	// the map points at real paths, not stale ones (the w111 shape: a
	// rule that names its pin keeps it)
	root := filepath.Join(v1Root(t))
	for _, rel := range []string{
		"docs/reference/commands.md",
		"docs/reference/config.md",
		"docs/reference/workflow-contract.md",
		"docs/concepts.md",
		"docs/bundles.md",
		"pi-web/AGENTS.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("the change→document map names %s, which must exist: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "polyforge", "verbs")); err != nil {
		t.Errorf("the change→document map names skills/polyforge/verbs/<verb>.md, whose directory must exist: %v", err)
	}
	// the File Map the pi-web row names is where pi-web/AGENTS.md keeps it
	piweb := w68Flat(t, "pi-web/AGENTS.md")
	if !strings.Contains(piweb, "## File Map") {
		t.Error("the map's pi-web row names the File Map, which pi-web/AGENTS.md must still carry")
	}
}
