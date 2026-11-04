package cli

// verify_scope_smoke_test.go: the engine's full-tier e2e selection for a
// repository that opts in with smoke_suite= (tools/verify-scope). Every case
// runs the in-tree engine through its documented surface (`--base <base>
// [--full] --json` in a git fixture), never the release-installed copy. The
// JSON output needs python3, a documented prerequisite — without it the test
// skips and says so.

import (
	"encoding/json"

	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type smokeSelection struct {
	Mode           string   `json:"mode"`
	Suites         []string `json:"suites"`
	UncoveredPaths []string `json:"uncovered_paths"`
	FullCommands   []string `json:"full_commands"`
}

type smokeReport struct {
	Mode     string `json:"mode"`
	Commands []struct {
		Phase string `json:"phase"`
		Cmd   string `json:"cmd"`
	} `json:"commands"`
	SmokeSuite   []string        `json:"smoke_suite"`
	E2ESelection *smokeSelection `json:"e2e_selection"`
	Diagnostics  []string        `json:"diagnostics"`
}

func (r smokeReport) cmds() []string {
	var out []string
	for _, c := range r.Commands {
		out = append(out, c.Cmd)
	}
	return out
}

const smokeConf = "fast_gate=echo FAST\n" +
	"e2e_runner=npm run test:e2e:all --\n" +
	"full_command=npm run test:e2e:all\n" +
	"smoke_suite=e2e/smoke.mjs\n"

// smokeFiles is an npm tree: two declared suites, one imported helper, one
// smoke suite and the runner target.
func smokeFiles(conf string) map[string]string {
	files := map[string]string{
		"package.json":    `{"name":"smoke","scripts":{"test:e2e:all":"node e2e/all.mjs"}}` + "\n",
		"src/panel.ts":    "1\n",
		"src/other.ts":    "1\n",
		"scripts/ops.sh":  "echo\n",
		"docs/guide.md":   "# g\n",
		"e2e/helper.mjs":  "export const h = 1;\n",
		"e2e/panel.mjs":   "// carries: src/panel*\nconsole.log(1)\n",
		"e2e/other.mjs":   "// carries: src/other*\nimport { h } from './helper.mjs';\nconsole.log(h)\n",
		"e2e/smoke.mjs":   "// carries: never/*\nconsole.log(3)\n",
		"e2e/all.mjs":     "console.log('runner')\n",
		"lib/unrelated.c": "int x;\n",
	}
	if conf != "" {
		files[".polyforge/verify-scope.conf"] = conf
	}
	return files
}

// smokeRun builds the fixture, commits the changes on top of the base and
// runs the in-tree engine with the given flags.
func smokeRun(t *testing.T, files map[string]string, changes map[string]string, flags ...string) smokeReport {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed: the engine fixtures are git repositories")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed: tools/verify-scope --json needs it (a documented prerequisite)")
	}
	engine, err := filepath.Abs(filepath.Join("..", "..", "..", "tools", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	w248Git(t, dir, "init", "-q", "-b", "main")
	for rel, body := range files {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "-m", "base")
	base := w248Git(t, dir, "rev-parse", "HEAD")
	for rel, body := range changes {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "--allow-empty", "-m", "change")
	cmd := exec.Command(engine, append([]string{"--base", base, "--json"}, flags...)...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("tools/verify-scope: %v\nstderr: %s", err, stderr.String())
	}
	var r smokeReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("--json is not one object: %v\n%s", err, out)
	}
	return r
}

func TestVerifyScopeSmokeSelection(t *testing.T) {
	full := []string{"npm run test:e2e:all"}
	cases := []struct {
		name      string
		conf      string
		files     map[string]string
		changes   map[string]string
		mode      string
		suites    []string
		uncovered []string
		cmds      []string
		diag      string
	}{
		{name: "pinned file selects its suite plus smoke",
			conf: smokeConf, changes: map[string]string{"src/panel.ts": "2\n"}, mode: "carries",
			suites: []string{"e2e/panel.mjs", "e2e/smoke.mjs"},
			cmds:   []string{"echo FAST", "npm run test:e2e:all -- e2e/panel.mjs e2e/smoke.mjs"}},
		{name: "helper selects its importer plus smoke",
			conf: smokeConf, changes: map[string]string{"e2e/helper.mjs": "export const h = 2;\n"}, mode: "carries",
			suites: []string{"e2e/other.mjs", "e2e/smoke.mjs"},
			cmds:   []string{"echo FAST", "npm run test:e2e:all -- e2e/other.mjs e2e/smoke.mjs"}},
		{name: "docs-only diff runs smoke only",
			conf: smokeConf, changes: map[string]string{"docs/guide.md": "# h\n", "README.md": "r\n"}, mode: "smoke",
			suites: []string{"e2e/smoke.mjs"},
			cmds:   []string{"echo FAST", "npm run test:e2e:all -- e2e/smoke.mjs"}},
		{name: "uncovered path is listed, not forced full",
			conf: smokeConf, changes: map[string]string{"src/panel.ts": "2\n", "scripts/ops.sh": "echo 2\n"}, mode: "carries",
			suites: []string{"e2e/panel.mjs", "e2e/smoke.mjs"}, uncovered: []string{"scripts/ops.sh"},
			cmds: []string{"echo FAST", "npm run test:e2e:all -- e2e/panel.mjs e2e/smoke.mjs"}},
		{name: "default suite commands without e2e_runner",
			conf:    "fast_gate=echo FAST\nfull_command=npm run test:e2e:all\nsmoke_suite=e2e/smoke.mjs\n",
			changes: map[string]string{"src/panel.ts": "2\n"}, mode: "carries",
			suites: []string{"e2e/all.mjs", "e2e/panel.mjs", "e2e/smoke.mjs"},
			cmds:   []string{"echo FAST", "node e2e/all.mjs", "node e2e/panel.mjs", "node e2e/smoke.mjs"},
			files: func() map[string]string {
				f := smokeFiles("")
				f["e2e/all.mjs"] = "// carries: src/*\nconsole.log('all')\n"
				return f
			}()},
		{name: "forced_full match runs the full tail",
			conf: smokeConf + "forced_full=lib/*\n", changes: map[string]string{"lib/unrelated.c": "int y;\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "changed path lib/unrelated.c is forced full"},
		{name: "built-in config rule runs the full tail",
			conf: smokeConf, changes: map[string]string{"tsconfig.json": "{}\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "changed path tsconfig.json is forced full"},
		{name: "conf change runs the full tail",
			conf: smokeConf, changes: map[string]string{".polyforge/verify-scope.conf": smokeConf + "# changed\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "the diff changes .polyforge/verify-scope.conf"},
		{name: "undeclared suite runs the full tail",
			conf: smokeConf, changes: map[string]string{"src/panel.ts": "2\n", "e2e/bare.mjs": "console.log(9)\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "declares no carries: header"},
		{name: "malformed header runs the full tail",
			conf: smokeConf, changes: map[string]string{"src/panel.ts": "2\n", "e2e/bad.mjs": "// carries:\nconsole.log(9)\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "malformed carries: declaration"},
		{name: "unresolvable import runs the full tail",
			conf: smokeConf, changes: map[string]string{"e2e/panel.mjs": "// carries: src/panel*\nimport x from './gone.mjs';\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "does not resolve"},
		{name: "invalid e2e_map runs the full tail",
			conf: smokeConf + "e2e_map=src/*=e2e/gone.mjs\n", changes: map[string]string{"src/panel.ts": "2\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "e2e_map names e2e/gone.mjs"},
		{name: "smoke suite that is not a suite runs the full tail",
			conf: smokeConf + "smoke_suite=e2e/missing.mjs\n", changes: map[string]string{"src/panel.ts": "2\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "smoke_suite e2e/missing.mjs is not a discovered suite"},
		{name: "empty smoke line runs the full tail",
			conf: smokeConf + "smoke_suite=\n", changes: map[string]string{"src/panel.ts": "2\n"}, mode: "full",
			cmds: []string{"echo FAST", "npm run test:e2e:all"}, diag: "conf rejects smoke_suite value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := c.files
			if files == nil {
				files = smokeFiles("")
			}
			files[".polyforge/verify-scope.conf"] = c.conf
			r := smokeRun(t, files, c.changes, "--full")
			if r.Mode != "full" {
				t.Fatalf("the tier fact must stay mode full, got %q", r.Mode)
			}
			if r.E2ESelection == nil {
				t.Fatalf("no e2e_selection: %v %v", r.cmds(), r.Diagnostics)
			}
			s := *r.E2ESelection
			if s.Mode != c.mode {
				t.Fatalf("selection mode %q, want %q (%+v, diags %v)", s.Mode, c.mode, s, r.Diagnostics)
			}
			if c.mode != "full" && !reflect.DeepEqual(s.Suites, c.suites) {
				t.Fatalf("suites %v, want %v", s.Suites, c.suites)
			}
			if !reflect.DeepEqual(s.UncoveredPaths, append([]string{}, c.uncovered...)) && !(len(s.UncoveredPaths) == 0 && len(c.uncovered) == 0) {
				t.Fatalf("uncovered %v, want %v", s.UncoveredPaths, c.uncovered)
			}
			if !reflect.DeepEqual(s.FullCommands, full) {
				t.Fatalf("full_commands %v, want the carried full tail %v", s.FullCommands, full)
			}
			if !reflect.DeepEqual(r.cmds(), c.cmds) {
				t.Fatalf("commands %v, want %v", r.cmds(), c.cmds)
			}
			if c.diag != "" && !strings.Contains(strings.Join(r.Diagnostics, "\n"), c.diag) {
				t.Fatalf("diagnostics %v miss %q", r.Diagnostics, c.diag)
			}
		})
	}
}

// TestVerifyScopeSmokeNotOptedIn: without a smoke_suite= line the full tier
// reports today's full commands and no selection.
func TestVerifyScopeSmokeNotOptedIn(t *testing.T) {
	conf := "fast_gate=echo FAST\ne2e_runner=npm run test:e2e:all --\nfull_command=npm run test:e2e:all\n"
	r := smokeRun(t, smokeFiles(conf), map[string]string{"src/panel.ts": "2\n"}, "--full")
	if r.E2ESelection != nil || len(r.SmokeSuite) != 0 {
		t.Fatalf("a repository without smoke_suite= must report no selection: %+v %v", r.E2ESelection, r.SmokeSuite)
	}
	if want := []string{"echo FAST", "npm run test:e2e:all"}; !reflect.DeepEqual(r.cmds(), want) {
		t.Fatalf("commands %v, want %v", r.cmds(), want)
	}
}

// TestVerifyScopeSmokeScopedUnchanged: the scoped tier of an opted-in
// repository adds no smoke suite and still sends an uncovered path to full.
func TestVerifyScopeSmokeScopedUnchanged(t *testing.T) {
	r := smokeRun(t, smokeFiles(smokeConf), map[string]string{"src/panel.ts": "2\n"})
	if r.Mode != "scoped" || r.E2ESelection != nil {
		t.Fatalf("scoped tier: mode %q selection %+v", r.Mode, r.E2ESelection)
	}
	if want := []string{"echo FAST", "npm run test:e2e:all -- e2e/panel.mjs"}; !reflect.DeepEqual(r.cmds(), want) {
		t.Fatalf("the scoped tier must add no smoke suite: commands %v, want %v", r.cmds(), want)
	}
	if !reflect.DeepEqual(r.SmokeSuite, []string{"e2e/smoke.mjs"}) {
		t.Fatalf("smoke_suite %v", r.SmokeSuite)
	}
	u := smokeRun(t, smokeFiles(smokeConf), map[string]string{"src/panel.ts": "2\n", "scripts/ops.sh": "echo 2\n"})
	if u.Mode != "full" || u.E2ESelection != nil {
		t.Fatalf("an uncovered path in the scoped tier must still fall back to full with no selection: mode %q %+v", u.Mode, u.E2ESelection)
	}
	if want := []string{"echo FAST", "npm run test:e2e:all"}; !reflect.DeepEqual(u.cmds(), want) {
		t.Fatalf("commands %v, want %v", u.cmds(), want)
	}
}
