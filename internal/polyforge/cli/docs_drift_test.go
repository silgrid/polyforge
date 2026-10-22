package cli

// docs_drift_test.go: the drift check of user_guide_drift_test.go
// (docDriftCheck), extended to every new/moved page of polyforge#254's
// AI-first documentation set — the explicit page list below. Per page:
// it exists and is non-empty; it is English-only (the w241 guide's
// assertion pattern); every `pf <verb>` and `--flag` it names in a code
// span or fenced block exists in the real surface (the same sets
// docDriftSets builds — the commands registry, the flag sets this
// package parses); and every relative link resolves. docs/concepts.md
// additionally carries all sixteen grill-summary terms (the
// required-topic check: the concepts page is the newcomer manual, a
// page that lost its terms would pass every other check vacuously).
// No AI cold-read evaluation harness: this check is mechanical.
//
// docs/user-guide.md keeps its own test (TestUserGuideNoDrift, with
// its sanity floors); README.md is the landing page (w254's acceptance
// scenarios pin it); docs/extending.md's subsection names are pinned by
// help_extensions_test.go; the design docs are pinned by make xrefs.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docsSetPages is the explicit page list: every page polyforge#254
// added to or moved into docs/ (the plan's names, the spec's
// destinations). A page renamed or added without this list noticing is
// a finding here waiting to happen — add it to the list.
var docsSetPages = []string{
	"docs/concepts.md",
	"docs/install.md",
	"docs/quickstart-developers.md",
	"docs/quickstart-macos.md",
	"docs/extending.md",
	"docs/bundles.md",
	"docs/architecture.md",
	"docs/contributing.md",
	"docs/reference/commands.md",
	"docs/reference/config.md",
	"docs/reference/uds-routes.md",
	"docs/reference/failure-codes.md",
	"docs/reference/release.md",
}

// docsSetRoot is the repo root (this package's cwd sits three levels
// below it).
func docsSetRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// TestDocsSetNoDrift: every page of the doc set exists, is non-empty,
// is English-only, names only real pf verbs and flags, and carries
// only resolving links (docDriftCheck — the same bar the user guide
// meets).
func TestDocsSetNoDrift(t *testing.T) {
	root := docsSetRoot(t)
	for _, p := range docsSetPages {
		t.Run(p, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, p))
			if err != nil {
				t.Fatalf("polyforge#254's doc set must carry %s: %v", p, err)
			}
			if len(raw) == 0 {
				t.Fatalf("%s is empty: a documentation page is a document, not a placeholder", p)
			}
			body := string(raw)
			// English-only (the w241 guide's assertion pattern).
			nonASCII, total := 0, 0
			for _, r := range body {
				total++
				if r > 0x7f {
					nonASCII++
				}
			}
			if nonASCII*4 > total {
				t.Fatalf("%s is English-only (no bilingual pages); %d of %d runes are non-ASCII", p, nonASCII, total)
			}
			// verbs, flags, links — the same check the guide meets.
			dir := filepath.Dir(filepath.Join(root, p))
			_, _, _, _, bad := docDriftCheck(t, p, dir, body)
			if len(bad) > 0 {
				t.Fatalf("%s drifted from the pf surface:\n%s", p, strings.Join(bad, "\n"))
			}
		})
	}
}

// docsConceptsTerms are the sixteen grill-summary terms
// (artifacts/spec.md's newcomer-manual requirement): the concepts page
// must define every one — the regexes match the term (or its compound)
// on the whitespace-normalized body.
var docsConceptsTerms = []struct{ term, re string }{
	{"capsule", `(?i)\bcapsule\b`},
	{"work item", `(?i)\bwork\s+item\b`},
	{"workflow", `(?i)\bworkflow\b`},
	{"lane", `(?i)\blane\b`},
	{"memory", `(?i)\bmemory\b`},
	{"step", `(?i)\bstep\b`},
	{"result delivery", `(?i)\bresult\s+delivery\b`},
	{"project", `(?i)\bproject\b`},
	{"workspace", `(?i)\bworkspace\b`},
	{"machine (work machine)", `(?i)\bmachine\b`},
	{"repo mapping", `(?i)\brepo\s+mapping\b`},
	{"data plane", `(?i)\bdata\s+plane\b`},
	{"execution plane", `(?i)\bexecution\s+plane\b`},
	{"aihub (vs daemon)", `(?i)\baihub\b`},
	{"bundle", `(?i)\bbundle\b`},
	{"gate", `(?i)\bgate\b`},
	{"rewind", `(?i)\brewind\b`},
	{"claim", `(?i)\bclaim\b`},
	{"chat identity", `(?i)\bchat\s+identity\b`},
}

// TestDocsConceptsCarriesSixteenTerms: the required-topic check —
// docs/concepts.md, the newcomer manual doubling as glossary, defines
// every term a newcomer meets, and carries the where-to-find-what
// mapping into the doc set.
func TestDocsConceptsCarriesSixteenTerms(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(docsSetRoot(t), "docs", "concepts.md"))
	if err != nil {
		t.Fatalf("read docs/concepts.md: %v", err)
	}
	norm := regexp.MustCompile(`\s+`).ReplaceAllString(string(raw), " ")
	if !regexp.MustCompile(`(?i)where\s+to\s+find`).MatchString(norm) {
		t.Fatal("docs/concepts.md must carry the where-to-find-what mapping into the doc set; it is absent")
	}
	var missing []string
	for _, c := range docsConceptsTerms {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			missing = append(missing, c.term)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/concepts.md is the newcomer manual doubling as glossary and must define every term a newcomer meets — it never names: %s", strings.Join(missing, ", "))
	}
}
