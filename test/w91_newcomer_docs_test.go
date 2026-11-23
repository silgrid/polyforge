// carries: docs/getting-started.md, docs/zh.md, docs/install.md, docs/quickstart-macos.md, docs/quickstart-developers.md, docs/user-guide.md, README.md, skills/polyforge/verbs/help.md, skills/polyforge/verbs/init.md, internal/polyforge/cli/doctor_display.go, .polyforge/verify-scope.conf

package test

// w91_newcomer_docs_test.go — polyforge#166 (epic #91, unit 91-docs) e2e
// acceptance scenarios for the newcomer mainline documentation: one English
// page that walks a reader from Docker through the workstation image and the
// first boot to a first submitted work item and the upgrade entry, with every
// other entry pointing at it and the old host paths labelled for who they
// serve. Authored against the ticket and its settled rulings alone, BEFORE
// the page and the pointers exist: every assertion below is red on the
// pre-implementation tree (the page is absent, no entry names it, the old
// pages carry no mode label) and turns green only on the delivered change.
//
// Assertions are on the ticket's pinned facts — the teaching order, the
// platform range, the placeholder registry, the entry pointers and the mode
// labels — never on wording; the prose-level halves (the reader path, the
// "only one mainline" discipline, the honesty of the unverified platforms)
// are judged by review against the same ticket.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW91' -count=1 -v

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// w91Root answers the repo root (this package's cwd is the repo's test/ dir).
func w91Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// w91Read answers a repo-relative file's body, failing on the named path.
func w91Read(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w91Root(t), rel))
	if err != nil {
		t.Fatalf("polyforge#166: %s is part of the deliverable and the repo must carry it: %v", rel, err)
	}
	return string(raw)
}

// w91Norm answers the whitespace-normalized body (a wrapped sentence still
// pairs; the w218/w241 convention).
func w91Norm(body string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
}

// w91Headings answers the page's '## ' headings in document order.
func w91Headings(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	return out
}

// w91Section answers the body of the first '## ' section whose heading
// carries kw (case-insensitive), from that heading line to the next '## '
// heading line. A missing section fails loudly: the assertion's subject must
// exist.
func w91Section(t *testing.T, body, kw string) string {
	t.Helper()
	var sec []string
	in := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = regexp.MustCompile(`(?i)\b` + kw + `\b`).MatchString(line)
			continue
		}
		if in {
			sec = append(sec, line)
		}
	}
	if !in {
		t.Fatalf("polyforge#166: the page needs a '## ' section carrying %q — it is an assertion's subject", kw)
	}
	return strings.Join(sec, "\n")
}

// w91PlaceholderRe matches one delivery placeholder's angle-bracket form.
var w91PlaceholderRe = regexp.MustCompile(`<PF_[A-Z_]+>`)

// w91Placeholders answers the distinct placeholder tokens in body, in
// document order.
func w91Placeholders(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range w91PlaceholderRe.FindAllString(body, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// TestW91NewcomerMainline: the mainline page exists, is English, and teaches
// the newcomer path in the order the system actually runs — Docker, the
// image pull, compose up, the first boot, the first work item, the upgrade
// entry — linking the workstation page for every mechanism detail rather
// than restating it, and naming whether a command runs on the host or inside
// the container.
func TestW91NewcomerMainline(t *testing.T) {
	body := w91Read(t, "docs/getting-started.md")
	if len(body) < 1500 {
		t.Fatalf("polyforge#166: docs/getting-started.md is the newcomer mainline and must be a real page, not a stub (%d bytes)", len(body))
	}
	nonASCII, total := 0, 0
	for _, r := range body {
		total++
		if r > 0x7f {
			nonASCII++
		}
	}
	if nonASCII*4 > total {
		t.Fatalf("the mainline page is English-only (the ticket's non-goal: no bilingual page); %d of %d runes are non-ASCII", nonASCII, total)
	}
	heads := w91Headings(body)
	at := -1
	for _, step := range []struct{ what, re string }{
		{"Docker (the host prerequisite)", `(?i)docker`},
		{"the image pull", `(?i)\b(image|pull)\b`},
		{"compose up", `(?i)compose`},
		{"the first boot", `(?i)first\s+boot`},
		{"the first work item", `(?i)work\s+item`},
		{"the upgrade entry", `(?i)upgrad`},
	} {
		found := -1
		for i := at + 1; i < len(heads); i++ {
			if regexp.MustCompile(step.re).MatchString(heads[i]) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("the mainline teaches the newcomer path in the order the system runs — Docker, image pull, compose up, first boot, first work item, upgrade — but no '## ' heading after %d teaches %s:\n%s", at, step.what, strings.Join(heads, "\n"))
		}
		at = found
	}
	norm := w91Norm(body)
	for _, want := range []string{
		"workstation.md#the-first-boot",
		"workstation.md#harness-login",
		"workstation.md#in-place-upgrade",
		"user-guide.md",
	} {
		if !strings.Contains(norm, want) {
			t.Fatalf("the mainline links the surface that owns each mechanism, never restating it — it never links %s", want)
		}
	}
	// A command's execution boundary is named: the page says which steps run
	// on the host and which inside the container.
	if !regexp.MustCompile(`(?i)inside the container`).MatchString(norm) || !regexp.MustCompile(`(?i)\bhost\b`).MatchString(norm) {
		t.Fatalf("the mainline says whether each command runs on the host or inside the container — it never names both boundaries")
	}
}

// TestW91SupportedPlatforms: the platform range is stated honestly — Linux is
// the verified platform, the workstation image is unverified on macOS and on
// Windows, no Windows support is promised, and the macOS host path still
// points at its own Quickstart.
func TestW91SupportedPlatforms(t *testing.T) {
	body := w91Read(t, "docs/getting-started.md")
	norm := w91Norm(w91Section(t, body, "platform"))
	if !regexp.MustCompile(`(?i)\blinux\b`).MatchString(norm) || !regexp.MustCompile(`(?i)\bverified\b`).MatchString(norm) {
		t.Fatalf("the platform section must name Linux as the verified platform:\n%s", norm)
	}
	for _, name := range []string{"macOS", "Windows"} {
		if !regexp.MustCompile(`(?i)\b` + name + `\b`).MatchString(norm) {
			t.Fatalf("the platform section must name %s as unverified for the workstation image:\n%s", name, norm)
		}
	}
	if !regexp.MustCompile(`(?i)\bnot\s+verified\b|\bunverified\b`).MatchString(norm) {
		t.Fatalf("the platform section states macOS and Windows are not verified — it never says so:\n%s", norm)
	}
	if regexp.MustCompile(`(?i)windows\s+(?:is|are)\s+supported|supports?\s+windows`).MatchString(norm) {
		t.Fatalf("the platform section must make no Windows support promise:\n%s", norm)
	}
	if !strings.Contains(norm, "quickstart-macos.md") {
		t.Fatalf("the macOS host path stays supported and links its own Quickstart — the platform section never links quickstart-macos.md:\n%s", norm)
	}
}

// w91Placeholder is one entry of the ticket's own placeholder registration
// (polyforge#166 Q1: "W91 测试登记位置清单"): the token, the page section
// that uses it, and how many times that section carries it.
type w91Placeholder struct {
	token   string
	section string
	count   int
}

// w91PlaceholderRegistry is the ticket's registration, written here and
// independent of the page under test: each delivery placeholder in the
// section that uses it, with its count. #86 lands the real assets and a
// later unit empties this table together with the page's registry — the
// ticket pins no "at least one placeholder" rule.
var w91PlaceholderRegistry = []w91Placeholder{
	{"<PF_IMAGE>", "pull", 1},
	{"<PF_IMAGE>", "compose", 1},
	{"<PF_COMPOSE_URL>", "compose", 1},
	{"<PF_SECCOMP_URL>", "compose", 1},
}

// TestW91PlaceholderInventory: until polyforge#86 publishes the delivery the
// page carries exactly the registered placeholders, in the registered
// sections, the registered number of times; a placeholder never stands in
// for a `pf` command (that would read as a runnable verb the CLI does not
// have).
func TestW91PlaceholderInventory(t *testing.T) {
	body := w91Read(t, "docs/getting-started.md")
	for _, want := range w91PlaceholderRegistry {
		sec := w91Section(t, body, want.section)
		if got := strings.Count(sec, want.token); got != want.count {
			t.Fatalf("polyforge#166 Q1: %s must appear %d time(s) in the %q section, found %d:\n%s", want.token, want.count, want.section, got, sec)
		}
	}
	// No unregistered token: the page's usage outside its own registry table
	// is exactly the registered set.
	usage := strings.Replace(body, w91Section(t, body, "placeholder"), "", 1)
	registered := map[string]bool{}
	for _, want := range w91PlaceholderRegistry {
		registered[want.token] = true
	}
	var unregistered []string
	for _, tok := range w91Placeholders(usage) {
		if !registered[tok] {
			unregistered = append(unregistered, tok)
		}
	}
	if len(unregistered) > 0 {
		t.Fatalf("polyforge#166 Q1: the mainline carries unregistered placeholder(s) %v — register each in w91PlaceholderRegistry (and the page's registry table) or drop it", unregistered)
	}
	// The page's own registry table names every registered token.
	table := w91Section(t, body, "placeholder")
	for _, tok := range w91Placeholders(usage) {
		if !strings.Contains(table, "`"+tok+"`") {
			t.Fatalf("the page's placeholder registry must list %s", tok)
		}
	}
	// A placeholder is a delivery value, never a pf verb or flag.
	if m := regexp.MustCompile(`(?m)\bpf\s+[^\n]*<PF_`).FindString(body); m != "" {
		t.Fatalf("a placeholder must never stand in a `pf` command — it reads as a runnable verb the CLI does not have: %q", m)
	}
}

// w91BeforeFirstH2 answers the page's body up to its first '## ' heading:
// the H1 and the opening paragraphs, where a reader starts.
func w91BeforeFirstH2(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestW91EntryPointersAndModes: every entry points at the mainline — the
// README's Try it and Documentation list, docs/zh.md's routing table, the
// two host pages labelled as the host installation, the developer page
// labelled as the from-source path, and the user guide's opening — and every
// relative link resolves to the real page.
func TestW91EntryPointersAndModes(t *testing.T) {
	readme := w91Read(t, "README.md")
	try := w91Section(t, readme, "try")
	norm := w91Norm(try)
	if !strings.Contains(norm, "docs/getting-started.md") {
		t.Fatalf("README's Try it section must point at the mainline page (docs/getting-started.md):\n%s", norm)
	}
	// The from-zero tutorial moved to the mainline page; the README no longer
	// restates it.
	if regexp.MustCompile(`pf\s+file\s+create`).MatchString(norm) {
		t.Fatalf("README's Try it section must not restate the from-zero tutorial (pf file create belongs on the mainline page):\n%s", norm)
	}
	docs := w91Norm(w91Section(t, readme, "documentation"))
	if !strings.Contains(docs, "docs/getting-started.md") {
		t.Fatalf("README's Documentation list must name the mainline page (docs/getting-started.md):\n%s", docs)
	}
	if !strings.Contains(w91Norm(w91Read(t, "docs/zh.md")), "getting-started.md") {
		t.Fatalf("docs/zh.md's routing table must route the newcomer mainline to getting-started.md")
	}

	for _, c := range []struct{ rel, label string }{
		{"docs/install.md", "Host installation"},
		{"docs/quickstart-macos.md", "Host installation"},
		{"docs/quickstart-developers.md", "from source"},
	} {
		top := w91Norm(w91BeforeFirstH2(w91Read(t, c.rel)))
		if !regexp.MustCompile(`(?i)` + c.label).MatchString(top) {
			t.Fatalf("%s must open labelled %q — the reader has to know which path the page installs:\n%s", c.rel, c.label, top)
		}
		if !strings.Contains(top, "getting-started.md") {
			t.Fatalf("%s must point the image newcomer at the mainline page (getting-started.md):\n%s", c.rel, top)
		}
	}

	// Every pointer resolves to the real page under its own directory.
	for _, c := range []struct{ rel, target string }{
		{"README.md", "docs/getting-started.md"},
		{"docs/zh.md", "getting-started.md"},
		{"docs/install.md", "getting-started.md"},
		{"docs/quickstart-macos.md", "getting-started.md"},
		{"docs/quickstart-developers.md", "getting-started.md"},
		{"docs/user-guide.md", "getting-started.md"},
	} {
		dir := filepath.Dir(filepath.Join(w91Root(t), c.rel))
		if _, err := os.Stat(filepath.Join(dir, c.target)); err != nil {
			t.Fatalf("%s's link %q must resolve to a real page: %v", c.rel, c.target, err)
		}
	}
}

// TestW91UserGuideStart: the user guide starts where the new reader starts —
// its opening paragraph names the workstation image and links the mainline —
// while the Install section is labelled as the host installation for a
// machine joining an existing team (polyforge#166 Q6).
func TestW91UserGuideStart(t *testing.T) {
	body := w91Read(t, "docs/user-guide.md")
	open := w91Norm(w91BeforeFirstH2(body))
	if !strings.Contains(open, "getting-started.md") {
		t.Fatalf("the user guide's opening paragraph must link the workstation image mainline (getting-started.md):\n%s", open)
	}
	if !regexp.MustCompile(`(?i)\bimage\b`).MatchString(open) {
		t.Fatalf("the user guide's opening names the workstation image as the entry:\n%s", open)
	}
	if regexp.MustCompile(`tools/install\.sh|curl\s+-`).MatchString(open) {
		t.Fatalf("the guide's opening must not be the host install one-liner — that stays under the host installation section:\n%s", open)
	}
	install := w91Norm(w91Section(t, body, "install"))
	if !regexp.MustCompile(`(?i)\bhost\b`).MatchString(install) {
		t.Fatalf("the Install section must say it is the host-installed path (a machine joining an existing team):\n%s", install)
	}
	if !strings.Contains(install, "getting-started.md") {
		t.Fatalf("the Install section must point the image newcomer at the mainline page:\n%s", install)
	}
}

// TestW91SkillAndDoctorPointers: the routing skill's help and init fragments
// point a newcomer at the mainline page (the CLI's `pf help` stays, Q3).
// The doctor's pi-package hint (Q2) is behaviour, not a source string: the
// cli package's TestPiPackageCheckNamesBothModes drives the check and
// asserts its Detail names both install modes, so this test does not grep
// the implementation.
func TestW91SkillAndDoctorPointers(t *testing.T) {
	for _, rel := range []string{"skills/polyforge/verbs/help.md", "skills/polyforge/verbs/init.md"} {
		body := w91Norm(w91Read(t, rel))
		if !strings.Contains(body, "docs/getting-started.md") {
			t.Fatalf("%s must point a newcomer at the mainline page (docs/getting-started.md)", rel)
		}
	}
	// Q3: the CLI's pf help is untouched, so the skill's help fragment keeps
	// naming the daily-use guide (the discovery chain chat → skill → help →
	// guide).
	if !strings.Contains(w91Norm(w91Read(t, "skills/polyforge/verbs/help.md")), "docs/user-guide.md") {
		t.Fatalf("the skill help fragment must keep the daily-use guide pointer (docs/user-guide.md); Q3 leaves the CLI pf help unchanged")
	}
}

// TestW91WebWorkbenchMode: the README's Web workbench section says Pi Web
// ships with the GitHub Release and is installed by the installer's
// --with-pi-web; it must also name the workstation image as the other path
// that carries Pi Web, so a newcomer on the image does not read the host
// installer's step as theirs (polyforge#166 计划审补充 3: the README
// Web-workbench paragraph gains the host/image distinction).
func TestW91WebWorkbenchMode(t *testing.T) {
	sec := w91Norm(w91Section(t, w91Read(t, "README.md"), "web workbench"))
	if !regexp.MustCompile(`(?i)workstation image`).MatchString(sec) {
		t.Fatalf("README's Web workbench section must name the workstation image as the path that carries Pi Web (the installer's --with-pi-web is the host path):\n%s", sec)
	}
	if !regexp.MustCompile(`(?i)\bhost\b`).MatchString(sec) {
		t.Fatalf("README's Web workbench section must label --with-pi-web as the host path:\n%s", sec)
	}
}
