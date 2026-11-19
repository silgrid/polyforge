package test

// w254_docs_set_acceptance_test.go — polyforge#254 e2e acceptance
// scenarios for the AI-first documentation set (artifacts/spec.md,
// "## Requirements" / "## Acceptance criteria"): the README becomes a
// landing page; the moved content lives under docs/ (concepts, install,
// both Quickstarts, extending, bundles, architecture, reference/,
// contributing); upgrade.md stays byte-identical and is only routed to;
// the unattended-mode documentation states the behavior the code
// actually has; and the in-tree pointers (cli.go strings, skill verbs,
// the lanes' README.md narratives, the design reference) follow the moves.
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
// the doc files themselves and the `pf help` surface that points into
// them — no daemon, no aihub, no harness is spawned. Two guarantees
// live in their packages instead (polyforge#255, one fact one place):
// the sixteen glossary terms in internal/polyforge/cli
// (TestDocsConceptsCarriesSixteenTerms) and the bundle chapter's worked
// example through the real loader in internal/polyforge/driver
// (TestDocsBundleExampleLoadsThroughTheDaemonPath).

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
// no question asked + the merge rule ⇒ auto-merge in the delivery lanes —
// develop and micro since the four older ones were removed; the
// "never unattended" list as settled docs-only policy) — wherever in the
// new doc set it lives.
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

// w254Sentences answers the section's statements: each bullet or
// paragraph, emphasis markers dropped and whitespace normalized, split
// into sentences — the unit a framing must hold within, so a doc that
// names the right tokens in the wrong framing (e.g. "feature-full
// merges unattended") does not pass.
func w254Sentences(sec string) []string {
	var units []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			units = append(units, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, l := range strings.Split(sec, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
			flush()
			continue
		case strings.HasPrefix(l, "- "), w254HeadingRe.MatchString(l):
			flush()
		}
		cur = append(cur, l)
	}
	flush()
	end := regexp.MustCompile(`[.!?]\s+`)
	var out []string
	for _, u := range units {
		u = w254Norm(strings.ReplaceAll(u, "**", ""))
		out = append(out, end.Split(u, -1)...)
	}
	return out
}

// w254Framed reports whether one sentence of sentences matches every
// one of res (and, when not is non-nil, does not match not).
func w254Framed(sentences []string, not *regexp.Regexp, res ...string) bool {
	for _, s := range sentences {
		if not != nil && not.MatchString(s) {
			continue
		}
		all := true
		for _, re := range res {
			if !regexp.MustCompile(re).MatchString(s) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// TestW254UnattendedModeActualBehavior: the unattended-mode section
// states the behavior the code actually has — when no question is asked
// and the merge rule holds, the work item merges unattended in the develop
// and micro lanes (the four lanes it named before were removed),
// unattended merging is every delivery lane's default and
// only a human stop that fires keeps a human in the loop; and micro has no
// declared checkpoint, but any step may still ask a human (pf ask), and fix
// asks the owner to re-file an out-of-bounds change. Each framing
// must hold within one sentence, not as tokens scattered over the section.
func TestW254UnattendedModeActualBehavior(t *testing.T) {
	page, sec := w254UnattendedSection(t)
	sentences := w254Sentences(sec)
	negated := regexp.MustCompile(`(?i)\b(never|not|no\s+longer)\b`)
	var missing []string
	for _, c := range []struct {
		what string
		not  *regexp.Regexp
		res  []string
	}{
		{"no question asked + the merge rule holding ⇒ the work item merges unattended", nil,
			[]string{`(?i)no\s+question`, `(?i)merge\s+rule`, `(?i)\bmerges?\s+unattended\b`}},
		{"the develop and micro lanes merging unattended", negated,
			[]string{`(?i)\bdevelop\b`, `(?i)\bmicro\b`, `(?i)\bmerges?\s+unattended\b`}},
		{"unattended merging as every delivery lane's default, only a human stop keeping a human in the loop", nil,
			[]string{`(?i)\bevery\s+delivery\s+lane\b`, `(?i)\bdefault\b`, `(?i)\bhuman\s+stop\b`}},
		{"micro having no declared checkpoint, while any step may still ask a human", nil,
			[]string{`(?i)\bmicro\b`, `(?i)\bno\s+declared\s+checkpoint\b`, `(?i)\bany\s+step\s+may\s+still\s+ask\b`}},
		// workflow contract 2 (change 10): micro's steps ask the owner
		// themselves — no resolved-conflict-review; out of bounds is fix's
		// question (workflow contract 4: the ask is `pf step ask`)
		{"the sync agent asking the owner (pf step ask) when unsure of a conflict resolution", nil,
			[]string{`(?i)\bsync\b`, "`pf step ask`", `(?i)\bconflict\s+resolution\b`}},
		{"fix asking the owner to re-file an out-of-bounds change on develop and cancelling on the owner's word", nil,
			[]string{`(?i)\bfix\b`, `(?i)\bre-file\b`, `(?i)\bdevelop\b`, `(?i)\bcancels?\b`}},
	} {
		if !w254Framed(sentences, c.not, c.res...) {
			missing = append(missing, c.what)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s's unattended-mode section must state the behavior the code actually has, each framing within one sentence — no question + the merge rule ⇒ auto-merge in develop and micro, every delivery lane's default unless a human stop fires; micro declares no checkpoint, but any step may still ask — no sentence states: %s", page, strings.Join(missing, "; "))
	}
}

// TestW254NeverUnattendedSettledPolicy: the unattended-mode section
// presents the owner-confirmed "never unattended" list (release/deploy,
// schema or data migration, credentials and permission surface,
// removing features or weakening guarantees, security-related changes,
// large cross-repo changes) as settled docs-only policy the code does
// not enforce — stated within one sentence — and avoiding unattended
// merges as the filer putting the work item on develop with
// `confirm_plan: true` (the feature-full bundle it named before was
// removed), never as a draft awaiting confirmation.
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
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			missing = append(missing, c.what)
		}
	}
	sentences := w254Sentences(sec)
	if !w254Framed(sentences, nil, `(?i)\bsettled\b`, `(?i)\bpolicy\b`, `(?i)\b(does\s+not|doesn't|never)\s+enforce`) {
		missing = append(missing, "the list as settled policy the code does not enforce, within one sentence")
	}
	if !w254Framed(sentences, nil, `(?i)\bavoid\b`, `(?i)\bunattended\s+merge`, `(?i)\bdevelop\b`, "`confirm_plan: true`") {
		missing = append(missing, "filing on develop with `confirm_plan: true` as the way to avoid an unattended merge, within one sentence")
	}
	if len(missing) > 0 {
		t.Fatalf("%s's unattended-mode section must present the owner-confirmed never-unattended list as settled docs-only policy the code does not enforce (avoiding unattended merges is filing on develop with `confirm_plan: true`) — it never covers: %s", page, strings.Join(missing, "; "))
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
		"go build -o bin/pf ./cmd/pf",
		"bin/pf aihub --data-dir",
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
		"pf-darwin-arm64 aihub --data-dir",
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

// TestW254UpgradeByteIdentical: docs/upgrade.md — per-version upgrade
// records, append-only history — stays byte-identical: the sha256 of the
// file is pinned here, and the list between the two marker lines below
// names what the pinned bytes carry, one line per appended section, and
// nothing else. The file starts at the first public release, v0.2.0: it was
// replaced once, by owner ruling, when the public line began, and has been
// append-only from that section on. Each append to docs/upgrade.md adds one line at the end of
// the list and moves the pin, in the same commit (the append-only rule,
// spec R14: docs change with the code, and the pin with the
// docs); the daemon's mechanical merge (develop's sync and merge steps)
// unions two lines appended at once (.polyforge/integrate.conf, list=).
// upgrade-sections:begin
// - the first public release's section (v0.2.0, workflow contract 1; the move from v0.1.x: drain, the aihub read-cursor split SQL, the installer and Pi Web, the per-Cell data dir migration, pf config migrate and stale per-step keys, the removed lanes and the user-lane rewrite)
// - the v0.2.1 section (the project deactivation status column; the SQL a v0.2.0 database needs, applied by the deploy's own switch window)
// - the second v0.2.1 section (the pre-submit analysis check: queued develop and micro tickets need `## 分析` or a dated 免分析 before `pf submit` or the auto-claim takes them; corrects the one above — the projects.status migration has no startup check, requests touching the missing column fail with SQLite errors)
// - the v0.2.1 section appended with the scope engine's Python rewrite (every host-run engine needs python3, and the light e2e prefix and the replay's not-runnable text become the repository's e2e_start_prefix= and not_runnable_rule= conf keys)
// - the v0.2.1 section appended with the integrate full gate running every command (the full-gate step's gate and --triage lines pass --keep-going, so one red batch records every failing command; gate.py's default and the develop and micro lanes' verification are unchanged)
// - the v0.2.1 section appended with units already red before the change (facts.py build --replay understands pre_existing_red: true in units.json: no test_commit, the red evidence is a replay on the tree of the change's base, and a run green there fails the unit — review classes that test_weakened)
// - the v0.2.1 section appended with pf step ask answering -h/--help itself (the flag-less ask used to post the literal "--help" to the owner as the question; -h/--help now print ask's usage locally with exit 0 and no call, a first word starting with "-" is refused before any call — the lone "-" stdin marker excepted — and the audit of the other step sub-actions found no second case)
// - the v0.2.1 section appended with the gate falling back to the lane's scope engine (a repo without its own verify-scope or tools/gate-id is gated by the lane's copies and can carry only the conf and the Dockerfile, the sources recorded in the result and the green tag, the fallback's plan naming <engine>)
// - the v0.2.1 section appended with the develop merge done by a person (steps.<project>.merge.by: human; the merge step asks the owner over the PRs, merge.py takes the person's GitHub landing by the patch-id fingerprint with exit 3 while it is not landed, and the landing record's merge_commits name each landed commit with its merge flag so revert.py rolls a squash or a rebase landing back)
// - the v0.2.2 section appended with the deploy configuration following the work item's Cell (every step process carries PF_CELL, PF_CELL_SERVER and PF_DEPLOY_ENV; deploy.sh reads the file PF_DEPLOY_ENV names, refusing when it is missing, and checks the deploy target against the Cell's server before any network work; a named Cell's operator creates its deploy-<profile>.env after upgrading, a manual run keeps the old behavior)
// - the v0.2.1 section appended with the task concurrency bound as a Host key (max_concurrent_tasks, default 2, live on reload; one-off tasks take no max_concurrent_jobs slot any more, and a spawn without budget flags gets 40m / 1000000 — the token default is the ceiling)
// - the v0.2.1 section appended with a bundle-bound ticket as a parent (PARENT_NOT_REGISTRY and HAS_CHILDREN_NO_BUNDLE are gone; a bound parent waits for its children to merge, via "child" in waiting_for, pf file edit <child> --parent "" detaches, PARENT_BUNDLE_FROZEN; deploy aihub before pf, and a rollback to the old aihub loses the wait)
// - the v0.2.3 section appended with the corrections to the v0.2.2 upgrade notes (five headings above say v0.2.1 but shipped with v0.2.2 — the pf step ask, gate fallback, human merge, task concurrency and bundle-parent sections; and the v0.2.2 pf/daemon writes that carry expected_updated_at need the new aihub first, the v0.2.1 server rejects the whole PATCH with field … is not patchable, pf file edit still writes)
// - the v0.2.3 section appended with the scratch layout categories (raw/ kept 3 days, tasks/ and sessions/ kept 14 days, one directory per run, no loose top-level file; --prune-scratch only removes a category's stale batches and the flat top-level 14-day rule is gone, entries outside the categories are only ever reported; re-run pf init to create the categories and refresh the notes' layout section — doctor warns an outdated one; a dispatched task is handed PF_SCRATCH_DIR/PF_SCRATCH_RAW_DIR and the same paths in its prompt)
// - the v0.2.3 section appended with the memory writer columns (polyforge#107: writer_kind/writer_details on memories, owner_display_name on every read; upgrade aihub and run deploy/sql/v0.2.3 first or every memory request fails; the backfill marks the pf-step-memory:-keyed rows as step; the rollback is a no-op and never drops the durable columns)
// - the second v0.2.3 section appended with the harness guidance (every step dispatch and task input carries the short output-discipline rule from the one source harness.ConciseRule, the doctor's harness concise guidance row and claude's resolved ~/.claude/CLAUDE.md-else-AGENTS.md target, and tools/install.sh auto-runs pf doctor --fix-harness-prompts once per install)
// - the v0.2.3 section appended with the dependency audit's declared level and the release reusing the gate's audit (audit_prod_level=moderate in .polyforge/verify-scope.conf, the only conf key that reaches the audit gate and tightening only: production dependencies turn the audit red from moderate up via --npm-prod-level, dev stays at high, the gate id hashes the key; the release's default audit is the engine's own audit command, run verbatim after the build and never skipped by a reused green tag; every audited surface prints its fetch time, UTC, and source)
// - the v0.2.3 section appended with the forensics standing rule (polyforge#122: SKILL.md session rule 9's six duties — raw data on disk first, locate then read by range, tool returns bounded at 8 KiB with the truncation stated, no reload with the exit code kept, conclusions and references after filing, the same constraints passed to a task or sub-agent brief; the task drafting list and the build's sub-agent brief rule bind their own directories; no action on upgrade, the skill is wired by reference)
// - the v0.2.3 section appended with the epic lane (a lane's `epic` key over `children_branch` and `resume_reset`: its own work items check the children branch of their own seq out — target-branch.json's `checkout` key, no `target`, so the epic merges into the default branch — the claim's children snapshot `runtime-state/children.json` is immutable while the epic runs, the driver re-checks it before every dispatch and parks a changed set blocked without a question, a changed re-claim writes `runtime-state/epic-resume.json` which lands as one owner-class rewind to the lane-declared resume_reset step charged to no budget, the hub list gained a `parent` filter, and the epic's merge registers ONE landed record with the children attached verbatim)
// - the v0.2.3 section appended with the light tier's selection closing its historical misses (a declared go_harness's directory carries: declarations are consumed into the E2E_RUN name set, malformed fail-closed; the scoped_command=<glob>|<command> conf key maps a changed path to a conditional scoped command, this repository declaring the step scripts' Python unit suite for workflows/_shared/scripts/*; test/w111_light_historical_replay_test.go replays polyforge#87's five historical misses and reds on any quiet regression or ballooning of the selection)
// - the v0.2.3 section appended with the epic split filing procedure and the split metrics (polyforge#118: the file verb's owner-confirmed split — the first sub-ticket filed --parent holds the original out of the ready queue, pf file edit --bundle epic moves it once held, one sub-ticket per decomposition unit filed check-first with split.origin/split.unit attrs so a re-run files no duplicate, the sibling order as --after written at create, the checklist verified before release; concepts/bundles/config follow; tools/review-firstpass --cohort groups an owner-confirmed manifest — unsplit control, split children, the epic apart — and reports the rework share of proxy execution time and the claim->merge proxy with the real endpoint N/A offline; no action on upgrade, the skill is wired by reference and the tool reads exported capsule trees)
// - the v0.2.3 section appended with the integration batch's in-batch repair (the lane's two rewind_only steps, repair and review: a red gate whose every unit is attributed and fixable by tests or documentation hands the batch back charged over the one max_rewinds budget, the fix lands as a PR onto the default branch by the revert step so the green tag lands there; a person-merged project (steps.<project>.merge.by: human, frozen into the claim's step modes) gets none of it, an owner's revert rewinds to revert by name, the batch record gaining fixes; no action on upgrade)
// - the v0.2.4 section appended with the rebuildable build products a doctor switch reclaims at once (polyforge#127: the rebuildable artifacts row reports, --prune-rebuildables removes the directories a repository declares in .polyforge/capsule-gc.conf from the terminal work items' capsule clones without waiting for the 14-day retention, the declaration read from the mapped workspace's .repo clone's committed default branch so the backlog predating it is cleaned too, probe-passing all-ignored directories only with artifacts/, capsule.json, the git dirs, the local commits and the git status never touched, nothing removed while the daemon is down, this repository's own declaration shipping pi-web/.next and pi-web/node_modules; no upgrade action beyond taking the new pf, the default doctor run still writes nothing)
// - the v0.2.4 section appended with the local development channel (polyforge#136: install.sh --dev takes origin's latest green tag, builds v<next patch>-dev.<first-parent count> locally through make dev-build, refuses AIHub-side sets from tools/aihub-paths.conf in prepare, writes nothing to GitHub, stamps the source commit into --json version's commit field and pf doctor's new pf build row, and the formal channel stays refused until the next formal release)
// - the v0.2.4 section appended with the per-clone-path cache volume (polyforge#153: the verification container's cache volume is pf-verify-cache-<repo>-<image hash>-<clone path hash> and the clone mounts at its canonical path, so two clones at different paths never share one volume's path-sensitive cache; the pre-#153 shared shape pf-verify-cache-<repo>-<32 hex> is never read or removed by the new scripts — the upgrade note hands its reclamation to a person — and the new shape accumulates one volume per clone path per image generation, still pruning only the own repo's older generations)
// - the v0.2.4 section appended with the release lane shipping the green integrate batch (release.sh takes --pins and its --help must declare it, an old-convention script is refused PF-RELEASE-PROCEDURE_CONTRACT, no green batch is refused before any version is claimed, and the batch is the test evidence the release runs no gate of its own)
// - the v0.2.4 section appended with the build fast check's flaky re-run and the red-state limit (polyforge#157: fast_checks.py --repo re-runs a located failing unit once and records a pass flaky, an all-flaky command confirmed by its completion proof or one whole re-run; --check-units re-runs only a red pre_existing_red unit while a unit the lane just wrote stays red; a unit's self-reported `timeout` in units.json — a Go duration, invalid or non-positive unusable — bounds its recorded run and its facts replay with a shared 10m default, and a red-state run its own limit kills is an exit-1 evidence problem, no longer not-judged; only a kill by the whole run's deadline stays not judged)
// upgrade-sections:end
// The file is only routed to, from the install ops page and the README
// landing page.
func TestW254UpgradeByteIdentical(t *testing.T) {
	raw := []byte(w254Read(t, "docs/upgrade.md"))
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != "395bca538d44348e1edb2afdda62f1ce81a9d0fc93852cfc51013eaf041575ea" {
		t.Fatalf("docs/upgrade.md must stay byte-identical (only routed to, never rewritten): sha256 %s != the pinned pre-move sha256", got)
	}
	// the list beside the pin: one whole `// - ` line per appended section
	// between the markers (the daemon's mechanical merge unions whole lines
	// only)
	self := w254Read(t, "test/w254_docs_set_acceptance_test.go")
	b, e := strings.Index(self, "\n// upgrade-sections:begin\n"), strings.Index(self, "\n// upgrade-sections:end\n")
	if b < 0 || e < b {
		t.Fatal("the pin's comment must keep its list between `// upgrade-sections:begin` and `// upgrade-sections:end`")
	}
	for _, l := range strings.Split(self[b+len("\n// upgrade-sections:begin\n"):e], "\n") {
		if !strings.HasPrefix(l, "// - ") {
			t.Fatalf("each line of the pin's list is `// - <one appended section>`, got %q", l)
		}
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
// the skill's extensions fragment, the lanes' README.md daemon-flag
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
	lanes, err := filepath.Glob(filepath.Join(root, "workflows", "*", "README.md"))
	if err != nil || len(lanes) == 0 {
		t.Fatalf("glob workflows/*/README.md: %v (%d lanes)", err, len(lanes))
	}
	for _, lane := range lanes {
		if body, err := os.ReadFile(lane); err == nil && strings.Contains(string(body), "README, `pf daemon` flags") {
			stale = append(stale, filepath.Join("workflows", filepath.Base(filepath.Dir(lane)), "README.md")+" still points its daemon-flag budget at README")
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
