// carries: skills/polyforge/verbs/task.md, skills/polyforge/SKILL.md

package test

// pf101_task_forensics_contract_test.go (polyforge#122, AC1): the `pf
// task` drafting list requires the session to write the task's output
// constraints into the prompt it drafts — a task agent sees nothing of
// the drafting session, so whatever the draft leaves out it cannot ask
// about. The constraints are SKILL.md session rule 9's six duties bound
// to the task's own directories (polyforge#121): raw material lands
// complete in the task's own raw batch (the PF_SCRATCH_RAW_DIR the
// prompt's closing # Scratch section names — the one #121 settled, the
// reason this ticket waits on it), and the report back to the session
// carries only a summary and an evidence index (paths, line ranges or
// JSON pointers, with hashes) the session can read on from — the
// ticket's AC1 wording verbatim: 原始材料落盘到 #121 定的子目录、只回
// 摘要和证据索引.
//
// The W59 contract (16 routing verbs, budgets, the read-only boundary,
// reports never backfilled by the task itself) pins the fragment's
// other halves; this test adds only the drafting item's tokens. Red on
// the base tree: the drafting list carries no 输出约束 item there.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPF101TaskDraftingForensicsContract(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, "skills", "polyforge", "verbs", "task.md"))
	if err != nil {
		t.Fatalf("skills/polyforge/verbs/task.md: %v", err)
	}
	frag := string(raw)

	// The Drafting section itself (pinned by shape, not prose).
	i := strings.Index(frag, "## Drafting")
	if i < 0 {
		t.Fatalf("skills/polyforge/verbs/task.md: no Drafting section")
	}
	sec := frag[i:]
	if j := strings.Index(sec[3:], "\n## "); j >= 0 {
		sec = sec[:j+3]
	}

	// The drafting item: the prompt states the output constraints —
	// raw material into the task's own raw batch (the #121 directory,
	// delivered as PF_SCRATCH_RAW_DIR and named by the closing #
	// Scratch section), the report back only a summary and an
	// evidence index a reader can follow, the tool-return bound.
	for _, tok := range []string{
		"**输出约束**",
		"session rule 9",
		"PF_SCRATCH_RAW_DIR",
		"# Scratch",
		"a summary and an evidence index",
		"line ranges or JSON pointers",
	} {
		if !strings.Contains(sec, tok) {
			t.Errorf("the Drafting section lacks the pinned token %q — the output-constraints drafting item is missing or reworded past recognition", tok)
		}
	}
	if !strings.Contains(sec, "hashes") {
		t.Errorf("the Drafting section's evidence index must carry hashes (the ticket's Q6 reference syntax)")
	}

	// The W59 boundary stays: the task still never backfills anything
	// itself (the drafting item teaches the prompt's shape, not a task
	// write).
	for _, w59 := range []string{
		"never a code change",
		"The task process carries `PF_TASK_ID`",
	} {
		if !strings.Contains(frag, w59) {
			t.Errorf("skills/polyforge/verbs/task.md: the W59-pinned boundary token %q is gone — this change adds a drafting item, it rewrites no existing half", w59)
		}
	}
}
