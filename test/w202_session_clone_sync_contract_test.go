package test

// w202_session_clone_sync_contract_test.go: text contract for the routing
// skill's session-layer clone sync discipline (SKILL.md session rule 8).
//
// Falsifiable property: a polyforge session syncs its reading copy of a repo
// (<workspace>/.repo/<repo>) with a fetch plus a fast-forward-only pull before
// it relies on that copy, at exactly three triggers (analysis, wrap, filing or
// submitting), once per repo per burst of work, and stops and reports on
// divergence instead of merging, rebasing or stashing. The rule lives once in
// skills/polyforge/SKILL.md; the four fragments that run at those triggers
// carry only a pointer to it.
//
// The assertions are on pinned tokens, never on prose: a rewording that keeps
// the tokens stays green, while dropping the rule, a trigger, the ff-only
// command, the divergence stop or a pointer turns the test red. The test is
// red on a tree without the rule.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW202SessionCloneSyncContract(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	skill := read("skills/polyforge/SKILL.md")
	for _, tok := range []string{
		"Eight standing rules",
		"8. **Sync the session clone before reading it as fact",
		"`<workspace>/.repo/<repo>`",
		"`git fetch origin && git pull --ff-only`",
		"once per repo per burst of work, never before every command",
		"- **analysis** —",
		"- **wrap** —",
		"- **filing or submitting** —",
		"a diverged branch mean STOP and report what diverged",
		"never `merge`, `rebase`,\n   `stash` or `reset` on your own",
		"(rule 6 still governs writes under `.repo/`",
		"never substitutes for a memory read (rule 2)",
		`"workspace repo clones"`,
		"restate the conclusion against the synced head",
	} {
		if !strings.Contains(skill, tok) {
			t.Errorf("skills/polyforge/SKILL.md: session rule 8 lacks pinned token %q", tok)
		}
	}
	if strings.Contains(skill, "Seven standing rules") {
		t.Errorf("skills/polyforge/SKILL.md: the Session rules lead-in still counts seven rules")
	}

	for _, frag := range []string{
		"skills/polyforge/verbs/file.md",
		"skills/polyforge/verbs/submit.md",
		"skills/polyforge/verbs/wrap.md",
		"skills/polyforge/verbs/execute-intent.md",
	} {
		if !strings.Contains(read(frag), "SKILL.md session rule 8") {
			t.Errorf("%s: no pointer to SKILL.md session rule 8 at its clone-read trigger", frag)
		}
	}
}
