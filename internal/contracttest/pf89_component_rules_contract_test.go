// carries: docs/reference/config.md, docs/reference/commands.md, internal/polyforge/daemon/analysis_prompt.md

package test

// pf89_component_rules_contract_test.go (polyforge#89, AC4): the
// epic-upgrade rule's configuration and its tool documented once, in
// English, in the pages the doc-set drift check reads — the config
// reference's verify-scope key table carries the component= declaration
// and the epic_* thresholds with the ticket's defaults (3 / 8 / 30) and
// the component definition, the commands reference carries the
// components.py tool and how the pre-filing analysis attaches its
// output, and every place that names the defaults names the same set
// (the template names them beside; the tool's behaviour values are
// pinned by the workflow's own components tests against the same
// ticket's numbers).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPF89ComponentRulesTextsContract(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
		return string(raw)
	}
	configMD := read(filepath.Join("docs", "reference", "config.md"))
	commandsMD := read(filepath.Join("docs", "reference", "commands.md"))
	promptMD := read(filepath.Join("internal", "polyforge", "daemon", "analysis_prompt.md"))

	// The config reference's key table: the declaration, the three
	// thresholds, the defaults and the reader split (the engine reads
	// none of these keys).
	for _, want := range []string{
		"`component`",
		"`<name>\\|<path>[,<path>…]`",
		"`epic_components`",
		"`epic_units`",
		"`epic_files`",
		"the longest prefix",
		"the first declaration wins",
		"accepts the line without the unknown-key diagnostic",
	} {
		if !strings.Contains(configMD, want) {
			t.Errorf("docs/reference/config.md must carry %q — the epic-rule conf keys in the verify-scope table (AC4)", want)
		}
	}

	// The component definition: the generic division of a repository,
	// its nests, its exclusions and its documentation marking.
	for _, want := range []string{
		"nested engine root",
		"each nest at its outermost",
		"first-level directory",
		"the root files forming one component",
		"testdata, node_modules or vendor",
		"listed but never counted",
	} {
		if !strings.Contains(configMD, want) {
			t.Errorf("docs/reference/config.md must carry %q — the component definition (AC4)", want)
		}
	}

	// The commands reference: the tool itself and the analysis's use
	// of it.
	for _, want := range []string{
		"`components.py --repo DIR [--rev REV] [--json]`",
		"the epic-upgrade analysis's component division",
		"origin/HEAD (else HEAD) commit tree",
		"the component division of the ticket's repositories",
		"answers one fallback line and the dispatch proceeds",
	} {
		if !strings.Contains(commandsMD, want) {
			t.Errorf("docs/reference/commands.md must carry %q — the tool and the analysis's use of it (AC4)", want)
		}
	}

	// One value set everywhere the defaults are named: the config
	// reference and the analysis prompt template state the same 3 / 8 / 30
	// (the tool's behaviour values are pinned to the same numbers by the
	// workflow's own tests).
	for _, doc := range []struct {
		rel  string
		body string
	}{
		{"docs/reference/config.md", configMD},
		{"internal/polyforge/daemon/analysis_prompt.md", promptMD},
	} {
		for _, want := range []string{"3 / 8 / 30"} {
			if !strings.Contains(doc.body, want) {
				t.Errorf("%s must state the epic-rule defaults %s — every place that names them names the same set (AC4)", doc.rel, want)
			}
		}
	}
}
