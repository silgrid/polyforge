package test

// w241_user_guide_test.go — polyforge#241 e2e acceptance scenarios for
// the user guide (artifacts/spec.md, "## Requirements" / "## Acceptance
// criteria"): docs/user-guide.md — the daily-use guide for a team member
// joining an already-running aihub — its three discovery pointers
// (README.md, `pf help extensions`, skills/polyforge/verbs/help.md) and
// the repo-suite drift check over it. Authored against the spec alone,
// BEFORE the guide exists: every assertion below is red on the
// pre-implementation tree (the guide is absent, no pointer names it, no
// drift check runs) and turns green only on the delivered guide.
// Assertions are on spec-pinned tokens and orders — never on wording;
// the prose-level halves of the criteria (exactly-one concept
// definitions, primary-vs-reference framing, the README diff's shape)
// are judged by review against the same spec (artifacts/e2e-scenarios.md).
//
// The drift-check scenario drives the repo's own test gate — the real
// interface — over a planted guide revision in a throwaway tree copy
// (never the real tree): a guide naming `pf frobnicate` (a verb `pf`
// help does not know) and a link to no file must FAIL it, naming each;
// the restored delivered guide must PASS it.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// w241GuidePath is the guide's repo-relative path (the ./test package's
// cwd is the repo's test/ dir).
var w241GuidePath = filepath.Join("..", "docs", "user-guide.md")

// w241ReadGuide answers the guide's raw body, failing on the deliverable
// itself when absent or a stub — every guide-content scenario's first
// assertion.
func w241ReadGuide(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(w241GuidePath)
	if err != nil {
		t.Fatalf("polyforge#241: docs/user-guide.md is the deliverable — the daily-use guide for a team member joining an already-running aihub — and the repo must carry it: %v", err)
	}
	if len(raw) < 100 {
		t.Fatalf("polyforge#241: docs/user-guide.md is a stub (%d bytes): a guide that carries a reader from the install one-liner to daily use is a document, not a placeholder", len(raw))
	}
	return string(raw)
}

// w241Norm answers the whitespace-normalized body (a wrapped sentence
// still pairs; test/w218_readme_docs_test.go's convention).
func w241Norm(body string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
}

// w241StripFences answers the body with fenced code blocks removed
// (``` … ``` lines inclusive): the flag-table scan must not see shell
// text inside an example block.
func w241StripFences(body string) string {
	var out []string
	in := false
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			in = !in
			continue
		}
		if !in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// w241Section answers the guide's one '## ' section whose heading carries
// kw (a spec-pinned token), whitespace-normalized: the section's body from
// that heading line to the next '## ' heading line. The order assertions
// run inside their own section — a whole-body scan passed on the same
// words landing anywhere before the section (review round 1's
// vacuous-assertion finding; the scoped slice is the approved fix). A
// missing section fails loudly: the assertion's subject must exist.
func w241Section(t *testing.T, raw, kw string) string {
	t.Helper()
	var sec []string
	in := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break // the next '## ' heading ends the slice
			}
			in = regexp.MustCompile(`(?i)\b` + kw + `\b`).MatchString(line)
			continue // the heading line itself is not the section's body
		}
		if in {
			sec = append(sec, line)
		}
	}
	if !in {
		t.Fatalf("polyforge#241: the guide's %s section is an order assertion's subject and must exist: no '## ' heading carries %q", kw, kw)
	}
	return w241Norm(strings.Join(sec, "\n"))
}

// TestW241GuideExistsForNewMember: docs/user-guide.md exists, in
// English, written for a team member joining an already-running aihub —
// starting where that reader starts, at the install one-liner (they have
// an API key and the aihub host and nothing else).
func TestW241GuideExistsForNewMember(t *testing.T) {
	body := w241ReadGuide(t)
	nonASCII, total := 0, 0
	for _, r := range body {
		total++
		if r > 0x7f {
			nonASCII++
		}
	}
	if nonASCII*4 > total {
		t.Fatalf("the guide is English-only (the spec's non-goal: no Chinese or bilingual guide); %d of %d runes are non-ASCII", nonASCII, total)
	}
	m := regexp.MustCompile(`(?i)install`).FindStringIndex(body)
	if m == nil || m[0] > len(body)*3/10 {
		at := -1
		if m != nil {
			at = m[0]
		}
		t.Fatalf("the guide starts at the install one-liner — the reader has an API key and the aihub host and nothing else; the first \"install\" mention sits at byte %d of %d", at, len(body))
	}
}

// TestW241GuideFlowRealOrder: the flow section teaches the order the
// system actually runs — pf file → pf submit → grill questions (which
// arrive after submit) answered via pf answer → pf watch → the lane's
// work steps → the workflow's own deliver step (PR/merge) — never "grill
// before submit" and never "wrap completes the workflow". The scan runs
// inside the flow section's own slice (w241Section): a whole-body scan
// passed on the same words landing anywhere before the section (review
// round 1's vacuous-assertion finding; the scoped slice is the approved
// fix).
func TestW241GuideFlowRealOrder(t *testing.T) {
	sec := w241Section(t, w241ReadGuide(t), "flow")
	at := 0
	for _, step := range []struct{ label, re string }{
		{"pf file", `(?i)\bfile`},
		{"pf submit", `(?i)\bsubmit`},
		{"grill questions answered via pf answer", `(?i)\banswer`},
		{"pf watch", `(?i)\bwatch`},
		{"the workflow's own deliver step (PR/merge)", `(?i)\bdeliver`},
	} {
		m := regexp.MustCompile(step.re).FindStringIndex(sec[at:])
		if m == nil {
			t.Fatalf("the flow section must teach the real order — pf file → pf submit → grill questions (which arrive after submit) answered via pf answer → pf watch → the lane's work steps → deliver (PR/merge) — never \"grill before submit\" and never \"wrap completes the workflow\": no %s after byte %d of the flow section", step.label, at)
		}
		at += m[1]
	}
}

// TestW241GuideWrapHumanCloseVerb: pf wrap is taught as the human close
// verb — closing a bundle-less ticket (design/decision/tracking, with
// --note / --fail) and recording the owner's fail verdict on a live run
// — never as the workflow's completion step (that rides deliver).
func TestW241GuideWrapHumanCloseVerb(t *testing.T) {
	norm := w241Norm(w241ReadGuide(t))
	if !regexp.MustCompile(`(?i)\bpf\s+wrap\b`).MatchString(norm) {
		t.Fatalf("the guide must teach pf wrap — the human close verb: closing a bundle-less ticket and recording the owner's fail verdict on a live run")
	}
	if !regexp.MustCompile(`(?i)bundle[\s-]?less`).MatchString(norm) {
		t.Fatalf("pf wrap is the human close verb for a bundle-less ticket (design/decision/tracking, with --note / --fail); the guide never names the bundle-less ticket it closes")
	}
	if !regexp.MustCompile(`--note\b|--fail\b`).MatchString(norm) {
		t.Fatalf("pf wrap's human-close moves show its flags (--note for the close note, --fail for the owner's fail verdict); the guide names neither")
	}
}

// TestW241GuideCoreConceptsNamed: every core concept — work item,
// bundle, lane, gate, rewind, capsule, chat identity — is defined in the
// guide (exactly one plain-words definition plus a pointer to the
// mechanism authority; the once-and-linked half is review's against the
// same spec).
func TestW241GuideCoreConceptsNamed(t *testing.T) {
	norm := w241Norm(w241ReadGuide(t))
	var missing []string
	for _, c := range []struct{ concept, re string }{
		{"work item", `(?i)work\s+item`},
		{"bundle", `(?i)\bbundle`},
		{"lane", `(?i)\blane`},
		{"gate", `(?i)\bgate`},
		{"rewind", `(?i)\brewind`},
		{"capsule", `(?i)\bcapsule`},
		{"chat identity", `(?i)chat\s+identity`},
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			missing = append(missing, c.concept)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("every core concept gets exactly one plain-words definition in the guide plus a link to the mechanism authority; the guide never names: %s", strings.Join(missing, ", "))
	}
}

// TestW241GuideChatPathPrimary: the chat-agent path is the guide's
// primary path — ask in natural language in the harness (pi or Claude
// Code, progress display included) and let the routing skill map the
// words to the verb; the terminal pf verbs are the reference form.
func TestW241GuideChatPathPrimary(t *testing.T) {
	norm := w241Norm(w241ReadGuide(t))
	for _, c := range []struct{ what, re string }{
		{"asks in natural language", `(?i)natural\s+language`},
		{"the pi harness", `(?i)\bpi\b`},
		{"the Claude Code harness", `(?i)claude\s+code`},
		{"the routing skill", `(?i)\bskill`},
		{"progress display (pf watch --statusline / the pf-chat extension)", `(?i)statusline`},
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			t.Fatalf("the chat-agent path is the guide's primary instruction — ask in natural language in the harness and let the routing skill route, covering both harnesses the skill installs into and the progress display; the guide never shows %s", c.what)
		}
	}
}

// TestW241GuideWorkedExampleAutopilot: one worked example, zero to
// delivered, explicitly labeled composed/illustrative (never a
// transcript of a real run), walking the autopilot lane end to end
// (grill if asked → fix → verification → deliver) with real command
// shapes, plus a sidebar naming what the feature-full bundle adds
// around the fix.
func TestW241GuideWorkedExampleAutopilot(t *testing.T) {
	norm := w241Norm(w241ReadGuide(t))
	if !regexp.MustCompile(`(?i)\b(composed|illustrative)\b`).MatchString(norm) {
		t.Fatalf("the worked example must be explicitly labeled composed/illustrative — never a transcript of a real run (step agents must not drive the daemon)")
	}
	if !regexp.MustCompile(`(?i)\bautopilot\b`).MatchString(norm) {
		t.Fatalf("the worked example walks the autopilot lane end to end (grill if asked → fix → verification → deliver); the guide never names the lane")
	}
	// the lane order runs inside the worked example's own slice
	// (w241Section): a whole-body scan passed on the same words landing
	// anywhere before the example (review round 1's vacuous-assertion
	// finding; the scoped slice is the approved fix). The presence checks
	// stay whole-body.
	sec := w241Section(t, w241ReadGuide(t), "worked example")
	at := 0
	for _, step := range []struct{ label, re string }{
		{"the fix step", `(?i)\bfix`},
		{"verification", `(?i)\bverification`},
		{"deliver (PR/merge)", `(?i)\bdeliver`},
	} {
		m := regexp.MustCompile(step.re).FindStringIndex(sec[at:])
		if m == nil {
			t.Fatalf("the worked example follows the autopilot lane end to end — grill (if asked) → fix → verification → deliver; no %s after byte %d of the worked example section", step.label, at)
		}
		at += m[1]
	}
	if !regexp.MustCompile(`(?i)feature[\s-]?full`).MatchString(norm) {
		t.Fatalf("the worked example's sidebar names what the feature-full bundle adds around the fix (the clarification pass — grill/spec — plus e2e-author and review); the guide never names the feature-full bundle")
	}
	if !regexp.MustCompile(`(?i)\breview\b`).MatchString(norm) {
		t.Fatalf("the worked example's sidebar names the added steps (grill as a full clarification pass with spec, e2e-author, review); the guide never names review")
	}
}

// TestW241GuideMemoryAndRecoveryMoves: memory is one of the three core
// pillars (pf recall before starting work, pf remember for durable
// lessons) and the when-things-go-wrong moves are human (pf pause /
// pf resume / pf cancel / the blocked state, pf doctor as the
// diagnostic first stop); both have their own section.
func TestW241GuideMemoryAndRecoveryMoves(t *testing.T) {
	norm := w241Norm(w241ReadGuide(t))
	var missing []string
	for _, v := range []struct{ move, re string }{
		{"pf recall before starting work", `(?i)\brecall\b`},
		{"pf remember for durable lessons", `(?i)\bremember\b`},
		{"pf pause", `(?i)\bpause\b`},
		{"pf resume", `(?i)\bresume\b`},
		{"pf cancel", `(?i)\bcancel\b`},
		{"the blocked state", `(?i)\bblocked\b`},
		{"pf doctor as the diagnostic first stop", `(?i)\bdoctor\b`},
	} {
		if !regexp.MustCompile(v.re).MatchString(norm) {
			missing = append(missing, v.move)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the guide teaches memory (pf recall before starting work, pf remember for lessons) and the when-things-go-wrong human moves (pf pause / pf resume / pf cancel / blocked, pf doctor as the diagnostic first stop), each with its own section; missing: %s", strings.Join(missing, ", "))
	}
}

// TestW241GuideWhereToFindWhat: a where-to-find-what section maps each
// kind of question (install detail, a verb's shapes, why it works that
// way, upgrading) to the document that owns the answer.
func TestW241GuideWhereToFindWhat(t *testing.T) {
	norm := w241Norm(w241ReadGuide(t))
	for _, c := range []struct{ what, re string }{
		{"a where-to-find-what section", `(?i)where\s+to\s+find`},
		{"upgrading → docs/upgrade.md", `(?i)upgrad`},
		{"why it works that way → the design docs", `(?i)design\s+doc`},
	} {
		if !regexp.MustCompile(c.re).MatchString(norm) {
			t.Fatalf("the guide's where-to-find-what section maps each kind of question to the document that owns the answer (install detail → README, a verb's shapes → pf help, why → the design docs, upgrading → docs/upgrade.md); it never covers %s", c.what)
		}
	}
}

// TestW241GuideNoRestatedFlagTables: the guide is the layer above — it
// restates no flag table (and no per-verb usage line or mechanism
// detail); it links to the surface that owns them.
func TestW241GuideNoRestatedFlagTables(t *testing.T) {
	body := w241StripFences(w241ReadGuide(t))
	if m := regexp.MustCompile(`(?m)^[ \t]*\|.*--[a-z][a-z-]*`).FindString(body); m != "" {
		t.Fatalf("the guide restates no flag table — it links to the owning surface (README sections, skills/polyforge/verbs/*.md) — but a markdown table row carries a flag shape: %q", m)
	}
}

// TestW241ReadmeLinksGuide: README.md gains exactly one addition — a
// link to docs/user-guide.md as the daily-use guide (the "touches
// nothing else" half rides this work item's README diff, judged in
// review).
func TestW241ReadmeLinksGuide(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	re := regexp.MustCompile(`(?m)\]\([^)]*docs/user-guide\.md[^)]*\)|^\s{0,3}\[[^\]]+\]:\s*<?\s*docs/user-guide\.md`)
	if !re.MatchString(string(raw)) {
		t.Fatalf("README.md must link docs/user-guide.md as the daily-use guide (exactly one added link — no content moved, trimmed or rewritten); no link to docs/user-guide.md exists")
	}
}

// TestW241HelpExtensionsNamesGuide: a freshly-installed user exploring
// help meets the extensions topic, and it names the guide as the
// daily-use guide — the discovery chain chat → skill → help → guide.
func TestW241HelpExtensionsNamesGuide(t *testing.T) {
	bins := v1Bins(t)
	out, err := exec.Command(filepath.Join(bins, "pf"), "help", "extensions").Output()
	if err != nil {
		t.Fatalf("pf help extensions: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "docs/user-guide.md") {
		t.Fatalf("`pf help extensions` must name docs/user-guide.md as the daily-use guide (the discovery chain chat → skill → help → guide); its output carries no guide pointer:\n%s", body)
	}
	if !regexp.MustCompile(`(?i)daily[\s-]?use`).MatchString(body) {
		t.Fatalf("`pf help extensions` must name the guide as the daily-use guide, not merely list it; its output never says daily use:\n%s", body)
	}
}

// TestW241SkillHelpFragmentNamesGuide: the routing skill's help
// fragment — the other surface a freshly-installed user meets — names
// the guide as the daily-use guide too.
func TestW241SkillHelpFragmentNamesGuide(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "skills", "polyforge", "verbs", "help.md"))
	if err != nil {
		t.Fatalf("read skills/polyforge/verbs/help.md: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "docs/user-guide.md") {
		t.Fatalf("skills/polyforge/verbs/help.md must name the guide as the daily-use guide (a freshly-installed user meeting the routing skill's help intent lands here); no guide pointer exists:\n%s", body)
	}
	if !regexp.MustCompile(`(?i)daily[\s-]?use`).MatchString(body) {
		t.Fatalf("skills/polyforge/verbs/help.md must name the guide as the daily-use guide, not merely link it; the fragment never says daily use:\n%s", body)
	}
}

// TestW241GuideDriftCheckRedSide: the repo-suite drift check over
// docs/user-guide.md — it FAILS on a guide revision naming a pf verb pf
// help does not know (naming the drifted verb), FAILS on one carrying a
// dead relative link (naming the dead link), and PASSES on the delivered
// guide. Driven through the repo's own test gate, `go test -count=1
// ./internal/polyforge/cli/`, over a planted revision in a throwaway
// copy of the source tree (the drift check reads the repo-root guide of
// the tree under test); the real tree is never mutated.
func TestW241GuideDriftCheckRedSide(t *testing.T) {
	real, err := os.ReadFile(w241GuidePath)
	if err != nil {
		t.Fatalf("polyforge#241: docs/user-guide.md is the deliverable and the drift check's subject; the repo must carry it: %v", err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	tmp := t.TempDir()
	// the whole tree, .git excepted (copyTreeExceptGit): the drift check
	// resolves the guide's links against the repo root of the tree under
	// test, so a valid link anywhere in the tree (../workflows/,
	// ../tools/, ...) must resolve in the throwaway copy too — a four-dir
	// copy made valid links fail and forced the guide to drop them (review
	// round 1's finding; the whole-tree copy is the approved fix).
	copyTreeExceptGit(t, tmp, root)
	guide := filepath.Join(tmp, "docs", "user-guide.md")
	run := func() (int, string) {
		cmd := exec.Command("go", "test", "-count=1", "./internal/polyforge/cli/")
		cmd.Dir = tmp
		cmd.Env = append(os.Environ(), "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("go test -count=1 ./internal/polyforge/cli/ (throwaway tree): %v: %s", err, out)
			}
			return ee.ExitCode(), string(out)
		}
		return 0, string(out)
	}
	plant := func(extra string) {
		if err := os.WriteFile(guide, []byte(string(real)+extra), 0o644); err != nil {
			t.Fatalf("plant the drifted guide revision: %v", err)
		}
	}

	// a verb `pf help` does not know
	plant("\n\n<!-- w241 e2e drift plant: verb -->\n\nRun `pf frobnicate` to recalibrate the sockets.\n")
	if code, out := run(); code == 0 || !strings.Contains(out, "frobnicate") {
		t.Fatalf("the drift check must FAIL on a guide revision naming a pf verb that pf help does not know (pf frobnicate), naming the drifted verb: go test -count=1 ./internal/polyforge/cli/ over the planted revision exited %d and the output never names the drifted verb\n--- go test output:\n%s", code, out)
	}
	// a relative link that resolves to no file
	plant("\n\n<!-- w241 e2e drift plant: dead link -->\n\nSee [the missing handbook](no-such-doc-w241.md) for detail.\n")
	if code, out := run(); code == 0 || !strings.Contains(out, "no-such-doc-w241.md") {
		t.Fatalf("the drift check must FAIL on a guide revision carrying a relative link that resolves to no file (no-such-doc-w241.md), naming the dead link: go test -count=1 ./internal/polyforge/cli/ over the planted revision exited %d\n--- go test output:\n%s", code, out)
	}
	// the delivered guide, restored, passes
	if err := os.WriteFile(guide, real, 0o644); err != nil {
		t.Fatalf("restore the delivered guide: %v", err)
	}
	if code, out := run(); code != 0 {
		t.Fatalf("the drift check must PASS on the delivered guide (no drift planted): go test -count=1 ./internal/polyforge/cli/ exited %d\n--- go test output:\n%s", code, out)
	}
}

// copyTreeExceptGit copies the whole tree at src into dst, .git
// excepted: the drift scenario's throwaway tree must be the whole source
// tree (the drift check resolves the guide's links against the repo root
// of the tree under test, so a valid link anywhere in the tree must
// resolve in the copy too).
func copyTreeExceptGit(t *testing.T, dst, src string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read %s for the throwaway tree: %v", src, err)
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if e.IsDir() {
			if err := os.CopyFS(filepath.Join(dst, e.Name()), os.DirFS(filepath.Join(src, e.Name()))); err != nil {
				t.Fatalf("copy %s into the throwaway tree: %v", e.Name(), err)
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s for the throwaway tree: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), mustRead(t, filepath.Join(src, e.Name())), info.Mode().Perm()); err != nil {
			t.Fatalf("copy %s into the throwaway tree: %v", e.Name(), err)
		}
	}
}

// mustRead answers the file's bytes, failing on the named path.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}
