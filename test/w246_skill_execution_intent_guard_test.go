package test

// w246 (polyforge#246, bug-fix lane, step "repro"): the routing skill
// never loads for an execution intent. Reported incident (2026-10-20,
// gmi-ws): the owner directed code work in a pi session ("先升级 reverst
// 库" — upgrade the reverst library, quic-go v0.59→v0.64.1); the session
// agent hand-executed it end-to-end — a branch in .repo/ieops-reverst,
// the go.mod change, a 19-minute full test run, commit, push, PR
// GMISWE/ieops-reverst#61 — with no ticket until after the PR (the
// after-the-fact registration ticket ieops#1359 was refused bundle-less,
// CORE-409; bound to a bundle it was claimed and ran normally). Nothing
// the session had loaded said not to.
//
// The loading surface this guard reads is the shipped skill:
// release/current/skills/polyforge (the pi settings' packages entry →
// package.json → ./skills/polyforge; the ~/.claude/skills/polyforge
// symlink's target) — byte-identical to this repo tree's
// skills/polyforge. Per the pi skills contract, startup puts only the
// skill's name + description + path into the system prompt; the body
// (SKILL.md past the frontmatter) loads only when the task matches the
// description — and the description disclaims execution ("never touches
// the execution scene") and names no execution intent, so an execution
// intent matches nothing, the body never loads, and no loaded rule
// routes the work into the lifecycle.
//
// This guard encodes the contract the fix must deliver (the work item's
// §5.8 — the mechanical repro of the gap: red on the pre-fix tree, green
// only on the delivered skill):
//
//   0. the frontmatter parses as strict YAML and carries a non-empty
//      description (the fix step's amendment, owner-approved 2026-10-20:
//      pi parses the frontmatter with a strict yaml.parse and drops the
//      whole skill on a parse error — an unquoted description carrying
//      ": " is such an error, and it kept the skill out of every pi
//      session since v2.2.0; the original line-prefix reading could not
//      see it);
//   1. the parsed description stays within the Agent Skills limit — at
//      most 1024 characters AND at most 1024 bytes (both readings; the
//      YAML quoting is syntax, not part of the value);
//   2. the description advertises execution intents — the four markers
//      of the work item's final description text;
//   3. verbs/execute-intent.md exists and carries the routing
//      discipline's markers;
//   4. the SKILL.md body carries session rule 7's title.
//
// On the pre-fix tree (3a5fb96e) assertions 0 and 2–4 are red (0: "yaml:
// line 2: mapping values are not allowed in this context"); assertion 1
// is a guard that stays green across the fix.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// w246SkillRel and w246FragRel are the shipped skill surface's
// repo-relative paths (the ./test package's cwd is the repo's test/ dir;
// v1Root answers the repo root).
var (
	w246SkillRel = filepath.Join("skills", "polyforge", "SKILL.md")
	w246FragRel  = filepath.Join("skills", "polyforge", "verbs", "execute-intent.md")
)

// TestW246SkillExecutionIntentGuard is the mechanical repro of
// polyforge#246: the shipped routing skill's loading surface carries no
// execution-intent routing — the description (the only text a session
// loads unconditionally) advertises none, the fragment that would own
// the procedure is absent, and the standing rules stop at six.
func TestW246SkillExecutionIntentGuard(t *testing.T) {
	root := v1Root(t)

	raw, err := os.ReadFile(filepath.Join(root, w246SkillRel))
	if err != nil {
		t.Fatalf("polyforge#246: %s is the shipped routing skill — the loading surface both harnesses advertise (release/current → the pi package's skill, the claude symlink's target) — and the repo must carry it: %v", w246SkillRel, err)
	}
	skill := string(raw)

	// 0. the frontmatter parses as strict YAML with a non-empty
	// description — otherwise pi drops the skill and no session sees it.
	desc, err := w246FrontmatterDescription(skill)
	if err != nil {
		t.Fatalf("polyforge#246: %s's frontmatter must parse as strict YAML — pi parses it with yaml.parse and drops the whole skill on an error (a warning only), so no pi session advertises it; quote a description that contains \": \": %v", w246SkillRel, err)
	}
	if strings.TrimSpace(desc) == "" {
		t.Fatalf("polyforge#246: %s's frontmatter must carry a non-empty `description:` — the only part of the skill a session loads unconditionally (pi's package advertising and claude's skill advertising both show it)", w246SkillRel)
	}

	// 1. the description stays within the Agent Skills limit, both
	// readings (characters and bytes).
	if nb := len(desc); nb > 1024 {
		t.Errorf("polyforge#246: the description is %d bytes — over the Agent Skills 1024-byte limit; the execution-intent advertising must fit the limit", nb)
	}
	if nc := utf8.RuneCountInString(desc); nc > 1024 {
		t.Errorf("polyforge#246: the description is %d characters — over the Agent Skills 1024-character limit; the execution-intent advertising must fit the limit", nc)
	}

	// 2. the description advertises execution intents (the markers of
	// the work item's final description text): the intent is named, the
	// never-hand-execute discipline is stated, the routing verb pair is
	// given, the routing-vs-execution boundary is drawn.
	for _, tok := range []string{
		"execution intent",
		"never hand-executes",
		"pf file create --bundle",
		"routing, not execution",
	} {
		if !strings.Contains(desc, tok) {
			t.Errorf("polyforge#246: the description must advertise execution intents — the marker %q is missing; the description is the only text a session loads unconditionally, and without the marker an execution intent (\"upgrade the reverst library\") matches nothing, the body never loads and no loaded rule routes the work into the lifecycle", tok)
		}
	}

	// 3. the fragment that owns the execution-intent procedure exists and
	// carries the discipline's markers.
	frag, err := os.ReadFile(filepath.Join(root, w246FragRel))
	if err != nil {
		t.Errorf("polyforge#246: %s must exist — the fragment that owns the execution-intent routing procedure (lane choice, the required bundle, the never-hand-execute discipline); the intent table has no row to point at without it: %v", w246FragRel, err)
	} else {
		for _, tok := range []string{
			"which execution ticket",
			"capsule",
			"CORE-409",
		} {
			if !strings.Contains(string(frag), tok) {
				t.Errorf("polyforge#246: %s must carry the routing discipline's marker %q — the fragment exists but does not teach the discipline", w246FragRel, tok)
			}
		}
	}

	// 4. the SKILL.md body carries session rule 7 — the standing rule
	// that binds every loaded session to route execution work into the
	// lifecycle.
	if !strings.Contains(skill, "Execution work routes into the lifecycle") {
		t.Errorf("polyforge#246: the SKILL.md body must carry session rule 7's title (\"Execution work routes into the lifecycle\") — the standing rules are the discipline every loaded session carries; six rules leave execution work unruled")
	}
}

// w246FrontmatterDescription answers the SKILL.md frontmatter's parsed
// description value — the block between the opening and closing `---`,
// unmarshalled as strict YAML the way pi's loader reads it — or the
// parse error.
func w246FrontmatterDescription(body string) (string, error) {
	lines := strings.Split(body, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "---" {
		return "", errW246NoFrontmatter
	}
	end := -1
	for i, ln := range lines[1:] {
		if strings.TrimSpace(ln) == "---" {
			end = i + 1
			break
		}
	}
	if end < 0 {
		return "", errW246NoFrontmatter
	}
	var fm struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return "", err
	}
	return fm.Description, nil
}

var errW246NoFrontmatter = errors.New("no `---` delimited frontmatter block")
