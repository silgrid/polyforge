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
	"docs/getting-started.md",
	"docs/extending.md",
	"docs/bundles.md",
	"docs/architecture.md",
	"docs/contributing.md",
	"docs/workstation.md",
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
	{"decision feed", `(?i)\bdecision\s+feed\b`}, // replaced the retired chat identity
	{"DaemonHost (Host)", `\bDaemonHost\b`},
	{"ProfileCell (Cell)", `\bProfileCell\b`},
	{"effective connection", `(?i)\beffective\s+connection\b`},
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

// TestW91GettingStartedDriftRedSide (polyforge#166 u4): the mainline page
// meets the same drift bar as every other doc-set page. The delivered page
// passes; a planted revision naming a pf verb the CLI does not have fails
// naming the drifted verb, and one carrying a relative link that resolves to
// no file fails naming the dead link. docDriftCheck is the one
// implementation (TestDocsSetNoDrift runs it over the real page), so no
// throwaway tree copy is needed here.
func TestW91GettingStartedDriftRedSide(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs")
	real, err := os.ReadFile(filepath.Join(dir, "getting-started.md"))
	if err != nil {
		t.Fatalf("polyforge#166: docs/getting-started.md is the drift check's subject and the repo must carry it: %v", err)
	}
	if _, _, _, _, bad := docDriftCheck(t, "docs/getting-started.md", dir, string(real)); len(bad) > 0 {
		t.Fatalf("the delivered mainline page must pass the drift check:\n%s", strings.Join(bad, "\n"))
	}
	if _, _, _, _, bad := docDriftCheck(t, "docs/getting-started.md", dir, string(real)+"\n\nRun `pf frobnicate` to recalibrate the sockets.\n"); len(bad) == 0 {
		t.Fatalf("the drift check must FAIL a mainline revision naming a pf verb that does not exist (pf frobnicate)")
	}
	if _, _, _, _, bad := docDriftCheck(t, "docs/getting-started.md", dir, string(real)+"\n\nRun `pf submit <WI_ID> --no-such-flag` to file it.\n"); len(bad) == 0 {
		t.Fatalf("the drift check must FAIL a mainline revision naming a pf flag that does not exist (--no-such-flag)")
	}
	if _, _, _, _, bad := docDriftCheck(t, "docs/getting-started.md", dir, string(real)+"\n\nSee [the missing handbook](no-such-doc-w91.md) for detail.\n"); len(bad) == 0 {
		t.Fatalf("the drift check must FAIL a mainline revision carrying a relative link that resolves to no file (no-such-doc-w91.md)")
	}
}

// TestW91MainlineWorkstationAnchors (polyforge#166 u4, review r1#4): the
// drift check strips a cross-file fragment, so it never proves the mainline
// page's workstation.md anchors land on real headings. Every
// `workstation.md#<frag>` the page names must slug-match an actual heading
// of docs/workstation.md — a renamed section would otherwise leave the
// newcomer with a dead link.
func TestW91MainlineWorkstationAnchors(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs")
	page, err := os.ReadFile(filepath.Join(dir, "getting-started.md"))
	if err != nil {
		t.Fatalf("polyforge#166: docs/getting-started.md is the mainline page: %v", err)
	}
	station, err := os.ReadFile(filepath.Join(dir, "workstation.md"))
	if err != nil {
		t.Fatalf("polyforge#166: docs/workstation.md is the anchor's owner: %v", err)
	}
	slugs := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(string(station), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		h := strings.TrimLeft(line, " \t")
		if !strings.HasPrefix(h, "#") {
			continue
		}
		slugs[guideSlug(strings.TrimSpace(strings.TrimLeft(h, "#")))] = true
	}
	frags := regexp.MustCompile(`workstation\.md#([A-Za-z0-9_-]+)`).FindAllStringSubmatch(string(page), -1)
	if len(frags) == 0 {
		t.Fatalf("polyforge#166: docs/getting-started.md links no workstation.md section — the mainline points the newcomer at the mechanism's owner")
	}
	for _, m := range frags {
		if !slugs[m[1]] {
			t.Fatalf("docs/getting-started.md's workstation.md#%s matches no heading of docs/workstation.md — the link is dead (the drift check drops cross-file fragments; this is the check that keeps them real)", m[1])
		}
	}
}

// TestDocsSetCoversEveryDocsGuide (polyforge#166 u4, review r1#2): the
// drift check's subject set is a promise about the real tree — every
// top-level docs/*.md page is a page of the doc set, or one of the
// exclusions that carries its own check. It reads the directory, not any
// source text, so a new top-level page that nothing registers is red here.
func TestDocsSetCoversEveryDocsGuide(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("polyforge#166: read docs/: %v", err)
	}
	listed := map[string]bool{}
	for _, p := range docsSetPages {
		listed[p] = true
	}
	// Pages deliberately outside the doc set: the append-only upgrade
	// history (sha256-pinned by TestW254UpgradeByteIdentical), the Chinese
	// routing page (not an English page) and the user guide (its own drift
	// check, TestUserGuideNoDrift).
	excluded := map[string]bool{
		"docs/upgrade.md":    true,
		"docs/zh.md":         true,
		"docs/user-guide.md": true,
	}
	var missing []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		rel := "docs/" + e.Name()
		if !listed[rel] && !excluded[rel] {
			missing = append(missing, rel)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("polyforge#166: every top-level docs page must be a docsSetPages page (drift-checked) or a named exclusion — unregistered: %s", strings.Join(missing, ", "))
	}
}
