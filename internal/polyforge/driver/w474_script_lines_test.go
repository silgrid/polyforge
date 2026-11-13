package driver

// w474_script_lines_test.go: the steps whose pre-written
// command became a workflow script — develop's and micro's sync and merge,
// micro's evidence, the integration lane's revert — carry the script's
// command lines written out in full in their rendered prompts
// (`"$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py" …`), name the script in
// their skill's `commands:`, and the checks of the steps after them read
// the upstream result file's argv; no shipped lane names the Go commands
// that left pf.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW474ScriptStepsCarryTheirLines(t *testing.T) {
	line := func(script string) string { return `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/` + script + `"` }
	for _, c := range []struct {
		bundle, step, script string
		lines                []string
	}{
		{developBundle, "sync", "sync.py", []string{" --json --out artifacts/sync/result.json", " --continue --json --out artifacts/sync/result.json", " --abort --json --out artifacts/sync/result.json"}},
		{"micro", "sync", "sync.py", []string{" --json --out artifacts/sync/result.json", " --continue --json --out artifacts/sync/result.json", " --abort --json --out artifacts/sync/result.json"}},
		{developBundle, "merge", "merge.py", []string{" --publish artifacts/publish.json --verified artifacts/verify/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json"}},
		{"micro", "merge", "merge.py", []string{" --publish artifacts/publish.json --verified artifacts/run/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json"}},
		{"micro", "evidence", "run.py", []string{" --isolation host --plan-from artifacts/fix/evidence.json --json --out artifacts/run/result.json"}},
		{"integrate", "revert", "revert.py", []string{
			" --batch artifacts/integrate/batch.json --item <id> --item <id> … --ops-out artifacts/integrate/revert-ops.json --json --out artifacts/integrate/revert.json",
			" --batch artifacts/integrate/batch.json --ops-out artifacts/integrate/revert-ops.json --json --out artifacts/integrate/revert.json"}},
	} {
		p := spawnStepPrompt(t, c.bundle, c.step)
		for _, l := range c.lines {
			if want := "`" + line(c.script) + l + "`"; !strings.Contains(p, want) {
				t.Errorf("%s/%s: the prompt must carry the line %s", c.bundle, c.step, want)
			}
		}
		if strings.Contains(p, "pf-"+strings.TrimSuffix(c.script, ".py")+" ") {
			t.Errorf("%s/%s: the prompt still names pf-%s", c.bundle, c.step, strings.TrimSuffix(c.script, ".py"))
		}
	}
	// the downstream checks read the upstream result's argv
	for _, c := range []struct{ bundle, step, want string }{
		{developBundle, verificationStepID, "its `argv` is the arguments of the sync skill's start line or of its continue line"},
		{"micro", "evidence", "its `argv` is the arguments of the sync skill's start line or of its continue line"},
		{developBundle, "merge", "Its `argv` is exactly the arguments of the line the verifying gate's skill writes out"},
		{"micro", "merge", "Its `argv` is exactly the arguments of the line the verifying gate's skill writes out"},
		{"integrate", "full-gate", "(`command` is `revert.py`), its `argv` is the revert skill's line for the round's list"},
	} {
		flat := strings.Join(strings.Fields(spawnStepPrompt(t, c.bundle, c.step)), " ")
		if !strings.Contains(flat, c.want) {
			t.Errorf("%s/%s: the upstream check must read argv: %q", c.bundle, c.step, c.want)
		}
	}
}

// TestW474NoLaneNamesTheRetiredCommands: every skill and step definition
// of the shipped lanes is free of pf-sync, pf-merge, pf-revert and pf-run,
// and the four skills name their script in `commands:`.
func TestW474NoLaneNamesTheRetiredCommands(t *testing.T) {
	root := filepath.Join("..", "..", "..", "workflows")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "_archive" || d.Name() == "scripts") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, name := range []string{"pf-sync", "pf-merge", "pf-revert", "pf-run"} {
			if strings.Contains(string(raw), name) {
				t.Errorf("%s names %s, a workflow script since", p, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for skill, script := range map[string]string{"_shared/skills/sync.md": "sync.py", "_shared/skills/merge.md": "merge.py",
		"integrate/skills/revert.md": "revert.py", "micro/skills/evidence.md": "run.py"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(skill)))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "\ncommands: ["+script+"]\n") {
			t.Errorf("%s: commands: must be [%s]", skill, script)
		}
	}
}
