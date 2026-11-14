// carries: skills/polyforge/verbs/submit.md, skills/polyforge/verbs/file.md, skills/polyforge/verbs/execute-intent.md, skills/polyforge/verbs/task.md, skills/polyforge/verbs/queue.md, skills/polyforge/verbs/ops.md, skills/polyforge/SKILL.md, docs/contributing.md, docs/reference/commands.md, docs/user-guide.md, docs/bundles.md

package test

// pf27_pre_submit_analysis_skill_contract_test.go (polyforge#27,
// AC4–AC8): a text contract. The routing skill owns the pre-submit
// analysis: after an execution ticket is filed, or when `pf submit`'s
// check refuses it, the session proposes a read-only `pf task spawn`
// errand and writes the report into the ticket's `## 分析` section; the
// 7-item report format is defined once (submit.md) and referenced by the
// filing, execute-intent and task fragments; the boundary against
// grilling (facts and recommendations, never decisions) and the
// pre-submit confirmation rule (a small ticket submitted directly, every
// other ticket explained in plain words) live there too; the acceptance
// item's writing names its proof command and run directory; the
// Down-lane check names no specific project's components; commands.md
// documents --allow-no-analysis and the queue's analysis mark.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pf27Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

// pf27Section answers the text of doc between heading and the next
// heading of level two, whitespace-flattened (the fragments are
// hard-wrapped; the pins match the flattened text, the w403 convention).
func pf27Section(doc, heading string) string {
	i := strings.Index(doc, heading)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return strings.Join(strings.Fields(rest), " ")
}

// pf27Flat flattens a whole document for the same pins.
func pf27Flat(doc string) string {
	return strings.Join(strings.Fields(doc), " ")
}

func TestPF27PreSubmitAnalysisSkillContract(t *testing.T) {
	// verbs/submit.md owns the analysis check, once.
	submitRel := filepath.Join("skills", "polyforge", "verbs", "submit.md")
	submit := pf27Read(t, submitRel)
	const heading = "## Before submitting: the analysis check"
	si := strings.Index(submit, heading)
	if si < 0 {
		t.Fatalf("%s must carry the section %q — the analysis check runs beside the acceptance check, before `pf submit`", submitRel, heading)
	}
	if gi := strings.Index(submit, "\n## Success"); gi < 0 || si > gi {
		t.Errorf("%s's %q must come before `## Success` (the check runs before `pf submit`)", submitRel, heading)
	}
	section := pf27Section(submit, heading)
	for _, want := range []string{
		"`## 分析` (or `## Analysis`)",
		"免分析",
		"--allow-no-analysis",
		"`pf task spawn`",
		// the 7-item report format, defined here and here only
		"7 items, defined here and here only",
		"改动范围",
		"证明命令和运行目录",
		"断言旧行为的已有测试与文档",
		"依赖或冲突",
		"`pf recall`",
		"风险",
		"待决问题",
		"不拍板",
		// the boundary against grilling
		"only establishes facts",
		"belongs to the grill",
		// the pre-submit confirmation rule and the small-ticket definition
		"small ticket is submitted directly",
		"never by pasting",
		"very clear",
		"`expected diff size`",
		"`lane risk surface`",
		"never unattended",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("%s's analysis-check section must carry %q — the flow, the 7-item report format, the grill boundary and the confirmation rule are defined once here", submitRel, want)
		}
	}

	// review r1#2: the grill's writing path carries the same shape, by
	// reference to the carrier slot.
	accSection := pf27Section(submit, "## Before submitting: the acceptance check")
	for _, want := range []string{"the `Acceptance carrier` slot's shape", "the command that shows it holds", "before the carrier, which stays last"} {
		if !strings.Contains(accSection, want) {
			t.Errorf("%s's acceptance-check section must carry %q — the criteria written after the grill follow the Acceptance carrier slot's shape, proof command and run directory (AC6, review r1#2)", submitRel, want)
		}
	}

	// verbs/file.md: the acceptance item names its proof; the Down-lane
	// check names no specific project's components; the filing side
	// references the analysis without restating it.
	fileRel := filepath.Join("skills", "polyforge", "verbs", "file.md")
	file := pf27Read(t, fileRel)
	fileFlat := pf27Flat(file)
	ac := pf27Section(file, "### Acceptance at creation")
	for _, want := range []string{
		"the command that shows the criterion holds and the directory that command runs in",
		"— carried by build",
	} {
		if !strings.Contains(ac, want) {
			t.Errorf("%s's \"Acceptance at creation\" must carry %q — every acceptance item names its proof command and run directory (AC6)", fileRel, want)
		}
	}
	// review r1#2: the proof rides in the Acceptance carrier slot — the one
	// shape every acceptance criterion is written in — carrier still last.
	for _, want := range []string{
		"`<criterion> (proof: <command>, run in <dir>) — carried by <step id>`",
		"the carrier, which stays last",
	} {
		if !strings.Contains(fileFlat, want) {
			t.Errorf("%s's Acceptance carrier slot must carry %q — the proof command and run directory ride in the slot, before the carrier (AC6, review r1#2)", fileRel, want)
		}
	}
	if strings.Contains(fileFlat, "driver/daemon core") {
		t.Errorf("%s still names a specific project's components in the Down-lane check's risk surface (AC8)", fileRel)
	}
	if !strings.Contains(fileFlat, "core runtime parts") {
		t.Errorf("%s's Down-lane check must describe the risk surface project-independently as the project's core runtime parts (AC8)", fileRel)
	}
	pre := pf27Section(file, "### Pre-submit analysis")
	for _, want := range []string{"`pf task spawn`", "`## 分析`", "免分析", "defined once in `verbs/submit.md`"} {
		if !strings.Contains(pre, want) {
			t.Errorf("%s's \"Pre-submit analysis\" must carry %q — the filing side proposes the errand and references the one definition (AC4)", fileRel, want)
		}
	}

	// The other fragments reference, never restate.
	intent := pf27Flat(pf27Read(t, filepath.Join("skills", "polyforge", "verbs", "execute-intent.md")))
	for _, want := range []string{"pre-submit analysis", "`pf task spawn`", "`## 分析`", "`verbs/submit.md`", "riding before the carrier"} {
		if !strings.Contains(intent, want) {
			t.Errorf("skills/polyforge/verbs/execute-intent.md must carry %q — the routing action references the analysis check (AC4) and the slot's proof shape (AC6)", want)
		}
	}
	task := pf27Flat(pf27Read(t, filepath.Join("skills", "polyforge", "verbs", "task.md")))
	for _, want := range []string{"pre-submit analysis", "7-item report", "`verbs/submit.md`", "`## 分析`"} {
		if !strings.Contains(task, want) {
			t.Errorf("skills/polyforge/verbs/task.md must carry %q — the errand's shape references the one report format (AC4)", want)
		}
	}
	skill := pf27Flat(pf27Read(t, filepath.Join("skills", "polyforge", "SKILL.md")))
	r7 := skill
	if i := strings.Index(skill, "7. **Execution work routes into the lifecycle"); i >= 0 {
		r7 = skill[i:]
	}
	for _, want := range []string{"analysis check", "`## 分析`"} {
		if !strings.Contains(r7, want) {
			t.Errorf("skills/polyforge/SKILL.md session rule 7 must carry %q — submit runs the analysis check beside the acceptance check (AC4)", want)
		}
	}

	// The queue and ops surfaces name the new mark; commands.md documents
	// the flag and the marks.
	queue := pf27Flat(pf27Read(t, filepath.Join("skills", "polyforge", "verbs", "queue.md")))
	for _, want := range []string{"需要交互：未分析", "`\"analysis_missing\": true`", "「免分析」 with its date counts as content"} {
		if !strings.Contains(queue, want) {
			t.Errorf("skills/polyforge/verbs/queue.md must carry %q — the missing-analysis mark, its json field and its way out (AC3)", want)
		}
	}
	ops := pf27Flat(pf27Read(t, filepath.Join("skills", "polyforge", "verbs", "ops.md")))
	for _, want := range []string{"需要交互：缺验收", "需要交互：未分析", "pre-submit analysis section"} {
		if !strings.Contains(ops, want) {
			t.Errorf("skills/polyforge/verbs/ops.md must carry %q — the auto-pull skips a ticket without analysis too", want)
		}
	}
	commands := pf27Flat(pf27Read(t, filepath.Join("docs", "reference", "commands.md")))
	for _, want := range []string{"--allow-no-acceptance", "--allow-no-analysis", "需要交互：未分析", "\"analysis_missing\": true", "both marks when both are missing"} {
		if !strings.Contains(commands, want) {
			t.Errorf("docs/reference/commands.md must carry %q — the reference documents the prechecks and the queue's interaction marks (AC7)", want)
		}
	}
	// review r1#3: the guide's develop example and the bundles reference
	// state the analysis check beside the acceptance check.
	guide := pf27Flat(pf27Read(t, filepath.Join("docs", "user-guide.md")))
	for _, want := range []string{"a `## 分析` section", "`pf task spawn`", "--allow-no-analysis", "免分析"} {
		if !strings.Contains(guide, want) {
			t.Errorf("docs/user-guide.md's develop example must carry %q — the worked example survives the analysis check (review r1#3)", want)
		}
	}
	bundles := pf27Flat(pf27Read(t, filepath.Join("docs", "bundles.md")))
	for _, want := range []string{"--allow-no-analysis", "需要交互：未分析", "neither check runs"} {
		if !strings.Contains(bundles, want) {
			t.Errorf("docs/bundles.md's `acceptance:` entry must carry %q — the lane key governs the analysis check too (review r1#3)", want)
		}
	}
	contrib := pf27Flat(pf27Read(t, filepath.Join("docs", "contributing.md")))
	for _, want := range []string{"--allow-no-analysis", "需要交互：未分析", "`pf task spawn`"} {
		if !strings.Contains(contrib, want) {
			t.Errorf("docs/contributing.md must carry %q — the develop-lane gates paragraph states the analysis check", want)
		}
	}
}
