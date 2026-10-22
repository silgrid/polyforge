package test

// w254_docs_set_acceptance_test.go — polyforge#254 e2e acceptance
// scenarios for the AI-first documentation set (artifacts/spec.md,
// "## Requirements" / "## Acceptance criteria"): the README becomes a
// landing page; the moved content lives under docs/ (concepts, install,
// both Quickstarts, extending, bundles, architecture, reference/,
// contributing); upgrade.md stays byte-identical and is only routed to;
// the unattended-mode documentation states the behavior the code
// actually has; and the in-tree pointers (cli.go strings, skill verbs,
// the workflow.md lanes, the design reference) follow the moves.
// Authored against the spec alone, BEFORE the pages exist: every
// assertion below is red on the pre-implementation tree (the pages are
// absent, README still carries the moved sections, the pointers still
// name README) and turns green only on the delivered doc set.
//
// Assertions are on spec-pinned tokens and shapes — never on wording;
// the prose-level halves of the criteria (the code-diff boundary,
// corrections-with-evidence beyond the delivery note, intactness beyond
// the pinned content markers, the once-and-linked glossary discipline)
// are judged by review against the same spec
// (artifacts/e2e-scenarios.md). The docs-only nature of this work item
// shows in the scenario shape: the project's real interfaces here are
// the doc files themselves, the `pf help` surface that points into
// them, and the real workflow loader the bundle chapter's worked
// example must parse with — no daemon, no aihub, no harness is spawned.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w254Pages is the new/moved doc set (the spec's destinations; the page
// names are the plan's choice, pinned here so the acceptance bar is
// concrete). README.md is the landing page they hang off.
var w254Pages = []string{
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

// w254Root answers the repo root (this package's cwd is the repo's
// test/ dir).
func w254Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// w254Read answers a repo-relative file's body, failing on the named
// path.
func w254Read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w254Root(t), rel))
	if err != nil {
		t.Fatalf("polyforge#254: read %s: %v", rel, err)
	}
	return string(raw)
}

// w254Page answers one doc-set page's body, failing on the deliverable
// itself when absent or empty — the doc set is this work item's
// deliverable, and every page-content scenario's first assertion is
// that the page exists.
func w254Page(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w254Root(t), rel))
	if err != nil {
		t.Fatalf("polyforge#254: %s is the deliverable — the AI-first documentation set — and the repo must carry it: %v", rel, err)
	}
	if len(raw) == 0 {
		t.Fatalf("polyforge#254: %s is empty: a documentation page is a document, not a placeholder", rel)
	}
	return string(raw)
}

// w254Norm answers the whitespace-normalized body (a wrapped sentence
// still matches; test/w218_readme_docs_test.go's convention).
func w254Norm(body string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
}

// w254Fences answers the fenced-block contents of a Markdown body
// (``` … ``` lines exclusive): where a docs chapter presents its worked
// example files.
func w254Fences(body string) []string {
	var out []string
	in := false
	var cur []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if in {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, l)
		}
	}
	return out
}

// w254HeadingRe matches one Markdown heading line (any level); the
// prefix is the heading's '##…' text.
var w254HeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// w254Section scans the doc set for the first heading (any level) whose
// text matches headingRe and answers that section's body — from the
// heading line to the next heading of the same or a higher level — plus
// the page it lives on. The spec leaves the section's page to plan;
// wherever it lives, the doc set must carry it. A missing page is
// skipped (soft): the scan's failure mode is "no such section anywhere
// in the doc set", not "one page absent" (the page-existence scenarios
// carry that assertion).
func w254Section(t *testing.T, pages []string, headingRe *regexp.Regexp, what string) (string, string) {
	t.Helper()
	for _, p := range pages {
		raw, err := os.ReadFile(filepath.Join(w254Root(t), p))
		if err != nil {
			continue // a missing page is not this scan's failure mode
		}
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			m := w254HeadingRe.FindStringSubmatch(line)
			if m == nil || !headingRe.MatchString(m[2]) {
				continue
			}
			level := len(m[1])
			var body []string
			for _, next := range lines[i+1:] {
				if h := w254HeadingRe.FindStringSubmatch(next); h != nil && len(h[1]) <= level {
					break
				}
				body = append(body, next)
			}
			return p, strings.Join(body, "\n")
		}
	}
	t.Fatalf("polyforge#254: the doc set must carry %s — no heading of any new doc page matches it: the section is the deliverable, not a presentation choice", what)
	return "", ""
}

// w254UnattendedHeadingRe matches the unattended-mode section's heading.
var w254UnattendedHeadingRe = regexp.MustCompile(`(?i)unattended`)

// w254UnattendedSection answers the unattended-mode section (the spec:
// no question asked + the merge rule ⇒ auto-merge in autopilot, bug-fix
// and chore, never feature-full; the "never unattended" list as settled
// docs-only policy) — wherever in the new doc set it lives.
func w254UnattendedSection(t *testing.T) (string, string) {
	t.Helper()
	return w254Section(t, w254Pages, w254UnattendedHeadingRe, "the unattended-mode section (when a work item merges with no human in the loop, and the owner-confirmed never-unattended list)")
}

// TestW254ReadmeLandingPage: the README is a landing page — every moved
// section's heading is gone (the content lives under docs/, README
// keeps a pointer block, and no heading is kept merely to preserve an
// old anchor), and the landing page points at the whole doc set.
func TestW254ReadmeLandingPage(t *testing.T) {
	body := w254Read(t, "README.md")
	var leftover []string
	for _, h := range []string{
		"Platform support", "Install", "Quickstart", "Architecture",
		"Build & test", "`pf` commands", "`pf daemon` flags", "Extending",
		"`pf recall`", "Contributing & filing work items",
	} {
		if regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(h)).MatchString(body) {
			leftover = append(leftover, h)
		}
	}
	if len(leftover) > 0 {
		t.Fatalf("README.md is a landing page: the moved sections live under docs/ and README keeps pointers, not the sections — but the '##' headings of %s are still there (no heading may be kept merely to preserve an old anchor)", strings.Join(leftover, ", "))
	}
	norm := w254Norm(body)
	var missing []string
	for _, p := range []string{
		"docs/concepts.md", "docs/install.md", "docs/quickstart-developers.md",
		"docs/quickstart-macos.md", "docs/extending.md", "docs/bundles.md",
		"docs/architecture.md", "docs/reference", "docs/contributing.md",
		"docs/upgrade.md", "docs/user-guide.md",
	} {
		if !strings.Contains(norm, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("README.md is a landing page: its pointer block must reach the whole doc set — it never links %s", strings.Join(missing, ", "))
	}
	// no placeholder heading: a heading kept only to preserve an old
	// anchor carries no content under it.
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		m := w254HeadingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		level := len(m[1])
		empty := true
		for _, next := range lines[i+1:] {
			if h := w254HeadingRe.FindStringSubmatch(next); h != nil && len(h[1]) <= level {
				break
			}
			if strings.TrimSpace(next) != "" {
				empty = false
				break
			}
		}
		if empty {
			t.Fatalf("README.md keeps a placeholder heading (%q) — a heading with no content under it exists only to preserve an old anchor; the moved content's links point at its new home", m[2])
		}
	}
}

// TestW254ConceptsSixteenTerms: the concepts page — the newcomer manual
// doubling as glossary — defines every one of the sixteen grill-summary
// terms (capsule, work item, workflow/lane, memory, step, result
// delivery, project, workspace, machine/work machine, repo mapping,
// data plane vs execution plane (aihub vs daemon), bundle, gate,
// rewind, claim, chat identity) and carries the where-to-find-what
// mapping into the doc set.
func TestW254ConceptsSixteenTerms(t *testing.T) {
	norm := w254Norm(w254Page(t, "docs/concepts.md"))
	if !regexp.MustCompile(`(?i)where\s+to\s+find`).MatchString(norm) {
		t.Fatalf("docs/concepts.md must carry the where-to-find-what mapping into the doc set (which page answers which kind of question); it is absent")
	}
	var missing []string
	for _, c := range []struct{ term, re string }{
		{"capsule", `(?i)\bcapsule\b`},
		{"work item", `(?i)\bwork\s+item\b`},
		{"workflow (lane)", `(?i)\bworkflow\b`},
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
		{"aihub vs daemon", `(?i)\baihub\b`},
		{"bundle", `(?i)\bbundle\b`},
		{"gate", `(?i)\bgate\b`},
		{"rewind", `(?i)\brewind\b`},
		{"claim", `(?i)\bclaim\b`},
		{"chat identity", `(?i)\bchat\s+identity\b`},
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			missing = append(missing, c.term)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/concepts.md is the newcomer manual doubling as glossary and must define every term a newcomer meets — it never names: %s", strings.Join(missing, ", "))
	}
}

// TestW254UnattendedModeActualBehavior: the unattended-mode section
// states the behavior the code actually has — when no question is asked
// and the merge rule holds, the work item merges unattended in the
// autopilot, bug-fix and chore lanes, and never in feature-full (its
// grill-review requires a human answer) — not the original ticket's
// "choose autopilot to enable unattended" framing.
func TestW254UnattendedModeActualBehavior(t *testing.T) {
	page, sec := w254UnattendedSection(t)
	norm := w254Norm(sec)
	var missing []string
	for _, c := range []struct{ what, re string }{
		{"the no-question precondition", `(?i)no\s+question`},
		{"the merge rule", `(?i)\bmerge\b`},
		{"the autopilot lane merging unattended", `(?i)\bautopilot\b`},
		{"the bug-fix lane merging unattended", `(?i)\bbug-?fix\b`},
		{"the chore lane merging unattended", `(?i)\bchore\b`},
		{"feature-full never merging unattended", `(?i)feature-?full\b`},
		{"the never", `(?i)\bnever\b`},
		{"the human answer feature-full's grill-review requires", `(?i)\bhuman\b`},
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			missing = append(missing, c.what)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s's unattended-mode section must state the behavior the code actually has — no question + the merge rule ⇒ auto-merge in autopilot, bug-fix and chore, never feature-full (its grill-review requires a human answer) — it never states: %s", page, strings.Join(missing, ", "))
	}
}

// TestW254NeverUnattendedSettledPolicy: the unattended-mode section
// presents the owner-confirmed "never unattended" list (release/deploy,
// schema or data migration, credentials and permission surface,
// removing features or weakening guarantees, security-related changes,
// large cross-repo changes) as settled docs-only policy the code does
// not enforce — avoiding unattended merges is the user choosing the
// feature-full bundle — never as a draft awaiting confirmation.
func TestW254NeverUnattendedSettledPolicy(t *testing.T) {
	page, sec := w254UnattendedSection(t)
	norm := w254Norm(sec)
	var missing []string
	for _, c := range []struct{ what, re string }{
		{"release/deploy", `(?i)\brelease\b`},
		{"schema or data migration", `(?i)(\bschema\b|\bmigration\b)`},
		{"credentials and permission surface", `(?i)\bcredential`},
		{"removing features or weakening guarantees", `(?i)(\bremov|\bweaken)`},
		{"security-related changes", `(?i)\bsecur`},
		{"large cross-repo changes", `(?i)\bcross-?repo\b`},
		{"the list as policy (settled, not a draft)", `(?i)\bpolicy\b`},
		{"the honesty that the code does not enforce it", `(?i)\benforc`},
		{"feature-full as the way to avoid unattended merges", `(?i)feature-?full\b`},
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			missing = append(missing, c.what)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s's unattended-mode section must present the owner-confirmed never-unattended list as settled docs-only policy the code does not enforce (avoiding unattended merges is choosing the feature-full bundle) — it never covers: %s", page, strings.Join(missing, ", "))
	}
}

// TestW254ArchitectureOverview: the design docs gain a reader-facing
// architecture overview entry page: it lists the live design docs in a
// recommended reading order and marks archive/ as history, not
// authority.
func TestW254ArchitectureOverview(t *testing.T) {
	norm := w254Norm(w254Page(t, "docs/architecture.md"))
	if !regexp.MustCompile(`(?i)reading\s+order`).MatchString(norm) {
		t.Fatalf("docs/architecture.md is the design docs' entry page and must list the live docs in a recommended reading order; no reading order exists")
	}
	var missing []string
	for _, doc := range []string{
		"01-workflow-skills.md", "06-memory-search.md",
		"08-capsule-base-migration.md", "reference.md",
		"v0-lessons-ledger.md", "v2-converge.md", "wi-kind-contract.md",
	} {
		if !strings.Contains(norm, doc) {
			missing = append(missing, doc)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/architecture.md must list every live design doc in its reading order — it never names: %s", strings.Join(missing, ", "))
	}
	if !regexp.MustCompile(`(?i)archive\b.{0,400}?\b(history|authorit)`).MatchString(norm) {
		t.Fatalf("docs/architecture.md must mark docs/design/polyforge-vnext/archive/ as history, not authority — no archive mention pairs with history/authority within a sentence")
	}
}

// TestW254QuickstartsMovedIntact: both Quickstart guides — the developer
// from-zero smoke run and the macOS single-machine run — live complete
// under docs/ (no content deleted; the pinned markers are the survival
// evidence, the full intactness is review's diff judgment), and README
// keeps pointers to both.
func TestW254QuickstartsMovedIntact(t *testing.T) {
	dev := w254Norm(w254Page(t, "docs/quickstart-developers.md"))
	var missing []string
	for _, marker := range []string{
		"go build -o bin/aihub ./cmd/aihub",
		"go build -o bin/pf ./cmd/pf",
		"bootstrap-admin.key",
		"/tmp/pf-quickstart",
	} {
		if !strings.Contains(dev, marker) {
			missing = append(missing, marker)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/quickstart-developers.md is the moved developer from-zero smoke Quickstart — no content may be deleted in the move — but it never carries: %s", strings.Join(missing, ", "))
	}
	mac := w254Norm(w254Page(t, "docs/quickstart-macos.md"))
	missing = nil
	for _, marker := range []string{
		"aihub-darwin-arm64",
		"xattr -c",
		"codesign -f -s -",
		"tmux new-session -d -s aihub",
		"Killed: 9",
	} {
		if !strings.Contains(mac, marker) {
			missing = append(missing, marker)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/quickstart-macos.md is the moved macOS single-machine Quickstart — no content may be deleted in the move — but it never carries: %s", strings.Join(missing, ", "))
	}
	readme := w254Norm(w254Read(t, "README.md"))
	for _, p := range []string{"docs/quickstart-developers.md", "docs/quickstart-macos.md"} {
		if !strings.Contains(readme, p) {
			t.Fatalf("README.md keeps pointers to both moved Quickstarts — it never links %s", p)
		}
	}
}

// TestW254UpgradeByteIdentical: docs/upgrade.md — 1511 lines of
// per-version upgrade records — stays byte-identical (the sha256 of the
// pre-move file is pinned here) and is only routed to, from the install
// ops page and the README landing page.
func TestW254UpgradeByteIdentical(t *testing.T) {
	raw := []byte(w254Read(t, "docs/upgrade.md"))
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != "349dfe4b4a87be01d4a3adfed23b5366b1be21d6b45c051983e90d6605d760f9" {
		t.Fatalf("docs/upgrade.md must stay byte-identical (only routed to, never rewritten): sha256 %s != the pinned pre-move sha256", got)
	}
	install := w254Norm(w254Page(t, "docs/install.md"))
	if !strings.Contains(install, "upgrade.md") {
		t.Fatalf("docs/install.md is the ops page that routes upgrading — it must link docs/upgrade.md; no upgrade routing exists on the page")
	}
	if readme := w254Norm(w254Read(t, "README.md")); !strings.Contains(readme, "docs/upgrade.md") {
		t.Fatalf("the README landing page must link docs/upgrade.md (upgrade history stays put, routed from the landing page)")
	}
}

// TestW254ContributingGuide: the contributor guide lives under docs/,
// references AGENTS.md as the sole binding rule list (restating no
// rule), and exactly one pointer line is appended to the end of
// AGENTS.md pointing back at the guide.
func TestW254ContributingGuide(t *testing.T) {
	guide := w254Norm(w254Page(t, "docs/contributing.md"))
	if !strings.Contains(guide, "AGENTS.md") {
		t.Fatalf("docs/contributing.md must reference AGENTS.md as the sole binding rule list (the guide restates no rule); AGENTS.md is never named")
	}
	agents := w254Read(t, "AGENTS.md")
	if !strings.Contains(agents, "docs/contributing.md") {
		t.Fatalf("AGENTS.md must gain exactly one appended pointer line to the contributor guide (docs/contributing.md); no such pointer exists")
	}
}

// TestW254SkillSedimentationPointer: skills/polyforge/SKILL.md gains
// exactly one pointer line for sedimentation — when the user repeatedly
// walks the same flow, the agent is directed to read the docs'
// bundle-authoring chapter and propose sedimenting the flow into a
// custom bundle.
func TestW254SkillSedimentationPointer(t *testing.T) {
	skill := w254Norm(w254Read(t, "skills/polyforge/SKILL.md"))
	if !strings.Contains(skill, "docs/bundles.md") {
		t.Fatalf("skills/polyforge/SKILL.md must carry the sedimentation pointer — directing the agent to the docs' bundle-authoring chapter (docs/bundles.md) to propose sedimenting a repeatedly-walked flow into a custom bundle; no pointer to the chapter exists")
	}
	if !regexp.MustCompile(`(?i)sediment`).MatchString(skill) {
		t.Fatalf("skills/polyforge/SKILL.md's added pointer line is the sedimentation prompt — propose sedimenting the repeatedly-walked flow into a custom bundle; no sedimentation wording exists")
	}
}

// TestW254ReferencePages: the reference set exists — commands (indexing
// the authorities: `pf help` and the skill verbs, no second flag table),
// config (covering the real config surface: cli.toml, .polyforge.yaml,
// the workspace and daemon tables, models and memory_policy), UDS
// routes, failure codes, release/build-test.
func TestW254ReferencePages(t *testing.T) {
	for _, p := range []string{
		"docs/reference/commands.md", "docs/reference/config.md",
		"docs/reference/uds-routes.md", "docs/reference/failure-codes.md",
		"docs/reference/release.md",
	} {
		w254Page(t, p)
	}
	cmds := w254Norm(w254Page(t, "docs/reference/commands.md"))
	var missing []string
	for _, what := range []struct{ token, want string }{
		{"the `pf help` authority", "pf help"},
		{"the skill-verb authority", "skills/polyforge/verbs"},
	} {
		if !strings.Contains(cmds, what.want) {
			missing = append(missing, what.token)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/reference/commands.md indexes and points to the command-shape authorities — it never references: %s (no second hand-maintained flag table)", strings.Join(missing, ", "))
	}
	cfg := w254Norm(w254Page(t, "docs/reference/config.md"))
	missing = nil
	for _, what := range []struct{ token, want string }{
		{"the cli.toml surface", "cli.toml"},
		{"the .polyforge.yaml surface", ".polyforge.yaml"},
		{"the models key", "models"},
		{"the memory_policy key", "memory_policy"},
		{"the workspace mapping table", "workspace"},
		{"the daemon table", "daemon"},
	} {
		if !strings.Contains(cfg, what.want) {
			missing = append(missing, what.token)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/reference/config.md covers the real config surface — every cli.toml / .polyforge.yaml key, each once, in one place — it never covers: %s", strings.Join(missing, ", "))
	}
}

// w254PrivateIPRe matches an RFC1918 private IPv4 literal — the class
// of internal identifier the public-tree scrub removed; no doc example
// may carry one.
var w254PrivateIPRe = regexp.MustCompile(`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b`)

// TestW254NewDocsClean: every new/moved page (and the README landing
// page) is English-only, and no example carries an internal identifier
// (a private-network IP literal; the public tree stays clean).
func TestW254NewDocsClean(t *testing.T) {
	for _, p := range append(append([]string{"README.md"}, w254Pages...), "docs/user-guide.md") {
		body := w254Page(t, p)
		nonASCII, total := 0, 0
		for _, r := range body {
			total++
			if r > 0x7f {
				nonASCII++
			}
		}
		if nonASCII*4 > total {
			t.Fatalf("%s is English-only (the spec's non-goal: no bilingual pages); %d of %d runes are non-ASCII", p, nonASCII, total)
		}
		if m := w254PrivateIPRe.FindString(body); m != "" {
			t.Fatalf("%s carries an internal identifier — the private-network IP literal %q; the public tree stays clean (no internal hostnames, IPs or keys in any example)", p, m)
		}
	}
}

// TestW254InTreePointersFollowMoves: the in-tree surfaces that pointed
// at the moved README sections point at the new locations — the cli.go
// extension-point strings (`pf help extensions` names docs/extending.md),
// the skill's extensions fragment, the workflow.md lanes' daemon-flag
// pointer, and the design reference's contributing pointer.
func TestW254InTreePointersFollowMoves(t *testing.T) {
	root := w254Root(t)
	var stale []string
	if cli := w254Read(t, "internal/polyforge/cli/cli.go"); !strings.Contains(cli, "docs/extending.md") {
		stale = append(stale, "internal/polyforge/cli/cli.go's extension-point pointer strings still name no docs/extending.md location")
	}
	if ext := w254Read(t, "skills/polyforge/verbs/extensions.md"); !strings.Contains(ext, "docs/extending.md") {
		stale = append(stale, "skills/polyforge/verbs/extensions.md points at no docs/extending.md location")
	}
	lanes, err := filepath.Glob(filepath.Join(root, "workflows", "*", "workflow.md"))
	if err != nil {
		t.Fatalf("glob workflows/*/workflow.md: %v", err)
	}
	for _, lane := range lanes {
		if body, err := os.ReadFile(lane); err == nil && strings.Contains(string(body), "README, `pf daemon` flags") {
			stale = append(stale, filepath.Join("workflows", filepath.Base(filepath.Dir(lane)), "workflow.md")+" still points its daemon-flag budget at README")
		}
	}
	if ref := w254Read(t, "docs/design/polyforge-vnext/reference.md"); !strings.Contains(ref, "docs/contributing.md") {
		stale = append(stale, "docs/design/polyforge-vnext/reference.md's change-coverage pointer still names no docs/contributing.md location")
	}
	if len(stale) > 0 {
		t.Fatalf("every in-tree inbound link must follow the moved content to its new location — stale pointers:\n%s", strings.Join(stale, "\n"))
	}
	// the wired surface, driven for real: `pf help extensions` — the
	// discovery chain chat → skill → help → docs — names the new
	// extension-point home.
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "pf"), "./cmd/pf")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pf for the wired-surface check: %v: %s", err, out)
	}
	out, err := exec.Command(filepath.Join(bin, "pf"), "help", "extensions").Output()
	if err != nil {
		t.Fatalf("pf help extensions: %v", err)
	}
	if !strings.Contains(string(out), "docs/extending.md") {
		t.Fatalf("`pf help extensions` must point its extension topics at docs/extending.md (the wired discovery surface follows the move); its output names no such location:\n%s", out)
	}
}

// TestW254BundleExampleLoaderParsed: the bundle-authoring chapter's
// worked example cannot drift from the actual bundle format: the
// chapter presents the example's files as fenced blocks — the
// workflow.md as a frontmatter-bearing block declaring steps, each
// step it names as a frontmatter-bearing block whose name: is the step
// id — and the real workflow loader parses and renders every one of
// them.
func TestW254BundleExampleLoaderParsed(t *testing.T) {
	fences := w254Fences(w254Page(t, "docs/bundles.md"))
	if len(fences) == 0 {
		t.Fatalf("docs/bundles.md is the bundle-authoring chapter and must present its worked example in fenced blocks; the page carries none")
	}
	stepRe := regexp.MustCompile(`(?m)^steps:`)
	nameRe := func(id string) *regexp.Regexp {
		return regexp.MustCompile(`(?m)^name:\s*` + regexp.QuoteMeta(id) + `\s*$`)
	}
	placeholderRe := regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

	// the workflow.md-shaped fences: frontmatter-bearing blocks that
	// declare steps — the real loader must parse each.
	var stepIDs []string
	seen := map[string]bool{}
	workflowShaped := 0
	for i, f := range fences {
		front, _, ok := workflow.SplitFrontmatter([]byte(f))
		if !ok || !stepRe.MatchString(string(front)) {
			continue
		}
		workflowShaped++
		tmp := filepath.Join(t.TempDir(), "workflow.md")
		if err := os.WriteFile(tmp, []byte(f), 0o644); err != nil {
			t.Fatalf("plant the documented workflow.md example for the real loader: %v", err)
		}
		fm, err := workflow.ReadFrontmatter(tmp)
		if err != nil {
			t.Fatalf("docs/bundles.md's worked example (fenced block %d) is the real format's workflow.md and must parse with the real workflow loader — it does not: %v", i+1, err)
		}
		if fm == nil || len(fm.Steps) == 0 {
			t.Fatalf("docs/bundles.md's worked example (fenced block %d) presents a workflow.md but parses to no steps — the example has drifted from the actual format", i+1)
		}
		for _, s := range fm.Steps {
			if s.ID == "" {
				t.Fatalf("docs/bundles.md's worked example (fenced block %d) names a step entry with no id", i+1)
			}
			if !seen[s.ID] {
				seen[s.ID] = true
				stepIDs = append(stepIDs, s.ID)
			}
		}
	}
	if workflowShaped == 0 {
		t.Fatalf("docs/bundles.md's worked example must present the custom bundle's workflow.md — a fenced block whose frontmatter declares steps — so the real loader can parse it; no such block exists")
	}

	// each step the example names needs its steps/<id>.md fence, and
	// the real loader must render it (placeholders satisfied by the
	// scan; {{principles}} renders from the shared stub).
	root := t.TempDir()
	bundle := filepath.Join(root, "workflows", "w254-documented-example")
	shared := filepath.Join(root, "workflows", workflow.SharedDir)
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatalf("plant the documented example's bundle dir: %v", err)
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatalf("plant the documented example's shared dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shared, workflow.PrinciplesFile), []byte("w254 stub principles\n"), 0o644); err != nil {
		t.Fatalf("plant the shared principles stub: %v", err)
	}
	for _, id := range stepIDs {
		var def string
		for _, f := range fences {
			front, _, ok := workflow.SplitFrontmatter([]byte(f))
			if ok && nameRe(id).MatchString(string(front)) {
				def = f
				break
			}
		}
		if def == "" {
			t.Fatalf("docs/bundles.md's worked example names step %s but presents no fenced step definition for it (a frontmatter-bearing block whose name: is %s) — the example has drifted from the actual format", id, id)
		}
		p := filepath.Join(bundle, "steps", id+".md")
		if err := os.WriteFile(p, []byte(def), 0o644); err != nil {
			t.Fatalf("plant the documented step definition for %s: %v", id, err)
		}
		vars := map[string]string{}
		for _, m := range placeholderRe.FindAllStringSubmatch(def, -1) {
			if m[1] == workflow.PrinciplesVar {
				continue // the loader renders it from _shared/principles.md
			}
			vars[m[1]] = "w254-example-value"
		}
		if _, err := workflow.ReadStepDefinition(bundle, id, vars); err != nil {
			t.Fatalf("docs/bundles.md's documented step definition for %s must render with the real workflow loader — it does not: %v", id, err)
		}
	}
}
