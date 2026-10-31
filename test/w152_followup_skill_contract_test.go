package test

// w152_followup_skill_contract_test.go: text contract for the follow-up
// management fragment, skills/polyforge/verbs/followup.md.
//
// The fragment is the one source of the follow-up lifecycle (ingest,
// present, claim, dismiss, dedupe) and of its AIHub doc.track ledger;
// verbs/watch.md and verbs/wrap.md only point at it, SKILL.md routes the
// follow-up intents to it, and the design docs name it. Red on the
// pre-change tree: the fragment does not exist and watch/wrap carry the
// presentation rules themselves.
//
// Honesty label: a skill is prose and no test executes the session agent.
// The worked example's fixture outcomes (3 ingested rows with a source,
// present lists only unclaimed rows, a claim back-link, a dismissal reason,
// a dedupe pointer to the first record, a re-ingest that stores nothing)
// are pinned here as the contract the fragment states, by marker tokens
// rather than prose; the live walkthrough on real deliveries is observed
// after merge. The one mechanical part is the fingerprint: w152Fingerprint
// implements the fragment's ordered rule, and the fixture fingerprints the
// fragment states are computed by it, not hand-copied.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

const w152Fragment = "skills/polyforge/verbs/followup.md"

// w152PresentOnlyClause is the operative presentation clause that used to
// be maintained twice (watch.md and wrap.md); the fragment owns it now.
const w152PresentOnlyClause = "leave deciding, claiming, filing, routing or dismissing any follow-up to the human"

func w152Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	// whitespace-normalized so a re-wrap does not break the pins
	return strings.Join(strings.Fields(string(b)), " ")
}

// w152Section returns the body of the "## <name>" section (up to the next
// "## " heading) of the whitespace-normalized text, or "" when absent.
func w152Section(text, name string) string {
	_, after, ok := strings.Cut(text, " ## "+name+" ")
	if !ok {
		return ""
	}
	if i := strings.Index(after, " ## "); i >= 0 {
		return after[:i]
	}
	return after
}

func TestW152FollowUpFragmentContract(t *testing.T) {
	frag := w152Read(t, w152Fragment)

	sections := map[string][]string{
		"Ingest":  {"artifacts/delivery.md", "never assume the list is empty", "unclaimed", "A re-ingest that adds no new row writes no record at all"},
		"Present": {"--tags followup", "--sort updated", "--after", "updated_at", "`claimed` or `dismissed`", "severity"},
		"Claim": {"Only when the owner names the item", "verbs/file.md", "state=claimed", "claim=<new slug>",
			"the owning row's `claim`", "the crash window", "never a second ticket"},
		"Dismiss": {"Only when the owner names the item", "state=dismissed", "no reason given"},
		"Dedupe": {"--any", "dedupe→", "owning", "never to a pointer record",
			"`follow-up ledger: <the very source>`) is not a dedupe hit", "never writes a pointer row to itself", "nothing is stored",
			"already recorded in the ledger is existing, not new"},
	}
	for name, wants := range sections {
		body := w152Section(frag, name)
		if body == "" {
			t.Errorf("%s lacks the `## %s` lifecycle section", w152Fragment, name)
			continue
		}
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s `## %s` does not state %q", w152Fragment, name, w)
			}
		}
	}

	// the ledger schema: the carrier, its attribution and the row shape
	for _, w := range []string{
		"--type doc.track", "--work-item", "--tags followup", "--title 'follow-up ledger: <project>#<seq>'",
		"| fp | item | severity | state | claim | note |",
		"`unclaimed`, `claimed`, `dismissed`", "unrated", "appends a superseding record",
		"`MEM-403`", "Never fall back silently",
	} {
		if !strings.Contains(frag, w) {
			t.Errorf("%s does not carry the ledger schema marker %q", w152Fragment, w)
		}
	}

	// the iron law survives the move
	law := w152Section(frag, "The iron law")
	for _, w := range []string{"Never auto-file", "never auto-claim", "never auto-dismiss", "A ledger memory is not a ticket"} {
		if !strings.Contains(law, w) {
			t.Errorf("%s `## The iron law` does not state %q", w152Fragment, w)
		}
	}

	// the behavioral oracle as fixture outcomes of the worked example
	ex := w152Section(frag, "Worked example")
	if ex == "" {
		t.Fatalf("%s lacks the `## Worked example` section", w152Fragment)
	}
	for _, w := range []string{
		"Fixtures, not real data",
		"lists 3 items", "3 rows, each `unclaimed`", "`source: demo#41`", // ingest
		"Present** shows 3 items",                                       // present
		"`state=claimed`, `claim=demo#43`", "Present now shows 2 items", // claim + back-link
		"`state=dismissed`, `note=the table is generated`", "Present now shows 1 item", // dismiss
		"`dedupe→ mem_FIXTURE1`", "never a second row", // dedupe to the first record
		"skipped as self-title", "no new row, no pointer row, nothing stored", // re-ingest
	} {
		if !strings.Contains(ex, w) {
			t.Errorf("%s worked example lacks the fixture outcome %q", w152Fragment, w)
		}
	}

	// the fingerprint rule is stated as ordered steps, lowercase before the
	// label strip, and every fixture fingerprint is computed by the same
	// rule: changing the stated order, a fixture text or a fixture value on
	// one side only turns this red.
	order := []string{
		"the order is normative", "Drop a leading list marker", "Lowercase the whole text",
		"`f<N>` label", "`<project>#<seq>` reference",
		"not a letter, a digit or whitespace", "Split on whitespace", "Join the first 8 words with `-`",
	}
	last := -1
	for _, w := range order {
		i := strings.Index(frag, w)
		if i < 0 {
			t.Errorf("%s does not state the fingerprint step %q", w152Fragment, w)
			continue
		}
		if i < last {
			t.Errorf("%s states the fingerprint step %q out of order", w152Fragment, w)
		}
		last = i
	}
	for _, fx := range w152FingerprintFixtures {
		if got := w152Fingerprint(fx.item); got != fx.fp {
			t.Errorf("fingerprint(%q) = %q, fixture says %q", fx.item, got, fx.fp)
		}
		if want := "`" + fx.item + "` → `" + fx.fp + "`"; !strings.Contains(ex, want) {
			t.Errorf("%s worked example does not state the fingerprint fixture %q", w152Fragment, want)
		}
	}

	// watch/wrap point at the fragment and no longer carry the rules
	total := strings.Count(frag, w152PresentOnlyClause)
	for _, rel := range []string{"skills/polyforge/verbs/watch.md", "skills/polyforge/verbs/wrap.md"} {
		s := w152Read(t, rel)
		if !strings.Contains(s, "`verbs/followup.md`") {
			t.Errorf("%s does not point at verbs/followup.md", rel)
		}
		if !strings.Contains(s, "never assume the list is empty") {
			t.Errorf("%s dropped the never-assume-empty duty", rel)
		}
		total += strings.Count(s, w152PresentOnlyClause)
	}
	if total != 1 {
		t.Errorf("the present-only clause %q appears %d times across followup/watch/wrap; the fragment must own it exactly once", w152PresentOnlyClause, total)
	}

	// the routing table and the design docs name the fragment
	skill := w152Read(t, "skills/polyforge/SKILL.md")
	skillRow := regexp.MustCompile("\\| [^|]*follow-up[^|]* \\| `verbs/followup\\.md`")
	if !skillRow.MatchString(skill) {
		t.Errorf("skills/polyforge/SKILL.md has no intent-table row routing follow-up intents to verbs/followup.md")
	}
	// the frontmatter description (the only text a session loads
	// unconditionally) enumerates the fragment
	if _, desc, ok := strings.Cut(skill, " description: "); !ok || !strings.Contains(strings.SplitN(desc, " license: ", 2)[0], "follow-up ledger (`followup.md`)") {
		t.Errorf("skills/polyforge/SKILL.md frontmatter description does not enumerate the follow-up ledger fragment followup.md")
	}
	for _, rel := range []string{
		"docs/design/polyforge-vnext/01-workflow-skills.md",
		"docs/design/polyforge-vnext/reference.md",
	} {
		if !strings.Contains(w152Read(t, rel), "`followup.md`") {
			t.Errorf("%s does not name followup.md in its routing-skill fragment list", rel)
		}
	}
}

// w152FingerprintFixtures cover a leading list marker, an F<N> label in
// both cases, a project#seq prefix, punctuation and the 8-word cut. Each
// pair is stated verbatim in the fragment's worked example.
var w152FingerprintFixtures = []struct{ item, fp string }{
	{"F1 retry budget is not logged", "retry-budget-is-not-logged"},
	{"F3 flaky e2e on slow hosts", "flaky-e2e-on-slow-hosts"},
	{"- demo#42 Retry budget, is NOT logged!", "retry-budget-is-not-logged"},
	{"1. f2: doc table misses the new --flag (see the generated table)", "doc-table-misses-the-new-flag-see-the"},
}

var (
	w152ListMarker = regexp.MustCompile(`^\s*(?:[-*+]|[0-9]+[.)])\s+`)
	w152Label      = regexp.MustCompile(`^f[0-9]+\pP*$`)
	w152Reference  = regexp.MustCompile(`^[a-z0-9._-]+#[0-9]+\pP*$`)
)

// w152Fingerprint is the reference implementation of the fragment's
// ordered fingerprint rule (steps 1-7 of the `fp` bullet).
func w152Fingerprint(item string) string {
	s := w152ListMarker.ReplaceAllString(item, "") // 2
	s = strings.ToLower(s)                         // 3
	words := strings.Fields(s)
	for len(words) > 0 && (w152Label.MatchString(words[0]) || w152Reference.MatchString(words[0])) {
		words = words[1:] // 4
	}
	s = strings.Map(func(r rune) rune { // 5
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return r
		}
		return -1
	}, strings.Join(words, " "))
	words = strings.Fields(s) // 6
	if len(words) > 8 {
		words = words[:8]
	}
	return strings.Join(words, "-") // 7
}

// TestW152FollowUpSkillStaysProjectionClean: the new fragment and this
// test are projected to the public mirror, so neither may carry the
// internal identifier families the projection's invariant refuses, and the
// fragment routes no repo path without the registry rule. The patterns are
// assembled from pieces so this file does not match itself.
func TestW152FollowUpSkillStaysProjectionClean(t *testing.T) {
	inv := regexp.MustCompile(strings.Join([]string{
		"GMI" + "SWE", "ieops" + "-", "the internal project" + "#", "gmi#[0-9]",
		"work" + "bench#[0-9]", "work" + "bench-[0-9]", "[wW][bB][0-9]+",
	}, "|"))
	for _, rel := range []string{w152Fragment, "test/w152_followup_skill_contract_test.go"} {
		b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
		if err != nil {
			t.Fatal(err)
		}
		if m := inv.FindString(string(b)); m != "" {
			t.Errorf("%s carries the internal identifier %q the projection refuses", rel, m)
		}
	}
	frag := w152Read(t, w152Fragment)
	if strings.Contains(frag, "repos/") && !strings.Contains(frag, "registry") {
		t.Errorf("%s routes repo paths without naming the aihub registry", w152Fragment)
	}
	// non-vacuous: the scan read the real fragment
	if !strings.Contains(frag, "# followup") {
		t.Fatalf("%s is not the follow-up fragment", w152Fragment)
	}
}
