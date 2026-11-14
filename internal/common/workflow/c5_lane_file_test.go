package workflow_test

// c5_lane_file_test.go: workflow contract 1's lane file. A lane is its
// workflow.yaml (README.md is prose the loader never reads); a lane that
// is still a workflow.md is refused with its rewrite — WFC-101 when it is
// the only lane file, WFC-110 beside a workflow.yaml — and a step's
// vars_files reads a var from a file of the lane, exactly as if it were
// written inline in vars.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// contract4Lane writes a lane in contract 4's shape — a workflow.md whose
// frontmatter declares contract (none when contract is ""), and a body —
// and answers its directory.
func contract4Lane(t *testing.T, contract string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "old")
	front := "name: old\ndescription: a contract 4 lane\nsteps:\n  - id: work\n"
	if contract != "" {
		front = "contract: " + contract + "\n" + front
	}
	write(t, filepath.Join(dir, workflow.Contract4File), "---\n"+front+"---\n\n# old\n")
	write(t, filepath.Join(dir, "steps", "work.md"), workflowtest.Definition("work"))
	return dir
}

// TestC5Contract4LaneIsRefusedWithItsRewrite: a lane whose lane file is a
// workflow.md loads nothing but the contract it declares and is refused
// with exactly one WFC-101 naming both versions, the upgrade section and
// the rewrite (workflow.yaml, README.md); it is present in its lanes root,
// so a claim names the rewrite instead of a missing lane.
func TestC5Contract4LaneIsRefusedWithItsRewrite(t *testing.T) {
	for _, tc := range []struct {
		contract string
		want     int
	}{{"4", 4}, {"", 0}, {"five", 0}} {
		dir := contract4Lane(t, tc.contract)
		lane, errs := workflow.Load(dir)
		if len(errs) != 1 || errs[0].Code != workflow.CodeContractVersion || errs[0].File != filepath.Join(dir, workflow.Contract4File) || errs[0].Path != "contract" {
			t.Fatalf("contract %q: defects %v, want one %s at %s", tc.contract, errs, workflow.CodeContractVersion, workflow.Contract4File)
		}
		for _, want := range []string{fmt.Sprintf("declares contract %d)", tc.want), fmt.Sprintf("implements contract %d", workflow.ContractVersion),
			workflow.UpgradeSection, "workflow.yaml", "README.md", "`contract: 1`", "delete workflow.md"} {
			if !strings.Contains(errs[0].Msg, want) {
				t.Errorf("contract %q: the refusal %q does not name %q", tc.contract, errs[0].Msg, want)
			}
		}
		if lane == nil || lane.Contract != tc.want || len(lane.Steps) != 0 {
			t.Errorf("contract %q: lane %+v, want contract %d and no step read", tc.contract, lane, tc.want)
		}
		if !workflow.LanePresent(filepath.Dir(dir), "old") {
			t.Errorf("contract %q: a workflow.md lane must be present in its root (its claim names the rewrite)", tc.contract)
		}
	}
	if workflow.LanePresent(t.TempDir(), "old") {
		t.Fatal("an absent lane must not be present")
	}
}

// TestC5BothLaneFilesAreRefused: a workflow.md beside a workflow.yaml is
// WFC-110 at the workflow.md with the rewrite's deletion, and the
// workflow.yaml is judged as usual (its own defects reported too).
func TestC5BothLaneFilesAreRefused(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo", "steps:\n  - id: work\n")
	old := filepath.Join(dir, workflow.Contract4File)
	write(t, old, "---\ncontract: 4\nname: demo\n---\n")
	lane, errs := workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != workflow.CodeLaneFile || errs[0].File != old || !strings.Contains(errs[0].Msg, "delete workflow.md") {
		t.Fatalf("defects %v, want one %s at %s", errs, workflow.CodeLaneFile, old)
	}
	if lane == nil || lane.Contract != workflow.ContractVersion || lane.Step("work") == nil {
		t.Fatalf("the workflow.yaml must still be read: %+v", lane)
	}
	write(t, filepath.Join(dir, workflow.WorkflowFile), workflowtest.Header("demo")+"steps:\n  - { id: work, kind: x }\n")
	if _, errs := workflow.Load(dir); len(errs) != 2 || errs[0].Code != workflow.CodeLaneFile || errs[1].Code != workflow.CodeUnknownKey {
		t.Fatalf("defects %v, want the workflow.md's WFC-110 and the workflow.yaml's WFC-111", errs)
	}
}

// TestC5LaneFileShape: a workflow.yaml is one YAML mapping — empty, two
// documents (a frontmatter's closing fence left in) or a list are WFC-110;
// a leading comment (the editor's schema line) is no defect and the lines
// are the file's own.
func TestC5LaneFileShape(t *testing.T) {
	for name, body := range map[string]string{
		"empty":     "# nothing\n",
		"documents": workflowtest.Header("demo") + "steps:\n  - id: work\n---\n",
		"list":      "- contract: 1\n",
	} {
		dir := filepath.Join(t.TempDir(), "demo")
		write(t, filepath.Join(dir, workflow.WorkflowFile), body)
		if _, errs := workflow.Load(dir); len(errs) != 1 || errs[0].Code != workflow.CodeLaneFile {
			t.Errorf("%s: defects %v, want one %s", name, errs, workflow.CodeLaneFile)
		}
	}
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo", "steps:\n  - id: work\n")
	write(t, filepath.Join(dir, workflow.WorkflowFile), "# yaml-language-server: $schema=../_schema/"+workflow.SchemaFile(workflow.ContractVersion)+"\n"+workflowtest.Header("demo")+"steps:\n  - { id: work, kind: x }\n")
	if _, errs := workflow.Load(dir); len(errs) != 1 || errs[0].Code != workflow.CodeUnknownKey || errs[0].Line != 6 {
		t.Fatalf("defects %v, want one %s at line 6", errs, workflow.CodeUnknownKey)
	}
}

// varsLane writes the lane demo whose step echo's definition uses
// {{note}} and {{other}}, with the step entry entry, and answers its
// directory (unchecked).
func varsLane(t *testing.T, entry string, files map[string]string) string {
	t.Helper()
	dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo", "steps:\n  - id: echo\n"+entry)
	write(t, filepath.Join(dir, "steps", "echo.md"), "---\nname: echo\ndescription: d\n---\n\nRun echo.\n{{note}}\nthen {{other}}.\n")
	for rel, body := range files {
		write(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
	}
	return dir
}

// TestC5VarsFilesIsAnInlineVar: a var read from a file of the lane renders
// the definition exactly as the same text written inline (a block scalar),
// and the step's vars carry it.
func TestC5VarsFilesIsAnInlineVar(t *testing.T) {
	text := "Line one of a long var,\n  indented line two: with a colon {braces} and `code`.\n"
	inline := varsLane(t, "    vars:\n      other: x\n      note: |-\n        Line one of a long var,\n          indented line two: with a colon {braces} and `code`.\n", nil)
	fromFile := varsLane(t, "    vars: { other: x }\n    vars_files: { note: fragments/note.md }\n", map[string]string{"fragments/note.md": text})
	var rendered []string
	for _, dir := range []string{inline, fromFile} {
		lane, errs := workflow.Load(dir)
		if len(errs) > 0 {
			t.Fatal(workflow.AsLoadError(dir, errs))
		}
		if got := strings.TrimRight(lane.Step("echo").Vars["note"], "\n"); got != strings.TrimRight(text, "\n") {
			t.Fatalf("%s: var note = %q", dir, got)
		}
		d, err := workflow.ReadDefinitionIn(lane, "echo")
		if err != nil {
			t.Fatal(err)
		}
		rendered = append(rendered, d.Body)
	}
	if rendered[0] != rendered[1] || !strings.Contains(rendered[1], "indented line two") {
		t.Fatalf("inline and vars_files render differently:\n%s\n---\n%s", rendered[0], rendered[1])
	}
}

// TestC5VarsFilesRefusals: a var in both vars and vars_files, and a
// vars_files path that leaves the lane, is absolute, names no file, a
// directory or a symbolic link, is one WFC-112 at its key; a vars_files
// var no placeholder uses is WFC-195 at the workflow.yaml's vars_files key.
func TestC5VarsFilesRefusals(t *testing.T) {
	note := map[string]string{"fragments/note.md": "a note\n"}
	dir := varsLane(t, "    vars: { note: inline, other: x }\n    vars_files: { note: fragments/note.md }\n", note)
	_, errs := workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "steps[0](echo).vars_files.note" || errs[0].Line != 7 ||
		!strings.Contains(errs[0].Msg, "vars and in vars_files") {
		t.Fatalf("both: defects %v, want one %s at vars_files.note (line 7)", errs, workflow.CodeInvalidValue)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	write(t, outside, "outside\n")
	for _, rel := range []string{"../outside.md", "fragments/../../outside.md", outside, "fragments/missing.md", "fragments", "fragments/link.md", "linked/note.md", "", "."} {
		dir := varsLane(t, "    vars: { other: x }\n    vars_files: { note: \""+rel+"\" }\n", note)
		if err := os.Symlink(filepath.Join(dir, "fragments", "note.md"), filepath.Join(dir, "fragments", "link.md")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "fragments"), filepath.Join(dir, "linked")); err != nil {
			t.Fatal(err)
		}
		_, errs := workflow.Load(dir)
		var bad []workflow.ContractError
		for _, e := range errs {
			if e.Code != workflow.CodePlaceholder {
				bad = append(bad, e)
			}
		}
		if len(bad) != 1 || bad[0].Code != workflow.CodeInvalidValue || bad[0].Path != "steps[0](echo).vars_files.note" {
			t.Errorf("vars_files %q: defects %v, want one %s at vars_files.note", rel, errs, workflow.CodeInvalidValue)
		}
	}
	dir = varsLane(t, "    vars: { note: n, other: x }\n    vars_files: { unused: fragments/note.md }\n", note)
	_, errs = workflow.Load(dir)
	if len(errs) != 1 || errs[0].Code != workflow.CodePlaceholder || errs[0].File != filepath.Join(dir, workflow.WorkflowFile) ||
		errs[0].Path != "steps[0](echo).vars_files.unused" || errs[0].Line != 7 {
		t.Fatalf("unused: defects %v, want one %s at the workflow.yaml's vars_files key (line 7)", errs, workflow.CodePlaceholder)
	}
}

// TestC5ShippedLanesAreYAML: every shipped lane is a workflow.yaml with
// its README.md and no workflow.md; develop's long review vars are files
// of the lane, not inline.
func TestC5ShippedLanesAreYAML(t *testing.T) {
	dirs, err := workflow.LaneDirs(shippedLanes)
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no shipped lanes: %v", err)
	}
	for _, d := range dirs {
		for f, want := range map[string]bool{workflow.WorkflowFile: true, "README.md": true, workflow.Contract4File: false} {
			if _, err := os.Stat(filepath.Join(d, f)); (err == nil) != want {
				t.Errorf("%s: %s present = %t, want %t", d, f, err == nil, want)
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(shippedLanes, "develop", workflow.WorkflowFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "intro: fragments/review-intro.md") || !strings.Contains(string(raw), "judge: fragments/review-judge.md") || strings.Contains(string(raw), "|-") {
		t.Errorf("develop's review must read intro and judge from fragments/, with no inline block var left")
	}
}
