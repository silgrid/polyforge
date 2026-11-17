package test

// carries: skills/polyforge/SKILL.md, skills/polyforge/verbs/doctor.md, skills/polyforge/verbs/init.md, skills/polyforge/verbs/task.md, skills/polyforge/verbs/file.md, skills/polyforge/verbs/remember.md, skills/polyforge/verbs/followup.md, docs/concepts.md, docs/reference/commands.md, docs/upgrade.md, internal/polyforge/workspace/layout.go, internal/polyforge/workspace/scratch_task.go

// w121_scratch_layout_texts_contract_test.go (polyforge#121, AC3/AC4):
// the shipped texts teach the scratch layout — the fixed categories
// (raw/ 3 days, tasks/ and sessions/ 14 days), one directory per run,
// no loose file at the top level, the prune rule (only stale batches
// inside the categories), the task delivery (PF_SCRATCH_DIR /
// PF_SCRATCH_RAW_DIR and the same paths in the prompt) and the
// session/sub-agent writing rule — in project-neutral words, and the
// workspace package's LayoutBlock and constants are the same source the
// texts spell (no drift: a category or a retention changed in code
// flips this test until every text follows). Every absence check is
// paired with a positive marker in the same file, so none passes
// vacuously. Red on the base tree: none of the new texts exist there,
// and every "gone" phrase does.

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w121Texts is one file's contract: the phrases it must carry and the
// phrases it must no longer carry (whitespace collapsed in both).
type w121Texts struct {
	rel  string
	want []string
	gone []string
}

func w121Read(t *testing.T, rel string) string {
	t.Helper()
	return w68Flat(t, rel)
}

// TestW121ScratchLayoutTextsContract (AC3/AC4): the routing skill, the
// verb skills, the docs and the upgrade note teach the scratch layout.
func TestW121ScratchLayoutTextsContract(t *testing.T) {
	for _, c := range []w121Texts{
		{
			// the routing skill: the categories, the task delivery and
			// the session/sub-agent writing rule
			"skills/polyforge/SKILL.md",
			[]string{
				"scratch/raw/",
				"scratch/tasks/",
				"scratch/sessions/",
				"kept 3 days",
				"kept 14 days",
				"no loose file at its top level",
				"raw data (batch JSON, logs, downloaded archives, history exports), whoever writes it",
				"agents/<id>/",
				"PF_SCRATCH_DIR",
				"PF_SCRATCH_RAW_DIR",
				"sub-agent brief",
				"`--prune-scratch` removes only stale batches inside the categories",
			},
			[]string{
				// the retired flat 14-day wording
				"reports entries older than 14 days, `--prune-scratch` removes them",
			},
		},
		{
			// the doctor skill: the new scratch row and the intro flag
			"skills/polyforge/verbs/doctor.md",
			[]string{
				"removes the stale scratch batches inside the categories",
				"raw/ 3 days, tasks/ 14 days, sessions/ 14 days",
				"every entry outside the categories",
				"`--prune-scratch` never removes them",
				"outdated layout section",
				"one directory per run",
			},
			[]string{
				// the retired scratch row's pass cell and age rule
				"no `scratch/` entry older than 14 days",
				"is older than 14 days, listed; run `pf doctor --prune-scratch` to remove them",
			},
		},
		{
			// the init skill: the categories come with scratch/
			"skills/polyforge/verbs/init.md",
			[]string{
				"creates `scratch/` with its fixed categories",
				"`raw/` (raw data, kept 3 days)",
				"`tasks/` (background tasks, kept 14 days)",
				"`sessions/` (sessions, kept 14 days)",
			},
			[]string{
				// the retired flat retention wording
				"reported by `pf doctor` after 14 days",
			},
		},
		{
			// the task skill: the delivered directories and variables
			"skills/polyforge/verbs/task.md",
			[]string{
				"PF_SCRATCH_DIR",
				"PF_SCRATCH_RAW_DIR",
				"scratch/tasks/<task id>/",
				"raw batch",
				"# Scratch",
			},
			nil,
		},
		{
			"skills/polyforge/verbs/file.md",
			[]string{"Scratch output goes to the workspace's `scratch/` categories"},
			nil,
		},
		{
			"skills/polyforge/verbs/remember.md",
			[]string{"belongs in the workspace's `scratch/` categories"},
			[]string{"(`pf doctor` reports entries older than 14 days)"},
		},
		{
			"skills/polyforge/verbs/followup.md",
			[]string{"pruned by each category's retention"},
			[]string{"(disposable, pruned after 14 days)"},
		},
		{
			// the concepts page: the workspace layout
			"docs/concepts.md",
			[]string{
				"fixed categories",
				"`raw/` kept 3 days",
				"`tasks/` and `sessions/` kept 14 days",
				"no loose file at its top level",
			},
			[]string{"`scratch/` (disposable output)"},
		},
		{
			// the reference's doctor row and task section: the prune
			// semantics, and the task prompt's closing # Scratch section
			// with the delivered directories and variables (review r1 #3)
			"docs/reference/commands.md",
			[]string{
				"removes the stale scratch batches inside the fixed categories",
				"past 3 days",
				"only ever reported",
				"a closing `# Scratch` section naming the task's own scratch directories",
				"`PF_SCRATCH_DIR` and `PF_SCRATCH_RAW_DIR`",
				"creates `scratch/tasks/<task id>/` and the task's own raw batch `scratch/raw/<UTC time>-<task id>-<unique suffix>/` before the dispatch",
				"a read-only errand runs without them and creates nothing",
			},
			[]string{"removes stale `scratch/` entries"},
		},
		{
			// the upgrade note: re-run init after upgrading
			"docs/upgrade.md",
			[]string{
				"the scratch layout categories",
				"run `pf init` again",
			},
			nil,
		},
	} {
		text := w121Read(t, c.rel)
		for _, w := range c.want {
			if !strings.Contains(text, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s must carry %q", c.rel, w)
			}
		}
		for _, g := range c.gone {
			if strings.Contains(text, strings.Join(strings.Fields(g), " ")) {
				t.Errorf("%s must no longer carry %q", c.rel, g)
			}
		}
	}
}

// TestW121ScratchLayoutSourceDrift (AC4): the workspace package is the
// one source — its LayoutBlock, category retentions and env names are
// what every text above spells; a change there flips this test until
// the texts follow, and the pinned words here cannot drift from the
// code that enforces them.
func TestW121ScratchLayoutSourceDrift(t *testing.T) {
	block := strings.Join(strings.Fields(workspace.LayoutBlock()), " ")
	for _, w := range []string{
		"`scratch/raw/` — raw data",
		"scratch/tasks/",
		"scratch/sessions/",
		"kept 3 days",
		"kept 14 days",
		"no loose file at its top level",
		"agents/<id>/",
		"removes only stale batches inside the categories",
		"<UTC time>-<source id>-<unique suffix>",
	} {
		if !strings.Contains(block, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("workspace.LayoutBlock() must carry %q (the texts spell its words):\n%s", w, block)
		}
	}
	if workspace.RawMaxAge != 3*24*time.Hour {
		t.Errorf("RawMaxAge = %v, want 3 days", workspace.RawMaxAge)
	}
	if workspace.TasksMaxAge != 14*24*time.Hour || workspace.SessionsMaxAge != 14*24*time.Hour {
		t.Errorf("tasks/sessions retention = %v / %v, want 14 days each", workspace.TasksMaxAge, workspace.SessionsMaxAge)
	}
	if workspace.EnvScratchDir != "PF_SCRATCH_DIR" || workspace.EnvScratchRawDir != "PF_SCRATCH_RAW_DIR" {
		t.Errorf("the scratch env names moved: %q / %q", workspace.EnvScratchDir, workspace.EnvScratchRawDir)
	}
}

// TestW121ScratchLayoutProjectNeutral (AC3): the scratch-layout texts
// name no concrete workspace, project or machine — the rule is spelled
// for any workspace (review r1 #9). The blocklist is the negative
// counterpart of the generic phrasing the contract above pins
// ("whoever writes it", "<task id>", "<UTC time>-<source id>").
func TestW121ScratchLayoutProjectNeutral(t *testing.T) {
	for _, rel := range []string{
		"skills/polyforge/SKILL.md",
		"docs/reference/commands.md",
	} {
		text := w121Read(t, rel)
		for _, concrete := range []string{"osp-ws", "/root/", "task-105", "wi87-"} {
			if strings.Contains(text, concrete) {
				t.Errorf("%s names the concrete %q — the scratch layout is project-neutral", rel, concrete)
			}
		}
	}
	if block := workspace.LayoutBlock(); strings.Contains(block, "osp-ws") || strings.Contains(block, "/root/") {
		t.Errorf("workspace.LayoutBlock() names a concrete workspace:\n%s", block)
	}
}
