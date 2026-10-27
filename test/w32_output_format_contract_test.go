package test

// w32_output_format_contract_test.go: text contract for workbench#32.
//
// Work item #32 ships a standalone output-format skill (skills/output-format/
// SKILL.md): output discipline for the three owner-facing surfaces — the
// session face, the grill-family needs_input envelope face, and the
// panel face (the session agent's status restatement) — derived from
// the five cognitive facts, with a Bad/Good pair per surface, a pre-send
// first/last-line check and a whole-session persistence clause. The
// change is prompt material (one new
// skill document plus the package/install plumbing that loads it): no
// endpoint, verb, flag or protocol moves, so the only mechanical contract is
// the shipped text itself.
//
// This test pins that text. It is red on the base tree (the file does not
// exist) and green only once the skill shipped. The qualitative half — that
// a real recent session fragment obeys the session-face rules — is walked
// through in the capsule's code summary and observed on real WIs, the same
// way workbench#29 and #30 handled their non-machine-verifiable residuals.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w32Frontmatter answers the YAML block between the leading `---` fences of
// an Agent-Skills document.
func w32Frontmatter(t *testing.T, s string) string {
	t.Helper()
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		t.Fatalf("workbench#32: SKILL.md must open with a frontmatter fence")
	}
	fm, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("workbench#32: SKILL.md frontmatter is not closed")
	}
	return fm
}

// w32Section answers the body from `## <title>` up to the next `## ` heading
// (or end of file).
func w32Section(s, title string) string {
	needle := "## " + title
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	rest := s[i+len(needle):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}

func TestW32OutputFormatSkillContract(t *testing.T) {
	root := v1Root(t)
	rel := filepath.Join("skills", "output-format", "SKILL.md")
	if strings.HasPrefix(rel, filepath.Join("skills", "polyforge")+string(os.PathSeparator)) {
		t.Fatalf("workbench#32: the standalone skill must live outside the routing skill directory, got %s", rel)
	}
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("workbench#32: the standalone output-format skill must exist at %s: %v", rel, err)
	}
	s := string(b)
	// Markdown wraps lines; marker checks compare on collapsed whitespace.
	flat := strings.Join(strings.Fields(s), " ")

	// It is independent: the routing skill body must not absorb its content.
	routing, rerr := os.ReadFile(filepath.Join(root, "skills", "polyforge", "SKILL.md"))
	if rerr != nil {
		t.Fatalf("workbench#32: cannot read the routing skill to prove independence: %v", rerr)
	}
	if strings.Contains(string(routing), "off-screen is nonexistent") {
		t.Fatalf("workbench#32: the output-format content leaked into skills/polyforge/SKILL.md — the two skills must load independently")
	}

	// Frontmatter: the name the release declares, and a description that is
	// present and non-empty (Agent Skills legality).
	fm := w32Frontmatter(t, s)
	if !strings.Contains(fm, "name: output-format") {
		t.Errorf("workbench#32: frontmatter must declare `name: output-format`")
	}
	desc := ""
	for _, line := range strings.Split(fm, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "description:"); ok {
			desc = strings.TrimSpace(v)
		}
	}
	if strings.Trim(desc, `"'`) == "" {
		t.Errorf("workbench#32: frontmatter must carry a non-empty description")
	}
	// The description must name all three surfaces it governs.
	for _, want := range []string{"session face", "envelope face", "panel face"} {
		if !strings.Contains(strings.ToLower(desc), want) {
			t.Errorf("workbench#32: the description must name the %s it governs", want)
		}
	}

	// The five cognitive facts are the derivation basis.
	for _, want := range []string{
		"off-screen is nonexistent",           // 1. working memory is small
		"Knowing is not doing",                // 2. knowing != doing
		"got-it → done-it",                    // 2. the friction, stated
		"Starting is the most expensive step", // 3. starting is costly
		"a vague estimate is no estimate",     // 4. time perception distorts
		"Progress must be visible",            // 5. progress must be visible
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("workbench#32: the skill must carry the cognitive-fact marker %q", want)
		}
	}

	// Three surfaces, each with a Bad/Good pair in this domain.
	for _, face := range []string{"Session face", "Envelope face", "Panel face"} {
		sec := w32Section(s, face)
		if sec == "" {
			t.Errorf("workbench#32: the skill must carry a `## %s` section", face)
			continue
		}
		for _, marker := range []string{"**Bad**", "**Good**"} {
			if !strings.Contains(sec, marker) {
				t.Errorf("workbench#32: the %s section must carry a %s example pair", face, marker)
			}
		}
	}

	// The envelope face is the existing four-column precedent, calibrated,
	// not a new shape.
	if !strings.Contains(flat, "| 问题 | 推荐选项 | 理由 | 重点关注程度 |") {
		t.Errorf("workbench#32: the envelope face must carry the four-column `| 问题 | 推荐选项 | 理由 | 重点关注程度 |` table")
	}
	// The panel face is current step / next step / the one human action.
	if !strings.Contains(flat, "current step / next step / the one thing only a human can do") {
		t.Errorf("workbench#32: the panel face must state the current step / next step / one human action shape")
	}

	// Common structural rules the work item named.
	for _, want := range []string{
		"At most 10 one-line bullets",
		"cause and fix",
		"conditional menu",
		"needs_input",
		"Ban preamble, process recap and pleasantry sign-off",
		"Restate state, not process",
		"Ban binary contrast",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("workbench#32: the skill must carry the common-rule marker %q", want)
		}
	}

	// Owner-ruled content the work item ordered absorbed (workbench#32
	// review B5): the dual-track ban, the table-retention clause, and the
	// binary-contrast Bad/Good pair across 中/EN word orders.
	ownerRuled := []string{
		// ruling 2: plain words the first time; the 技术版 + 人话版 double
		// structure is abolished
		"No dual track",
		"技术版 + 人话版",
		"plain words the first time",
		// ruling 3: structured tables stay; only decorative prose lists are banned
		"Structured data keeps its table",
		"状态板",
		"对照表",
		"决策表",
		// binary contrast banned in every word order (中/EN)
		"不是X而是Y",
		"X，而不是Y",
		"与其说X不如说Y",
		"It's not X — it's Y",
		// the Bad/Good pair
		"这不是升级，而是迁移",
		"这是迁移（顺带升级）",
		"This is intended behavior.",
	}
	for _, want := range ownerRuled {
		if !strings.Contains(flat, want) {
			t.Errorf("workbench#32: the skill must carry the owner-ruled marker %q", want)
		}
	}

	// Pre-send first/last-line check.
	check := w32Section(s, "Pre-send check")
	if !strings.Contains(check, "first and last lines") {
		t.Errorf("workbench#32: the pre-send check must be the first/last-line test — a reader seeing only those two lines knows what happened and what to do")
	}

	// Persistence clause: whole session, survives a topic switch, ends only
	// on an explicit close phrase.
	persist := w32Section(s, "Persistence")
	for _, want := range []string{"whole session", "topic switch", "close phrase"} {
		if !strings.Contains(persist, want) {
			t.Errorf("workbench#32: the persistence clause must state %q", want)
		}
	}

	// Attribution for the four inspirations (MIT / open sources; text rewritten).
	for _, want := range []string{"ayghri/i-have-adhd", "hexiecs/talk-normal", "hardikpandya/stop-slop", "openai/codex"} {
		if !strings.Contains(flat, want) {
			t.Errorf("workbench#32: the credits must name the inspiration %q", want)
		}
	}

	// Independent pi loading (workbench#32 review N2): the release
	// package.json must declare the new skill, or pi never loads it.
	pkg, perr := os.ReadFile(filepath.Join(root, "package.json"))
	if perr != nil {
		t.Fatalf("workbench#32: cannot read package.json to prove pi loads the skill: %v", perr)
	}
	var manifest struct {
		Pi struct {
			Skills []string `json:"skills"`
		} `json:"pi"`
	}
	if err := json.Unmarshal(pkg, &manifest); err != nil {
		t.Fatalf("workbench#32: package.json is not valid JSON: %v", err)
	}
	found := false
	for _, s := range manifest.Pi.Skills {
		if s == "./skills/output-format" {
			found = true
		}
	}
	if !found {
		t.Errorf("workbench#32: package.json pi.skills must declare ./skills/output-format (got %v)", manifest.Pi.Skills)
	}
}
