package cli

// w248_scope_gates_parity_test.go: `pf doctor`'s scoped-gates row reads the
// carries layer with its own Go reading of the engine's discovery, header
// parsing and import scanning (doctor_scope_gates.go), while the engine
// itself is tools/verify-scope. One fact in two places: this test feeds the
// same git fixture to both and requires them to agree, so drift reddens the
// bar (#217 N5 / #227). The engine runs through its
// documented surface only (`--base <base> --json` in the clone); its JSON
// output needs python3, a documented prerequisite — without it the test
// skips and says so, never passes silently.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// w248EngineSuiteDiag is the engine's per-suite carries diagnostic: a suite
// with no carries: header or with a malformed carries: declaration, and an
// e2e_map= line naming no discovered suite.
var w248EngineSuiteDiag = regexp.MustCompile(`^(?:verify-scope: )?carries: (?:suite (\S+) (declares no carries: header|has a malformed carries: declaration)|e2e_map names (\S+), which is not a discovered suite)`)

// w248Git runs git in dir with a fixed identity.
func w248Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=w248@example.invalid", "-c", "user.name=w248", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w248Write writes one fixture file.
func w248Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestW248ScopeGatesCarriesParity: for every carries-layer shape, the
// engine's mode and the suites it names undeclared or malformed equal the
// doctor row's verdict and its undeclared list.
func TestW248ScopeGatesCarriesParity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed: the parity fixtures are git repositories")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed: tools/verify-scope --json needs it (a documented prerequisite), so the engine half cannot run")
	}
	engine, err := filepath.Abs(filepath.Join("..", "..", "..", "tools", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(engine); err != nil {
		t.Fatalf("the engine is missing: %v", err)
	}
	const (
		npmDeclared = "// carries: src/*\nconsole.log(1)\n"
		pyDeclared  = "# carries: src/*\ndef test_a():\n    pass\n"
		rsDeclared  = "// carries: src/*\n#[test]\nfn a() {}\n"
		pkg         = `{"name":"w248","scripts":{"test:e2e:all":"node e2e/all.mjs"}}` + "\n"
		conf        = ".polyforge/verify-scope.conf"
	)
	cases := []struct {
		name, analyzer, change string
		files                  map[string]string
	}{
		{"npm declared", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", "e2e/a.mjs": npmDeclared}},
		{"npm undeclared", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", "e2e/a.mjs": npmDeclared, "e2e/b.mjs": "console.log(2)\n"}},
		{"npm malformed carries", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", "e2e/a.mjs": npmDeclared, "e2e/m.mjs": "// carries:\nconsole.log(3)\n"}},
		{"npm imported helper", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", "e2e/a.mjs": "// carries: src/*\nimport { h } from \"./helper.mjs\";\nh();\n",
			"e2e/helper.mjs": "export const h = () => 1;\n"}},
		{"suite_glob discovery", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "suite_glob=spec/*.spec.mjs\n",
			"spec/a.spec.mjs": npmDeclared, "spec/b.spec.mjs": "console.log(2)\n", "e2e/ignored.mjs": "console.log(9)\n"}},
		{"exclude", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "exclude=e2e/skip-*.mjs\n",
			"e2e/a.mjs": npmDeclared, "e2e/skip-me.mjs": "console.log(2)\n"}},
		{"e2e_runner target", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "e2e_runner=npm run test:e2e:all\n",
			"e2e/a.mjs": npmDeclared, "e2e/all.mjs": "console.log('runner')\n"}},
		{"e2e_map present suite", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "e2e_map=src/*=e2e/b.mjs\n",
			"e2e/a.mjs": npmDeclared, "e2e/b.mjs": "console.log(2)\n"}},
		{"e2e_map absent suite", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "e2e_map=src/*=e2e/gone.mjs\n",
			"e2e/a.mjs": npmDeclared}},
		{"python declared", "python", "e2e/test_a.py", map[string]string{
			"pyproject.toml": "[project]\nname = \"w248\"\n", "src/a.py": "A = 1\n", "e2e/test_a.py": pyDeclared}},
		{"python undeclared", "python", "e2e/test_a.py", map[string]string{
			"pyproject.toml": "[project]\nname = \"w248\"\n", "src/a.py": "A = 1\n", "e2e/test_a.py": pyDeclared,
			"tests/e2e/test_b.py": "def test_b():\n    pass\n"}},
		{"rust declared", "rust", "tests/a.rs", map[string]string{
			"Cargo.toml": "[package]\nname = \"w248\"\nversion = \"0.1.0\"\n", "src/lib.rs": "pub fn a() {}\n", "tests/a.rs": rsDeclared}},
		{"npm empty smoke line", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "smoke_suite=\n", "e2e/a.mjs": npmDeclared}},
		{"npm malformed smoke line", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "smoke_suite=e2e/a.mjs e2e/b.mjs\n", "e2e/a.mjs": npmDeclared}},
		{"npm valid smoke line", "npm", "src/a.js", map[string]string{
			"package.json": pkg, "src/a.js": "1\n", conf: "smoke_suite=e2e/a.mjs\n", "e2e/a.mjs": npmDeclared}},
		{"rust undeclared", "rust", "tests/a.rs", map[string]string{
			"Cargo.toml": "[package]\nname = \"w248\"\nversion = \"0.1.0\"\n", "src/lib.rs": "pub fn a() {}\n", "tests/a.rs": rsDeclared,
			"tests/b.rs": "#[test]\nfn b() {}\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			w248Git(t, dir, "init", "-q", "-b", "main")
			for rel, body := range c.files {
				w248Write(t, dir, rel, body)
			}
			w248Git(t, dir, "add", "-A")
			w248Git(t, dir, "commit", "-q", "-m", "base")
			base := w248Git(t, dir, "rev-parse", "HEAD")
			// the change touches a path a declaration covers (a source the
			// suites carry, or the declared suite itself), so the mode turns
			// on the carries layer alone
			w248Write(t, dir, c.change, c.files[c.change]+"\n")
			w248Git(t, dir, "commit", "-q", "-am", "change "+c.change)

			cmd := exec.Command(engine, "--base", base, "--json")
			cmd.Dir = dir
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("tools/verify-scope --base %s --json: %v\nstderr: %s", base, err, stderr.String())
			}
			var rep struct {
				Mode        string   `json:"mode"`
				Analyzer    *string  `json:"analyzer"`
				Diagnostics []string `json:"diagnostics"`
			}
			if err := json.Unmarshal(out, &rep); err != nil {
				t.Fatalf("--json is not one JSON object: %v\n%s", err, out)
			}
			if rep.Analyzer == nil || *rep.Analyzer != c.analyzer {
				t.Fatalf("engine analyzer = %v, want %s (fixture shape)", rep.Analyzer, c.analyzer)
			}
			engineSet := []string{}
			for _, d := range rep.Diagnostics {
				if m := w248EngineSuiteDiag.FindStringSubmatch(d); m != nil {
					s := m[1]
					switch {
					case m[3] != "":
						s = m[3] + " (e2e_map names no discovered suite)"
					case strings.HasPrefix(m[2], "has a malformed"):
						s += " (malformed carries:)"
					}
					engineSet = append(engineSet, s)
				}
			}
			sort.Strings(engineSet)

			facts := scopedGatesReadConf(filepath.Join(dir, filepath.FromSlash(conf)))
			rowSet := []string{}
			rowFull := len(facts.rejected) > 0
			if !rowFull {
				_, undeclared := scopedGatesCarries(dir, c.analyzer, facts)
				rowSet = append(rowSet, undeclared...)
				rowFull = len(undeclared) > 0
			}
			sort.Strings(rowSet)

			if engineFull := rep.Mode == "full"; engineFull != rowFull {
				t.Errorf("mode drift: engine %q (diagnostics %q), doctor row full=%v (undeclared %q)", rep.Mode, rep.Diagnostics, rowFull, rowSet)
			}
			if !reflect.DeepEqual(engineSet, rowSet) {
				t.Errorf("suite drift: engine names %q, doctor row names %q (diagnostics %q)", engineSet, rowSet, rep.Diagnostics)
			}
		})
	}
}

// TestW248ScopeGatesSmokeParity: the doctor row's smoke_suite= reading — the
// declared list, a rejected (empty or malformed) line, and a smoke suite that
// names no discovered suite — agrees with the engine's `smoke_suite` report
// and its full-tier selection diagnostics (`--full --json`), through the one
// Go parser (scopedGatesReadConf) and no other.
func TestW248ScopeGatesSmokeParity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed: the parity fixtures are git repositories")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed: tools/verify-scope --json needs it (a documented prerequisite), so the engine half cannot run")
	}
	engine, err := filepath.Abs(filepath.Join("..", "..", "..", "tools", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	const (
		pkg  = `{"name":"w248","scripts":{"test:e2e:all":"node e2e/all.mjs"}}` + "\n"
		conf = ".polyforge/verify-scope.conf"
		head = "full_command=npm run test:e2e:all\n"
	)
	missingDiag := regexp.MustCompile(`smoke_suite (\S+) is not a discovered suite`)
	cases := []struct {
		name, conf string
	}{
		{"valid list", head + "smoke_suite=e2e/a.mjs\nsmoke_suite=e2e/b.mjs\nsmoke_suite=e2e/a.mjs\n"},
		{"empty line", head + "smoke_suite=e2e/a.mjs\nsmoke_suite=\n"},
		{"not a discovered suite", head + "smoke_suite=e2e/a.mjs\nsmoke_suite=e2e/gone.mjs\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			w248Git(t, dir, "init", "-q", "-b", "main")
			files := map[string]string{"package.json": pkg, "src/a.js": "1\n", conf: c.conf,
				"e2e/a.mjs": "// carries: src/*\nconsole.log(1)\n", "e2e/b.mjs": "// carries: lib/*\nconsole.log(2)\n"}
			for rel, body := range files {
				w248Write(t, dir, rel, body)
			}
			w248Git(t, dir, "add", "-A")
			w248Git(t, dir, "commit", "-q", "-m", "base")
			base := w248Git(t, dir, "rev-parse", "HEAD")
			w248Write(t, dir, "src/a.js", "2\n")
			w248Git(t, dir, "commit", "-q", "-am", "change")

			cmd := exec.Command(engine, "--base", base, "--full", "--json")
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("tools/verify-scope --full --json: %v", err)
			}
			var rep struct {
				SmokeSuite  []string `json:"smoke_suite"`
				Diagnostics []string `json:"diagnostics"`
			}
			if err := json.Unmarshal(out, &rep); err != nil {
				t.Fatalf("--json: %v\n%s", err, out)
			}
			engineRejected, engineMissing := false, []string{}
			for _, d := range rep.Diagnostics {
				if strings.Contains(d, "conf rejects smoke_suite value") {
					engineRejected = true
				}
				if m := missingDiag.FindStringSubmatch(d); m != nil {
					engineMissing = append(engineMissing, m[1])
				}
			}

			facts := scopedGatesReadConf(filepath.Join(dir, filepath.FromSlash(conf)))
			if !reflect.DeepEqual(append([]string{}, facts.smoke...), append([]string{}, rep.SmokeSuite...)) {
				t.Errorf("smoke list drift: engine %q, doctor row %q", rep.SmokeSuite, facts.smoke)
			}
			if rowRejected := len(facts.rejected) > 0; rowRejected != engineRejected {
				t.Errorf("rejection drift: engine %v (diagnostics %q), doctor row %v (%q)", engineRejected, rep.Diagnostics, rowRejected, facts.rejected)
			}
			if !engineRejected {
				suites, _ := scopedGatesCarries(dir, "npm", facts)
				rowMissing := append([]string{}, scopedGatesSmokeMissing(suites, facts)...)
				if !reflect.DeepEqual(rowMissing, engineMissing) {
					t.Errorf("missing-suite drift: engine %q, doctor row %q (diagnostics %q)", engineMissing, rowMissing, rep.Diagnostics)
				}
			}
		})
	}
}
