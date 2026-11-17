package test

// carries: workflows/_shared/step-header.md, workflows/_shared/skills/publish.md, workflows/_shared/skills/verify.md, workflows/_shared/skills/merge.md, workflows/_shared/skills/sync.md, workflows/develop/fragments/review-judge.md, workflows/develop/steps/build.md, workflows/micro/steps/review.md, docs/reference/workflow-contract.md, docs/bundles.md, docs/reference/commands.md

// pf88_base_branch_text_contract_test.go (polyforge#88, AC7): the base
// branch's one definition and the command literals that take it, pinned
// sentence by sentence — the step header's Files-table row (the one
// definition every step's prompt carries), the skills' rewritten literals
// (publish's rev-list and gh pr create --base and its Never list, the
// three merge-base diff literals, verify's --stat, merge's freshness
// check and its sync sentence, sync's base sentence) and the three
// documents' new sub-ticket sentences; the old <default> and origin/HEAD
// literals must be gone from the places that carried them. The pinned
// sentences name no lane and no project: AC7's own neutrality rule
// (review r1 #1/#2, N4).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pf88DocRead answers a repo-relative file's body (v1Root is the module
// root; this package runs two levels below it).
func pf88DocRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("polyforge#88: read %s: %v", rel, err)
	}
	return string(b)
}

// pf88Wants are the sentences each file must carry (flattened: a sentence
// the paragraph wraps across lines must still pin).
var pf88Wants = map[string][]string{
	"workflows/_shared/step-header.md": {
		"It is the **base branch**: the one branch every step's diff, merge and PR commands",
		"wherever a step's text or command names the default branch, a capsule carrying this file means this branch",
		"never a silent fall-back to the default branch",
	},
	"workflows/_shared/skills/publish.md": {
		"`<base>` is the step header's base branch — the",
		"`git rev-list --count origin/<base>..HEAD`",
		"`gh pr create --base <base> --head <branch>`",
		"per repo the base branch's commit merged in",
		"a push to the default branch (a fixed target branch alike) or without a refspec",
	},
	"workflows/develop/fragments/review-judge.md": {
		"`git diff $(git merge-base HEAD origin/<base>)..HEAD`",
	},
	"workflows/develop/steps/build.md": {
		"`git diff $(git merge-base HEAD origin/<base>)..HEAD`",
	},
	"workflows/micro/steps/review.md": {
		"`git diff $(git merge-base HEAD origin/<base>)..HEAD`",
	},
	"workflows/_shared/skills/verify.md": {
		"`git -C repos/<name> diff --stat origin/<base>...HEAD`",
	},
	"workflows/_shared/skills/merge.md": {
		"the current `origin/<base>` (the step header's base branch)",
		"which merges the base branch again",
	},
	"workflows/_shared/skills/sync.md": {
		"merges the step header's base branch (the capsule's fixed target branch, else origin's default branch) into each task repo in place",
	},
	"docs/reference/workflow-contract.md": {
		"The fixed branch is the sub-ticket's steps' base too",
		"the scope engine's default base — and the micro lane's governing conf, read at it — is that merge-base",
		"The landing the sub-ticket's merge registers is `state: landed_on_target` with `target` naming the branch",
	},
	"docs/bundles.md": {
		"The fixed branch is the sub-ticket's steps' base: their sync",
		"registers is `state: landed_on_target` (never in the integrate pool's `landed` records)",
	},
	"docs/reference/commands.md": {
		"only `origin/<target>` is fetched and merged",
		"`{state: landed_on_target, target, …}`",
		"is the default branch's stand-in in every one of these places",
		"`merge-base HEAD origin/<target>` then, else the default branch",
	},
}

// pf88Banned are the literals the rewritten places must no longer carry:
// the dangling <default> a publish agent could read as the default branch
// (review r1 #1) and the origin/HEAD diff literals the base branch
// replaced.
var pf88Banned = map[string][]string{
	"workflows/_shared/skills/publish.md": {
		"gh pr create --base <default>",
	},
	"workflows/develop/fragments/review-judge.md": {
		"merge-base HEAD origin/HEAD",
	},
	"workflows/develop/steps/build.md": {
		"merge-base HEAD origin/HEAD",
	},
	"workflows/micro/steps/review.md": {
		"merge-base HEAD origin/HEAD",
	},
	"workflows/_shared/skills/verify.md": {
		"origin/<default>",
	},
	"workflows/_shared/skills/merge.md": {
		"origin/<default>",
	},
}

// TestPF88BaseBranchTextContract pins the base branch's definition and
// every rewritten literal: each want must survive flattened (a wrapped
// sentence still pins), each banned literal must be gone. The pinned
// sentences name no lane and no project.
func TestPF88BaseBranchTextContract(t *testing.T) {
	for rel, wants := range pf88Wants {
		flat := strings.Join(strings.Fields(pf88DocRead(t, rel)), " ")
		for _, w := range wants {
			if !strings.Contains(flat, w) {
				t.Errorf("polyforge#88: %s must carry %q (flattened), it does not", rel, w)
			}
		}
	}
	for rel, banned := range pf88Banned {
		flat := strings.Join(strings.Fields(pf88DocRead(t, rel)), " ")
		for _, b := range banned {
			if strings.Contains(flat, b) {
				t.Errorf("polyforge#88: %s must no longer carry %q, it does", rel, b)
			}
		}
	}
}
