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
// develop and micro since removed the four older ones; the
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
// removed,), never as a draft awaiting confirmation.
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
// nothing else. Each append to docs/upgrade.md adds one line at the end of
// the list and moves the pin, in the same commit (the append-only rule,
// spec R14: docs change with the code, and the pin with the
// docs); the daemon's mechanical merge (develop's sync and merge steps)
// unions two lines appended at once (.polyforge/integrate.conf, list=).
// upgrade-sections:begin
// - main's bytes plus the appended,, and entries
// - the absorbed public PR #15/#16 rewrites — polyforge#262 identifier anonymization and history-proof api-key example, polyforge#263 neutral candidate names
// - the expected_deltas contract
// - the appended v2.20.10 section (the signed-heartbeat version gate, its ratchet and the owner-accepted leaked-key limit)
// - the appended entries (the recall duty rides the steps declaring `recall: true`; the parked close and resume answers, the 24 h close-retry window, the blocked reason in the hub note and the 10 s remote-watch snapshot cache)
// - the appended entry (claude steps' Bash timeouts follow the tool window)
// - the appended entry (the first model_error resumes, the paced retries and the status face's RETRY column)
// - the appended/#115 entry (the conf-first engine and `pf verify-run`)
// - the appended entry (the step tool window bounds verify-run's deadline)
// - the appended TMPDIR-reclamation entry
// - the appended v2.20.12 section (aihub's browse total, `pf recall` browse mode, the task-list prompt summary and pi-web's 2.20.12 floor)
// - the appended v2.20.12 section (the project member face, `pf msg contacts` and pi-web's Messages 2.20.12 floor)
// - the appended section (owner exits, the reserved cancel answer and owner-routed rewinds)
// - the appended section (a rewind record without a class fails closed)
// - the appended section (step attrs in .polyforge.yaml)
// - the appended section (the aihub server is `pf aihub`; one pf binary ships)
// - the appended section (verify-scope selects e2e suites by `carries:` declarations; python and rust analyzers)
// - the appended section (the legacy rollback image needs the running version's tag; the dry-run plan names both probe layouts)
// - the appended v2.20.18 section (message analysis runs as a task and answers a structured result)
// - the appended section (full-tier e2e selection by smoke suites; the deliver step's recorded bar runs)
// - the appended section (the capsule retention row slims wrapped capsules; `--prune-capsules`; the disk watermark row)
// - the appended section (e2e-author's four-key executability preflight and its driver rejection)
// - the appended v2.20.20 section (`pf msg prompt` turns a message into an execution prompt)
// - the appended v2.20.21 section (summarize and memory_policy move into cli.toml; `pf config migrate`, its deploy order and rollback)
// - the appended section (the micro lane: pure text changes deliver on fast evidence)
// - the appended section (doc.module-map: the Create convention and current-revision-first recall)
// - the appended section (same-repo full bars run one at a time; the live per-repo bar limit)
// - the appended section (the dependency-audit gate in the full verification bar; the tools every host installs before its release)
// - the appended section (a blocked work item's holder operator may resume or cancel it; the sole-basis audit and the refusal detail)
// - the appended section (one Host, many Cells: the per-Cell data layout, `pf migrate cell-layout` plan/apply/rollback)
// - the appended section (the completed-layout no-op, the byte-exact rollback and the whole-run Host lock; pending-rotation and execution-disabled refusals; the live Cell ceiling; the unreadable-index refusal; local repair verbs under inherited connection variables)
// - the appended section (the unified v0.2.x line, the one accepted v2.20.x -> v0.2.x cutover, the Pi Web install opt-in)
// - the appended section (incomplete blocker evidence and owner continuation with prior output and cumulative runtime)
// - the appended section (deploy/test tmux namespace isolation)
// - the appended section (first public v0.2.0 cutover and content-bound publication checks)
// - the appended section (the daemon's [daemon] command_path, its doctor row and the recorded tier fallback)
// - the appended section (the heavy-run admission's shared repo follows the admission order)
// - the appended section (the develop build settle checks the plan's Bindings commands; an unlocatable bound command rewinds build)
// - the appended section (daemon-run verification in a per-run container)
// - the appended section (`pf bar-run`'s trailer carries fixed-length facts only)
// - the appended section (the develop lane's light verification and its publish step)
// - the appended section (the release's test gate runs in the verification container; green tags let it reuse a result)
// - the appended section (the integration lane merges published work items in batches and marks green)
// - the appended section (develop merges itself; integrate gates and tags; daemon steps run named actions; drain before deploy)
// - the appended section (environment failures block instead of asking; the verify-judge asks the owner itself)
// - the appended section (the daemon asks nothing: agents raise authorization requests, the owner's selection runs the daemon action)
// - the appended notary section (command evidence is the daemon's record only; long runs are requested)
// - the appended section (chat subscriptions retired; the decision feed, pf ops role, the retired chat state doctor row)
// - the appended section (the four retired lanes removed; an uninstalled bundle is refused before the claim; delete their steps' yaml keys before installing)
// - the appended section (files over the chain blob limit stay out of the capsule chain; the artifacts/downloads/ directory; size refusals name the path and range)
// - the appended section (a repo published again after its merge is merged again; the landing record names every merge)
// - the appended section (the develop build's analysis and big-plan units go to isolated sub-agents only; implemented_by; a sub-agent call supervised on its silence, never on a heartbeat; tools/review-firstpass)
// - the appended section (the owner sends a work item back with a reason: pf rewind, the aihub send-back door, charged to no budget, the panel's entry)
// - the appended section (a Claude step runs one --safe-mode profile in every auth mode: its declared tools, the AGENTS.md plugin off, only the harness settings.json read)
// - the appended section (the retired lanes' leftovers removed: pf verify-run, pf revise-binding and its route, the authored-suite surface, the notary's verification exception, deliver's rewind_to, the bar's tool-window deadline ceiling; an in-flight develop item's verification output unvalidated)
// - the appended section (workflow contract 1: one strict lane loader, typed WFC errors, contract/name/description, max_rewinds and build_step, removed keys, WFC-123 claims, the snapshot's contract block, pf workflows validate, the watch field max_rewinds; the drain)
// - the appended section (workflow contract 2: the run action in the verification container, at least once, masked output tails, blocked binding_missing, WFC-185 claims skipped by auto-pull, merge-pr's verified_by and last-agent pr_from; the drain)
// - the appended section (a claude task runs a step's profile with its declared tools; a summarize dispatch has no tools; no Claude process the daemon starts opens a cross-session messaging inbox; an OAuth-login run still to do)
// - the appended section (workflow contract 3: declared inputs, outputs and checks; records; exact verdicts and top-level envelope fields; micro delivers like develop; the drain)
// - the appended section (workflow contract 4: the integration lane is the integration property, fix tickets on its fix_lane; a sub-definition takes its dispatching step's models, the verify-judge key retired; stale workspace keys warn; test_bar and ci_evidence removed; the drain)
// - the appended section (workflow contract 1, publicly frozen: the internal numbers 1 to 4 renumbered to 1; user lanes in <workspace>/.polyforge/workflows/<lane>/, resolved per work item; WFC-122, a user lane's WFC-172, WFC-123 per workspace; a refused user lane only disables itself; pf workflows validate judges the workspace's lane set; a dispatch judges attr overrides against its snapshot, pf resume reads the current lane; a released machine follows every later section in order; the drain of an internal build)
// - the appended section (hub and carrier git read GIT_CONFIG_GLOBAL from the daemon-owned empty <data-dir>/git/empty.config, mode 0444, rebuilt when not an empty regular file, instead of /dev/null; the new pf doctor null device row with its atomic mknod recovery command; no action)
// - the appended section (the daemon's integrate trigger: on by default, a batch after the quiet period following the last landing, the integrate: section of .polyforge.yaml, one unfinished batch per project at aihub's exclusive create, the integrate_trigger event, the doctor row and the watch batch line; the trigger obeys pf ops auto off and the Cell's gates (held); to turn it off: auto off, upgrade, auto: false, auto on; remove the section before a rollback; aihub first)
// - the appended section (pf submit refuses an execution ticket with no criterion under ## Acceptance or ## 验收, exit 1, unless --allow-no-acceptance; the driver and the CLI share one prefix-matched heading definition, so a ## 验收 ticket has acceptance; why; no action beyond the binary)
// - the appended workflow contract v2 PR 1 section (develop's build declares owner-stops acceptance only and asks the owner only on a surprise — a dead end or contradiction, a deviation, confirm_plan right after the plan, a missing acceptance list — never at completion; the approval grammar and the archived answer's acceptance/grill snapshots are deleted, any owner answer counts and the build reads it, its summary carrying the words and its reading; the judged list of a ticket without an acceptance section is the plan's current ## Acceptance; review's guarantee rule reads the build summary against the owner's words; the contract stays 1 and the registry keeps the grill stop for user lanes; no action)
// - the appended workflow-contract-v2 change-2 section (the step commands `pf-run` and `pf-verify`: their result file, symlinks, doctor row and step environment)
// - the appended workflow-contract-v2 change-3 section (the step commands `pf-sync`, `pf-merge` and `pf-revert`: the sync record and `--continue`, the landing rules and revert ordering moved into gitmerge, the plan record, symlinks and the doctor row)
// - the appended section (the verification container's default cpu limit is 8 capped at the host's cores; a configured limit is passed as written; a green tag's plan digest carries the effective limit)
// - the appended section (the auto-pull skips an execution ticket with no criterion under ## Acceptance or ## 验收, #460's one predicate, presence only; pf queue's STATUS queued (需要交互：缺验收), the merged --json acceptance_missing and Pi Web's label; an integration fix ticket's acceptance gains one criterion per named red unit, passes on the fixed head; why; queued tickets without acceptance stay queued until pf file edit adds it or a hand submit)
// - the appended workflow-contract-v2 change-6c section (`pf ask` and the awaiting-owner state: the capsule's asks.jsonl record, PF_STEP/PF_SPAWN, the awaiting_owner event under the needs_input class, the pending-question faces, the released slot, supervision not counting the wait, the ask_warm_window key, restart parks without a dispatch; a v1 needs_input mirrored as an ask and its answer as the reply)
// - the appended workflow-contract-v2 change-6d section (the owner-message relay: `pf answer --text` at any time with no question id, the reply or message line before the owner_message event before the delivery, the live process or the resumed session under a new spawn ordinal, the delivered line and event only on the marker in the harness stream, a restart replays only undelivered messages, the ask's stamp as --question, --option refused without a v1 question, the feed resolves an ask on the step's result, not on the message; no action beyond the binary)
// - the appended workflow-contract-v2 change-8 section (workflow contract 2, additive: the agent step's skill, verifies, resources, tool_minutes, model_tier with defaults.tiers, effects, handoff, waivable, the pool input and the artifact format; the envelope's intent and effects; the waived record and the effect_failed park; WFC-200/204/205; every shipped lane contract: 2 with the step-level placeholder lists gone; a spent rewind budget asks the owner through the gate's agent on every lane, a bare FAIL parks rewind_budget, P6 reads the owner-message record; waivable default false; pf waive and its route; the answers input names asks.jsonl; the step's declared tool window for bar-run lines; replay exit 126/127 not judged; the workspace yaml's tiers section; the drain)
// - the appended workflow-contract-v2 change-9 section (the release lane's agents run release.sh / deploy.sh themselves in one tool call under PF_COMMAND_PATH, no evidence, bar-run or run request; release and deploy hold the heavy-run admission so a release waits for running verifications; release's 120-minute tool window inside a 135-minute wall, deploy 30, verify wall 30; no gate waivable; verify verifies release's record and deploy the verified version against release's reported version; model_tier mid on release and deploy over the shipped placeholder tiers, verify on no tier)
// - the appended workflow-contract-v2 change-14 section (Pi Web's job card as the step's conversation: the ask highlighted and the owner's messages as bubbles in the flow, one input box to the step agent through the owner-message relay with no question id, enabled while running and awaiting, the running sub-agent list, the no-agent menu 继续/取消/打回到… per §5.5's table with `POST /api/polyforge/cancel` and the item panel's actions moved into it, the discussion assistant and the option buttons deleted, a contract-1 option only by the whole message or `/<id>`, the echo matched by the record's own stamp, a re-announced ask shown once, the watch route's pendingQuestion and the admitted v2 event kinds; action: re-run install.sh)
// - the appended workflow-contract-v2 change-13 section (the integrate lane's four agent steps collect, revert, full-gate and close under skills and pre-written commands; the hub writes as declared effects and the batch record as the integration_batch hand-off with `reverts`; pf-integrate close's --revert/--triage/--execution/--ledger and absent-file tolerance, collect's hold merges and pf-revert's held items; the batch progress read from the step state; the ruling and fix-tickets sub-definitions gone from the shipped lane; no action beyond the binary)
// - the appended workflow-contract-v2 change-11 section (develop: sync, verification, publish and merge are mechanical agent steps on the cheap tier following the shared skills that run pf-sync, pf-verify and pf-merge verbatim and verify the step before them first; sync no gate, a real conflict resolved in place and continued; verification the one waivable gate under the heavy admission; merge hands the landing off under the merge lock and owes the wrap summary; the daemon runs no develop action; install.sh links and the cheap tier's model; pf-sync --abort and the round-tied sync record)
// - the appended workflow-contract-v2 change-12 section (develop: build records each unit's red-then-green run in artifacts/build/units.json, no bar-run, no notary and no check on build; its output is coverage and summary; it asks through pf ask only in the four cases, the stop question ids gone; review computes pf-facts plan-conformance, build --replay and carries under the heavy admission and verifies build's hand-off first, no fact sections, no finding-classes check (the schema enforced), no sweep; the replay in the repo's verification container, else on the host as pf-verify runs the bar; pf-verify --unit-runs runs each unit's run in the verification; the same-tree rules in the skills behind the daemon's park; install.sh and the plugin update)
// - the appended workflow-contract-v2 change-10 section (micro: fix and review on the mid tier, sync, evidence, publish and merge on the cheap tier, every step an agent step following its skill — micro-bounds and evidence of its own, the shared sync, publish and merge — that runs pf-facts micro-bounds, pf-sync, pf-run --plan-from and pf-merge verbatim and verifies the step before it first; fix writes the evidence plan and judges its bounds, review re-runs the judgment; out of bounds fix asks the owner to re-file on develop and cancels on the owner's word instead of failed_gate; evidence the one waivable gate under the heavy admission; resolved-conflict-review gone; merge the last agent step; review's class is its schema's; the shared publish definition is the v2 one; install.sh links and the mid and cheap tiers' models)
// - the appended workflow-contract-v2 change-15 section (workflow contract 3: build_step and {{build_step}}, executor, action, with, checks, sweep, evidence and the facts input removed, each WFC-111 with its rewrite; every step an agent step; the records last_review_round and waived; the codes 131-133, 150-152, 160-162, 171, 185 and 196 out of force; the envelope's needs_input, question fields, run_request and command_evidence out of the contract; the shipped lanes at contract 3 without build_step, the shared review's rework_step var, every skill's pf-ask and foreground rules, the principles' waiting rule aligned, the deliverable_carrier fragment gone; the rewrite table; user lanes rewritten; drain #2)
// - the appended workflow-contract-v2 change-20 section (the watch snapshot drops verify_queue, run_request, options[].action_text and — a fourth, dead since change 14 — pending_question.asked_by: pf watch --once prints no verify: line and an action option without the daemon's text, a run request still no question, Pi Web's watch route and composer without actionText, the capsule still records the asker; the admission's events and the /status job row's verify_queue unchanged; action: install.sh and the plugin update)
// - the appended workflow-contract-v2 change-19 section (a step's needs_input refused at result submit and the accept gate and the step re-dispatched told to pf ask; pf answer the owner-message relay alone — --question, --option, the option selection and the reserved cancel gone, the answer route refusing question_id and option; no v1 question on the watch, status, feed or model-health faces, no options: lines, no pending_invalid; Pi Web's composer words only, no option list; the driver's checks, fact sections, sweep and unchanged-tree park deleted, the capsule tree hash's retry wording kept; aihub's cancel door without dry_run; action: aihub, then install.sh and the plugin update, open v1 questions answered first)
// - the appended workflow-contract-v2 change-16 section (the daemon's verify, run, sync-default-branch and merge-pr actions deleted with their dispatch, the verify-judge and resolve-conflicts sub-definitions, the run-repo check WFC-185 and the step's verify: queued record, the heavy admission and its events unchanged; no heads record, last_review_round.heads kept; pf-verify's bar without the merge phase, test_bar, test_bar_notes, command_evidence, the test_files checks and the execution.json plan bindings; [daemon] verify_deadline gone and refused as unknown, command_path and verify_container_* kept; no binding_missing or forge blocked kind, Pi Web without them and without the verify_routed end line, its checks or a verify-judge block; the bar light-only, the full tier pf-gate's; the note that change 19 also removed criterion 5's fast-path suppression on an unchanged tree; action: remove verify_deadline, then install.sh)
// - the appended workflow-contract-v2 change-18 section (pf bar-run, the /step/bar-run and /step/bar-lock routes and the bar-runs record deleted, a script calling pf bar-run refused as unknown, an old bar-runs/ ignored; the envelope without needs_input, question fields, options, run_request and command_evidence, an envelope carrying them landing without them, the execution record without pending_question, answers, answer_base and the commands record; no environment blocked kind and no log_tail; no question_withdrawn or answered event and no answer era; the publish rules gone; the per-repo bar lock kept for locks: [bar], no config key removed; action: remove pf bar-run from scripts, then install.sh and the plugin update)
// - the appended workflow-contract-4 section (one step entry pf step: handoff, ask, recall/memory show/remember, hub verbs with pending writes made on done; one hand-off document artifacts/handoff.md with daemon-stamped headers; inputs, verifies, outputs, verdicts, effects, handoff, step attrs, on_fail.charge, integration.fix_lane, the definition's output schema and locks: [bar] removed, codes 141, 172, 180-182, 192, 193, 197, 204, 205 out of force, the hub key new; the reactions, the wake of a parked work item by an owner message, the supervision rules, the six-part prompt; pf result submit, pf waive, pf rewind and the step-side pf ask/recall/remember removed, the /step/result, waive and rewind routes and Pi Web's three job-card controls gone; step-calls.jsonl new, pending-results, result.json archives, pool.json, effects.json and the owner rulings gone, execution.json daemon-private, wrap-attrs and attrs.waivers at the wrap; steps.*.attrs and max_concurrent_bars_per_repo removed; the shipped lanes at contract 4 with the wrap_attrs key (merge: integration, close: integration_batch) refusing done until registered; pending writes per step round, dropped writes recorded, an empty batch a no-op, clear-hold citing the owner's message; the wake through the owner's own door, a cannot-start answer refused and not recorded; user-lane rewrite checklist; drain #3)
// - the appended workflow-contract-4 C3a section (the heavy-run admission with two gates and no repo reservations; bar_wait, the --once bar wait line, the bar-lock queue reason and Pi Web's Host row for max_concurrent_bars_per_repo gone; no step attrs on pf workflows or pf config workspace, an attrs key refused as an unknown workspace key, the steps.* write gate on the daemon's workflows dir, no /status capabilities, no attr_override_ignored event; pf daemon --retry-backoff-base removed; action: none beyond the contract 4 drain)
// - the appended workflow-contract-4 C3e correction section (execution.json keeps no gate FAIL's commits and no waivers: the FAIL is its handoff.md section with the stamped start and end commits and its call-ledger record, the waivers the ledger's waive hand-offs written to attrs.waivers at the wrap; the workflow status failed_rewind_exhausted, written by no daemon since v0.2.7, gone from the decision table and Pi Web's finished set; no action)
// - the appended step-scripts section (the daemon resolves the step scripts' Python once at its start — [daemon] python, Host-only and restart-only, else python3 on its PATH — and refuses a missing, silent or pre-3.9 one, exit 78; PF_CAPSULE, PF_LANE_DIR, PF_SHARED_DIR and PF_PYTHON in every step; pf doctor's python3 row; the published pf-step-result-1.json schema and the pfstep library in the lane snapshot; action: pf doctor, then install Python >= 3.9 or set [daemon] python; rollback: unset python first)
// - the appended section (the scope engine lists Go packages with go list -e and tolerates an embed pattern matching no file, so a fresh clone without the built front end gets a scoped light plan, other package errors still full; a changed stack config file diagnosed under --light; result.json's tier.reason replaced by tier.reasons, one report-mechanical.md line each, the informational --light diagnostics no longer reasons; the doctor's scoped-gates readiness row counts the latest light runs that ran full by reason category, status unchanged; action: none beyond install.sh, scripts read tier.reasons)
// - the appended section (pf submit answers the claim's verdict: a free slot claims before the answer and the placement line means the claim happened; an aihub refusal is exit 1 with the typed code, the not-submitted detail and, for an unmerged blocker, the blocker, its status and the wait-or-pf-file-dep-rm guidance, nothing left queued and the refusal still recorded as the submit_rejected event before the answer; a waiting submit prints the queued: line pointing at pf watch, a later refusal still the submit_rejected event; --json gains state and queued_reason, machine_id/capsule_path on claimed only; no shim; no action beyond the binary)
// - the appended verify-script section (develop's verification runs the workflow script verify.py from the lane snapshot, pf-verify no longer dispatched, linked or doctor-checked, a stale link is pf itself; the result names verify.py and pfstep/1; where a repo's bar runs unchanged; the container limits the script's own, [daemon] verify_container_* no longer reaching it; a unit's marker a Python re.ASCII regular expression; a nested root's full-tier fallback makes the run full; the engine from the shared tree first; action: drain develop's verification, pf resume re-claims; rollback: the previous release)
// - the appended section (pf-sync, pf-merge, pf-revert and pf-run left pf: the workflow's scripts sync.py, merge.py, revert.py and run.py, run by the skills as "$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py" lines from the lane snapshot, same flags, exit codes, result files and side files, command the script's name, version pfstep/1, hardened git committing as the lane; run.py's required --isolation container|host, micro's evidence on the host, the scripts' own container limits; the steps after them check the result file's argv; install.sh links and the doctor's step commands row hold pf-facts, pf-gate, pf-integrate, old links removed by hand; action: drain develop, micro and integrate before installing, rollback the same)
// - the appended step-scripts P2 section (facts.py and integrate.py replace pf-facts and pf-integrate in the review, micro bounds, collect and close skills — the same flags, exit codes, result files and details, the result naming the script (facts.py, integrate.py), integrate.py reading the pool through pf --json step hub; integrate.py close reading revert.py's result; the names no longer dispatched, install.sh linking only pf-gate; review replays on the host with --isolation host, a container replay on the scripts' limits, not [daemon] verify_container_*; tools/verify-scope and tools/audit-deps moved unchanged to workflows/_shared/scripts, found at $PF_SHARED_DIR/scripts in a step (verify.py's only engine now, its release fallback gone), the release checkout for a hand run, the gated tree's own for the gate; no scoped-gates readiness row, scope_ready.py instead, its light-fallback note a row of its own, light verification fallbacks; action: drain those lanes, install, remove the pf-facts and pf-integrate links, repoint hand runs; rollback: the previous install.sh)
// - the appended agent-language section (the [daemon] agent_language key, en or zh-CN, absent = en: after the upgrade the step agents work in English and the handoff.md stamps are English; the body's seven headings and the re-entry heading in both languages, pf step handoff checking the set of the dispatch's language; fixed per dispatch, a resumed conversation keeping its language; a step dispatched by the previous binary checked and stamped as en; Pi Web's Agent language save writing the key of the default Cell through pf config set, one way, a Pi Web already set to Chinese not carried over until saved again; a work item claimed before the upgrade keeping its capsule's Chinese-only handoff.md template while its new dispatches default to en; action: install.sh, then right after it pf config set agent_language zh-CN to keep Chinese; unset it before a rollback)
// - the appended gate-script section (the full test gate is the workflow script gate.py, run by the integrate lane from its snapshot and by release.sh and tools/green-tag from the released tree with python3, its plan from the tree's workflows/_shared/scripts/verify-scope; pf-gate and cmd/pf-gate gone and with them the last step command, the dispatch, link loop and doctor row left for, a stale link is pf itself; the release needs python3 >= 3.9 not go for its gate, refused before any gate runs; --isolation container required, no Dockerfile or no docker engine not judged; the container limits the script's flags, [daemon] verify_container_* reaching nothing any more; result command and green-tag runner gate.py, runner version 2, a Go runner's tag never reused, integrate.py close reading only gate.py results; action: drain the integrate lane's full-gate, pf resume re-claims; rollback: the previous release)
// - the appended section (integration batches only by hand: the daemon's integrate trigger deleted, no batch filed or submitted by any daemon, none of a dead filer's queued batches submitted; a leftover integrate: section of .polyforge.yaml refused whole with its rewrite hint, DAEMON-414 at claims and dispatches, the doctor's workspace yaml row failing; no trigger block on GET /status?integration=1, no integrate trigger doctor row, no integrate_trigger event, no watch batch block or batch: line, an integration-auto batch read as any other; pf ops integrate's refusal naming the open batch and its status, a queued never-claimed one with its filing time and pf submit / pf cancel; action: delete the section right after install.sh, not before, pf resume a step it blocked; rollback: the previous binary, then auto: false per project)
// - the appended step-command-removal section (pf answers to no step-command name, no argv[0] dispatch; install.sh removes the retired pf-* links pointing at pf, a user's own file of that name left alone, nothing by hand; no step commands doctor row; [daemon] verify_container_* gone and refused as an unknown key, delete the lines before installing; no verify container engine or verify container paths doctor rows and no start-up container sweep, the script's own owner label pf-step or pf-gate and a fresh boot, expired containers removed by the next run of the owner; with command_path set a step process's PATH the daemon's own pf directory, its harness binary's directory and the key's entries, each once, unset the daemon's own PATH as before, GET /status step_path and the doctor's command path row checking pf under it; no PF_COMMAND_PATH, PF_VERIFY_DAEMON or PF_VERIFY_BOOT, the release lane's lines without the PATH prefix; gate.py --triage refusing a batch file not integrate.py's; pfstep.command merged into pfstep.invoke, pfstep.hub, sweep_stale and the stepenv PATH helpers gone; action: remove the keys, drain the release lane, install; rollback: the previous release)
// - the appended section (the integration lane's read-only faces gone, the workflow contract untouched: no pf ops integrate status, refused as an unexpected argument, an older pf running it against the new daemon printing an empty pool; GET /status answers no integration block, ?integration=1 read by nothing, on a Cell and the Host aggregate, no pool read from aihub; the watch snapshot has no integration block and pf watch --once no integration: line, the landing record unchanged on the aihub attrs; a batch followed with pf watch; action: none beyond install.sh, Pi Web read none of them; rollback: the previous binary)
// - the appended section (a step's last stdout records reach its exit's class: the stdout reader read after the exit until a read started after it finds the pipe empty, its own 5 s bound instead of 500 ms to EOF, a descendant holding the pipe costing nothing; stderr's same rule recorded; a missed bound logged and noted in the archived failure detail, stdout records incomplete / stderr tail incomplete, never classified; action: none; rollback: the previous binary)
// - the appended workflow-contract-5 part-1 section (a lane is workflow.yaml, contract 4's workflow.md frontmatter with contract: 5, the prose README.md the daemon never reads; a workflow.md alone WFC-101 naming its declared contract, beside a workflow.yaml WFC-110, a second YAML document WFC-110; vars_files reads a var from a file of the lane inside it, no .., absolute path or symlink, the same var in vars WFC-112, develop's review intro and judge in fragments/; the generated lane schema workflows/_schema/workflow-5.schema.json and the editor's yaml-language-server line; the snapshot copies workflow.yaml, README.md, fragments/ and steps/, Pi Web's cards read the declared order from workflow.yaml; action: drain, a work item left in flight parks blocked kind contract, the built-in lanes ship converted, each user lane rewritten with the awk lines and pf workflows validate; rollback: restore workflow.md)
// - the appended workflow-contract-5 part-2 section (pf step hub attrs <wi|key:K> --set <key> <json|@file> [--if <path>=<value>]... [--per-owner <at>] replacing move and clear-hold, a whole top-level attrs key, null deletes, the --if fence CORE-409 ATTRS_IF_MISMATCH, the batch op shape, the owner message copied to aihub with the write; hub list --attr <path>=<value> replacing --integration; a step's hub: a mapping {verbs, attrs}, attrs granting the verb and the only keys written, the list form WFC-112; aihub's integration and clear-hold routes and ?integration= gone, PATCH /work-items/{id}/attrs signed machine route and ?attr=<path>:<value>, attrs_written events, reached by a step only through the daemon; the integration rules in pfstep/integration.py, the whole record fenced on the whole record as read so no write drops another's keys, landed to landed, merged on a green tag or reverted, the develop lane's keys never rewritten, built by integrate.py close, revert.py --ops-out (revert's hub naming get) and the new integrate.py records (fix, clear, blame entries; exit 1 refused, nothing written), a hold cleared only on the owner's message with hold_cleared, refused while another batch of the lane runs, no holds_cleared on the blaming batch, no migration, no maintainer needed to clear a hold; action: drain, deploy aihub, install on every machine, rewrite user lanes' hub:; rollback: previous aihub and release together)
// - the appended workflow-contract-5 part-3 section (pf ops integrate deleted, its status face and --clear-hold with it, an unknown pf ops action; a batch filed as any work item with pf file create --bundle integrate and pf submit, no integration label, no pre-check, nothing landed or nothing moved ending empty; the lane key exclusive, the daemon's proxy forwarding a create on such a lane exclusive, aihub refusing CORE-409 OPEN_ON_BUNDLE, pf file create naming the open work item and its status, a never-claimed queued one with its filing time and pf submit / pf cancel, user lanes too, checked at a create, not at a rebind; a hold cleared only by a running batch's collect question, --per-owner; the integration: true marker and its property removed, WFC-111; the integrate: section refusal naming pf file create and pf submit; action: drain, install on every machine, rewrite scripts; rollback: the previous release)
// - the appended section (aihub's send-back route and the owner-rewind settle route deleted, both answering the mux's 404, no client since contract 4 C3c (v0.2.9); aihub writes no attrs.owner_rewind and none of its recorded, applied or dropped events, a plain resume no longer drops a legacy attrs.owner_rewind, which stays read by nothing; owner_rewind an ordinary attrs key at create and PATCH attrs, only blocked_seal refused; action: deploy aihub, no SQL migration; rollback: the previous aihub)
// - the appended section (a summarize call writes no session: pi -p --no-session, claude --no-session-persistence, one hidden session per call before; Pi Web's hidden-session badge counts only inside the day window like the list, 0 days every session; action: optional, by hand, list then delete only the summarize-cwd directories, pi under the daemon user's ~/.pi/agent/sessions, claude under <harness-root>/claude-config/projects, nothing deleted by the daemon or install.sh; rollback: the previous binary and Pi Web)
// - the appended lane-acceptance-key section
// - the appended section (Pi Web carries its saved Agent language into an unset [daemon] agent_language of the default Cell at its start and whenever its settings page reads it, only when the values differ, never over a key pf's file sets, nothing written or reported with an older pf, no pf or a failing pf; the settings page's pf line, mismatch hint and sync button, the sync an explicit save; the GET's idempotent write reachable by any /api/* caller, signed in with PI_WEB_PASSWORD; #501's right-after-install pf config set kept as the fallback, #501's section unchanged; action: install.sh and start Pi Web; rollback: the previous Pi Web, the written key stays)
// - the appended workflow-contract-version-rule section (the version moves only on an incompatible change, at most once per release; a compatible change - a new optional key whose default is the present behavior, a new value - never bumps it and needs no drain, golden file and lane schema regenerated in place; an incompatible change registered in internal/common/workflow/contract-pending.md when it merges, a test red on a removed key, new required key or removed value with no entry; release.sh refusing a release while an entry waits and the version is still the previous release's, a bump only by one and with the list emptied, an empty list no bump and no drain; WFC-111 naming the unknown key and asking to upgrade pf; current state: since v0.2.10 only's acceptance key, compatible,'s unmerged skippable-step key compatible too, the next release keeps contract 5 with no drain; action: install, no drain; rollback: the previous release)
// - the appended section (summarize sessions removed by tools/clean-summarize-sessions judging each session's first user message, replacing the rm -rf, which also removed one-off sessions, missed the early ones and could match a user's own directory; it lists first, deletes only with --delete, scans every ~/.pi/agent/sessions directory and every Cell's claude-config/projects, --harness-root for a custom root, never removes a directory; Pi Web's GET /api/agent-language reads pf once and never writes, the backfill only at Pi Web's start; the settings page's hint for a pf still unset; install.sh --with-pi-web skips npm install -g for the same installed version, --reinstall-pi-web reinstalls; action: install.sh; rollback: the previous release and Pi Web)
// - the appended section (aihub's console work-item list orders same-millisecond creations newest first, julianday(created_at) DESC then the per-project seq, julianday rounding to the nearest millisecond, the random id tiebreak gone; the page cursor version 2 carrying created_at and seq, a version-1 cursor refused 400 with no compatibility path; action: deploy aihub, no SQL migration, a console page opened before the deploy on page 2 or later answering 400 once, the first page continuing; rollback: the previous aihub, a version-2 cursor refused once the same way)
// - the appended section (a lane step may declare skippable, develop's merge alone among the shipped lanes, contract 5 extended in place; the workspace yaml switch steps.<project>.<step-id>.skip, a step entry's models and skip, a skip-only entry no chain level, none on default or task, written by pf config set|unset, class next-claim, refused for a step no lane declares skippable, ignored when hand-written on another, micro's merge still running; the skipped set fixed at the first claim in runtime-state/skipped-steps.json, kept by a re-claim and pf resume; a skipped step never dispatched, execution status skipped, the step_skipped event, the handoff.md opening noting it; publish the last step, merge's wrap_attrs passing to it, the PR body awaiting a human review, published.py's record integration.state = published registered with wrap-attrs, never collected by the integrate lane; pf workflows skippable and skip, Pi Web's switch and gray dashed DAG node; action: install on every machine, then pf config set steps.<project>.merge.skip true, an older daemon refusing the yaml key; rollback: unset the switch, let develop work items in flight finish, the previous release)
// - the appended section (the verification container's memory default 16g, up from 8g: the light tier's per-suite next dev servers at 4 browsers filled the 8g cgroup and the kernel's OOM kill took Chromium's renderer, Target crashed / Page crashed, not /dev/shm; measured 3 suites 3/10 crashing rounds under 8g, 0/10 under 16g, 6 suites 4/5 and 0/5; verify.py's units[].failure, browser_crash or assertion, on every unit that failed its first run, a suite's ABORT line included, also under details.checks[].units[]; units[].not_rerun on a failed unit whose re-run never started, the budget unchanged; the report marking browser crashes and listing Not re-run by check; the verify skill never placing a browser_crash on the change, asking the owner about one still red and the not_rerun list, naming a flaky one a browser crash; action: install.sh, no drain; rollback: the previous release)
// - the appended section (the verification's private TMPDIR /tmp/pfv-... on the host and in the container alike, the host's under /tmp, no longer the step's $TMPDIR, for verify.py, gate.py and run.py: a unix socket under Go's t.TempDir() of a 64-byte test name keeps 11 of sun_path's 107 bytes for /<name>.sock, bind: invalid argument before; the report naming each command's private TMPDIR with its length in bytes under its repo's plan, result.json's repos[].commands[].tmp_dir and the result file's details.checks[].tmp_dir the real path, the failure output and the failure signatures keeping <tmp>; action: install.sh, no drain, leftover pf-verify-tmp-* directories no longer swept, removed by hand once every work item claimed before the upgrade ended; rollback: the previous release, nothing reads tmp_dir, the report losing its TMPDIR lines)
// - the appended section (pf doctor probes each mapped repository's declared host prerequisites: host_requires=<name>|<probe>|<hint> in .polyforge/verify-scope.conf, parsed by the scope engine and listed in its --json report as host_requires, a malformed line refused with a diagnostic, a declaration only, no tier and no command changed, a missing tool still failing its fast gate red; the doctor's host prerequisites row asking the daemon's lane-set engine per mapped clone and probing each with bash -c from the clone's root under the step PATH, 30 s bound, pass with the probe's first line, fail naming the repository, the prerequisite and its install hint, the doctor naming no tool and installing nothing; this repository declaring mypy>=1.20,<2; action: install.sh then pf doctor, install what fails by hand; rollback: the previous release, its engine reporting an unknown key)
// - the appended section (step harnesses and task agents started by the daemon in a new session, setsid without setctty, no controlling terminal, still their own process-group leaders killed whole by the timeout, the step's end and the cleanup layers; pfstep proc.run's commands in a new session, start_new_session: an interactive shell reads its pipe, a command reading /dev/tty fails at once instead of waiting on the daemon's tmux terminal, the group still killed whole; action: install.sh, no drain, the next dispatch after the daemon restart, a work item claimed before keeping its snapshot's proc.py; rollback: the previous release)
// - the appended section (review's Stale docs item carries the doc-impact check: a change to user-visible behaviour, a configuration key, a command or a step definition updates the README, reference docs, upgrade notes and skill docs describing it in the same diff, one left out of sync blocking, project_rule for an AGENTS.md coupling, guarantee otherwise, review.md listing the documents compared, micro's review unchanged; the release step's first-run docs check after the already-released check and before the procedure, README, install and upgrade notes against the range since the previous release tag or the whole tree, no edit, one owner question on a mismatch, release as is or cancel, the preconditions again on a go-ahead, a resumed version skipping it; action: install.sh, no drain; rollback: the previous release)
// - the appended section (the verification container's cache volume pf-verify-cache-<repo>-<hash>, the image tag's own hash, one per image generation: the same Dockerfile warm, any byte changed cold; contents self-checking within a generation, a lockfile change only missing; node_modules not cached, npm ci as before; each container start removes the repo's older generations no container references, named exactly pf-verify-cache-<repo>-<32 hex>, not the current one, docker volume ls dangling, docker volume rm without --force, another repo's or another shape's volume never touched, one in use kept until a later start, a failing listing or removal one line; action: install.sh, a cold first run, the image rebuilt once, the volume of the name before generations removed by hand once per repository; rollback: the previous release, the generation volumes removed by hand)
// - the appended section (pf queue --bundle L and repeatable --attr <path>=<value> passed straight through as aihub's existing list filters ?bundle=L and ?attr=<path>:<value>, split at the first =, the rest verbatim, no default and no special lane or path, aihub checking the path; an --attr without = or with an empty path refused before any request; list mode only, every mapped project's query carrying them without --project, refused with --ready; reach unchanged, a step agent still refused; action: install.sh, an older pf refusing the flags and a Pi Web view reading through them showing that refusal per project; rollback: the previous release)
// - the appended section (the build skill never amends or otherwise rewrites a commit it made, no rebase, no reset past it, a fix a new commit, and checks an earlier commit's red in a separate git worktree, never by switching its working directory; a final end, completed, wrapped, failed or canceled, through the daemon's release or a hub read, resolves the work item's carrier_push_failed decision entries, a blocked or paused end keeps them, an entry the previous release left open dismissed by hand; the backup rule and push unchanged; action: install.sh, no drain; rollback: the previous release)
// - the appended section (sync.py --continue refuses a conflicted file whose staged result drops a line both parents, HEAD and MERGE_HEAD, carry: trailing whitespace stripped, blank lines ignored, order and repetition not counted, a file removed from the index read as empty; the file kept under conflicts with a note naming it, the count of distinct dropped lines and up to three of them, exit 1; a binary file, a NUL in its first 8000 bytes, and a file only one parent has not checked; the result's shape unchanged; the sync skill putting the lines back or asking the owner when dropping them is the resolution; action: install.sh, no drain; rollback: the previous release)
// upgrade-sections:end
// The file is only routed to, from the install ops page and the README
// landing page.
func TestW254UpgradeByteIdentical(t *testing.T) {
	raw := []byte(w254Read(t, "docs/upgrade.md"))
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != "e56c43a1845718aa937deca31c37faebe0123c0f2d96e348eaec1536b980a38f" {
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
