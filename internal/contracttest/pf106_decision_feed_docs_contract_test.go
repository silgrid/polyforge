// carries: docs/reference/uds-routes.md, docs/concepts.md, docs/reference/commands.md, skills/polyforge/verbs/watch.md, skills/polyforge/verbs/task.md

package test

// pf106_decision_feed_docs_contract_test.go (polyforge#106, AC9): a text
// contract. The task entries' auto-resolve rules are documented once, in
// English, in the pages the doc-set drift check reads: a background
// task's failed / capped / interrupted entry still counts as needing the
// owner but resolves on its own once the ticket it served no longer
// needs it — a later terminal of the same ticket (a rerun that
// completed), the ticket's end or confirmed analysis read at aihub by
// the entry's own Cell (wrapped, canceled, failed, CORE-404, content
// without the unconfirmed marker), or the entry passing three days of
// age (every Cell's alike, no aihub read) — always written resolved,
// never dismissed. The uds-routes reference also names the `wi` an
// analysis task's entry carries; the commands reference and the two
// skill fragments state the same end in their own words.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPF106DecisionFeedDocsNameTaskAutoResolve(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("%s must exist: %v", rel, err)
		}
		return string(raw)
	}
	flat := func(rel string) string { return strings.Join(strings.Fields(read(rel)), " ") }
	udsRoutes := flat(filepath.Join("docs", "reference", "uds-routes.md"))
	concepts := flat(filepath.Join("docs", "concepts.md"))
	commands := flat(filepath.Join("docs", "reference", "commands.md"))
	watchMD := flat(filepath.Join("skills", "polyforge", "verbs", "watch.md"))
	taskMD := flat(filepath.Join("skills", "polyforge", "verbs", "task.md"))

	for _, tc := range []struct {
		what, doc string
		pins      []string
	}{
		{
			what: "the uds-routes reference's GET /decisions row",
			doc:  udsRoutes,
			pins: []string{
				// the entry names the ticket a daemon-scheduled analysis served
				"carries the ticket's `wi`",
				// the ticket signals, read at the entry's own Cell
				"a rerun that completed",
				"wrapped, canceled, failed, CORE-404",
				"confirmed content (no unconfirmed marker)",
				// the age bound, Cell-independent, no hub read
				"older than three days (every Cell's alike, no aihub read)",
			},
		},
		{
			what: "the concepts page's Decision feed section",
			doc:  concepts,
			pins: []string{
				"resolves on its own once the ticket it served no longer needs it",
				"older than three days",
				// the semantics: the daemon writes resolved, never dismissed
				"resolved, never dismissed",
			},
		},
		{
			what: "the commands reference's task report paragraph",
			doc:  commands,
			pins: []string{
				"That attention ends on its own",
				"three days",
				"written `resolved`, never `dismissed`",
			},
		},
		{
			what: "the watch verb's auto-resolve rules",
			doc:  watchMD,
			pins: []string{
				"a later terminal of the same ticket",
				"three days",
				"never `dismissed`",
			},
		},
		{
			what: "the task verb's presentation section",
			doc:  taskMD,
			pins: []string{
				"a later terminal of the ticket",
				"three days",
				"`resolved`, never `dismissed`",
			},
		},
	} {
		for _, want := range tc.pins {
			if !strings.Contains(tc.doc, want) {
				t.Errorf("%s must name %q — the task entries' auto-resolve rules (polyforge#106, AC9)", tc.what, want)
			}
		}
	}
}
