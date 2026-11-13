package cli

// w288_micro_scope_engine_test.go: the engine's micro declaration
// (micro_path=, micro_check=) and its `--micro` evidence mode
// (workflows/_shared/scripts/verify-scope,). Every case runs the in-tree engine
// in a git fixture, never the release-installed copy. --json needs
// python3, a documented prerequisite — without it the test skips and says
// so.

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type microReport struct {
	Mode     string `json:"mode"`
	Commands []struct {
		Name  string `json:"name"`
		Phase string `json:"phase"`
		Cmd   string `json:"cmd"`
	} `json:"commands"`
	Paths       []string  `json:"paths"`
	MicroPaths  *[]string `json:"micro_paths"`
	MicroChecks *[]string `json:"micro_checks"`
	Diagnostics []string  `json:"diagnostics"`
}

func (r microReport) cmds() []string {
	var out []string
	for _, c := range r.Commands {
		out = append(out, c.Phase+":"+c.Name+":"+c.Cmd)
	}
	return out
}

// microFixture commits base (the files) and then the change on top; it
// answers the repo dir and the base sha.
func microFixture(t *testing.T, base, change map[string]string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed: the engine fixtures are git repositories")
	}
	dir := t.TempDir()
	w248Git(t, dir, "init", "-q", "-b", "main")
	for rel, body := range base {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	sha := w248Git(t, dir, "rev-parse", "HEAD")
	for rel, body := range change {
		w248Write(t, dir, rel, body)
	}
	w248Git(t, dir, "add", "-A")
	w248Git(t, dir, "commit", "-q", "--allow-empty", "-m", "change")
	return dir, sha
}

// microEngine runs the in-tree engine in dir; it answers stdout, stderr
// and the exit code.
func microEngine(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	engine, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(engine, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("workflows/_shared/scripts/verify-scope: %v", err)
	}
	return stdout.String(), stderr.String(), code
}

func microJSON(t *testing.T, dir string, args ...string) microReport {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed: workflows/_shared/scripts/verify-scope --json needs it (a documented prerequisite)")
	}
	out, errb, code := microEngine(t, dir, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope --json %v: exit %d\nstderr: %s", args, code, errb)
	}
	var r microReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json is not one object: %v\n%s", err, out)
	}
	return r
}

const microConf = "fast_gate=echo FAST\n" +
	"micro_path=docs/**\n" +
	"micro_path=lib/i18n/*.json\n" +
	"micro_check=echo MICRO-A\n" +
	"full_command=echo FULL\n" +
	"micro_check=echo MICRO-B\n"

// TestW288MicroEngineCommandsAndEcho: --micro reports exactly the fast
// phase followed by the micro checks in file order (phase micro, named
// micro-<i>), mode micro, no full command; the declaration is echoed.
func TestW288MicroEngineCommandsAndEcho(t *testing.T) {
	dir, base := microFixture(t, map[string]string{".polyforge/verify-scope.conf": microConf, "docs/a.md": "a\n"},
		map[string]string{"docs/a.md": "b\n"})
	r := microJSON(t, dir, "--base", base, "--micro")
	want := []string{"fast:fast-1:echo FAST", "micro:micro-1:echo MICRO-A", "micro:micro-2:echo MICRO-B"}
	if !reflect.DeepEqual(r.cmds(), want) {
		t.Fatalf("--micro commands %v, want %v", r.cmds(), want)
	}
	if r.Mode != "micro" {
		t.Fatalf("--micro mode %q, want micro", r.Mode)
	}
	if r.MicroPaths == nil || !reflect.DeepEqual(*r.MicroPaths, []string{"docs/**", "lib/i18n/*.json"}) {
		t.Fatalf("micro_paths %v", r.MicroPaths)
	}
	if r.MicroChecks == nil || !reflect.DeepEqual(*r.MicroChecks, []string{"echo MICRO-A", "echo MICRO-B"}) {
		t.Fatalf("micro_checks %v", r.MicroChecks)
	}
	// the default report keeps its commands: no micro check there
	d := microJSON(t, dir, "--base", base)
	for _, c := range d.Commands {
		if c.Phase == "micro" {
			t.Fatalf("the default report carries a micro command: %v", d.cmds())
		}
	}
}

// TestW288MicroEngineUndeclaredKeepsKeySet: a repository that declares
// no micro key keeps the report's key set (no micro_paths/micro_checks);
// under --micro it reports empty arrays and --micro --run refuses.
func TestW288MicroEngineUndeclaredKeepsKeySet(t *testing.T) {
	dir, base := microFixture(t, map[string]string{".polyforge/verify-scope.conf": "fast_gate=echo FAST\n", "docs/a.md": "a\n"},
		map[string]string{"docs/a.md": "b\n"})
	r := microJSON(t, dir, "--base", base)
	if r.MicroPaths != nil || r.MicroChecks != nil {
		t.Fatalf("an undeclared repository's report gained micro keys: %v %v", r.MicroPaths, r.MicroChecks)
	}
	m := microJSON(t, dir, "--base", base, "--micro")
	if m.MicroPaths == nil || len(*m.MicroPaths) != 0 {
		t.Fatalf("--micro on an undeclared repository: micro_paths %v, want []", m.MicroPaths)
	}
	if _, errb, code := microEngine(t, dir, "--base", base, "--micro", "--run"); code == 0 || !strings.Contains(errb, "declares no micro_path") {
		t.Fatalf("--micro --run on an undeclared repository: exit %d, stderr %q", code, errb)
	}
}

// TestW288MicroEngineBaseConfWins: a working-tree conf the change adds a
// micro_path to is never seen by --micro; the base conf governs.
func TestW288MicroEngineBaseConfWins(t *testing.T) {
	dir, base := microFixture(t, map[string]string{".polyforge/verify-scope.conf": microConf, "docs/a.md": "a\n"},
		map[string]string{".polyforge/verify-scope.conf": microConf + "micro_path=src/**\nmicro_check=echo WIDENED\n", "src/x.go": "package x\n"})
	r := microJSON(t, dir, "--base", base, "--micro")
	if !reflect.DeepEqual(*r.MicroPaths, []string{"docs/**", "lib/i18n/*.json"}) {
		t.Fatalf("--micro read the change's own conf: micro_paths %v", *r.MicroPaths)
	}
	for _, c := range r.Commands {
		if strings.Contains(c.Cmd, "WIDENED") {
			t.Fatalf("--micro ran the change's own micro_check: %v", r.cmds())
		}
	}
	// a base with no conf at all declares nothing, whatever HEAD carries
	dir2, base2 := microFixture(t, map[string]string{"docs/a.md": "a\n"},
		map[string]string{".polyforge/verify-scope.conf": microConf})
	r2 := microJSON(t, dir2, "--base", base2, "--micro")
	if len(*r2.MicroPaths) != 0 || len(r2.Commands) != 0 {
		t.Fatalf("a conf absent at the base must declare nothing: %v %v", *r2.MicroPaths, r2.cmds())
	}
}

// TestW288MicroEngineKeyValidation: a rejected micro_path is diagnosed and
// contributes nothing; a micro_check that begins with a substitution is
// lint-skipped; micro-<n> is a reserved check name.
func TestW288MicroEngineKeyValidation(t *testing.T) {
	conf := "fast_gate=echo FAST\n" +
		"micro_path=/abs/**\nmicro_path=./docs/*\nmicro_path=../up\nmicro_path=docs/../x\nmicro_path=\n" +
		"micro_path=docs/**\nmicro_path=docs/**\n" +
		"micro_check=$(evil)\n" +
		"check=micro-1|fast|echo RESERVED\n"
	dir, base := microFixture(t, map[string]string{".polyforge/verify-scope.conf": conf}, nil)
	r := microJSON(t, dir, "--base", base, "--micro")
	if !reflect.DeepEqual(*r.MicroPaths, []string{"docs/**"}) {
		t.Fatalf("micro_paths %v, want only the valid, de-duplicated docs/**", *r.MicroPaths)
	}
	if len(*r.MicroChecks) != 0 {
		t.Fatalf("a substitution micro_check was kept: %v", *r.MicroChecks)
	}
	joined := strings.Join(r.Diagnostics, "\n")
	for _, want := range []string{"micro_path=/abs/**", "micro_path=./docs/*", "micro_path=../up", "micro_path=docs/../x", "micro_check value begins with a command substitution", "reserved for the anonymous and analyzer commands"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diagnostics lack %q:\n%s", want, joined)
		}
	}
}

// TestW288MicroEngineRunStopsAtFirstRed: --micro --run executes the fast
// phase then the micro checks in order from the repository root and stops
// at the first non-zero; a missing prerequisite is a failure, never a skip.
func TestW288MicroEngineRunStopsAtFirstRed(t *testing.T) {
	conf := "fast_gate=echo ran-fast >>trace\n" +
		"micro_path=docs/**\n" +
		"micro_check=echo ran-micro-1 >>trace; false\n" +
		"micro_check=echo ran-micro-2 >>trace\n"
	dir, base := microFixture(t, map[string]string{".polyforge/verify-scope.conf": conf}, nil)
	_, errb, code := microEngine(t, dir, "--base", base, "--micro", "--run")
	if code == 0 {
		t.Fatalf("--micro --run exited 0 although a micro check is red (stderr %q)", errb)
	}
	out, err := exec.Command("cat", filepath.Join(dir, "trace")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(out)); !reflect.DeepEqual(got, []string{"ran-fast", "ran-micro-1"}) {
		t.Fatalf("--micro --run ran %v, want ran-fast then ran-micro-1 and a stop", got)
	}

	conf2 := "fast_gate=true\nmicro_path=docs/**\ncheck_requires_env=micro-1|W288_SURELY_UNSET_VAR\nmicro_check=true\n"
	dir2, base2 := microFixture(t, map[string]string{".polyforge/verify-scope.conf": conf2}, nil)
	if _, errb, code := microEngine(t, dir2, "--base", base2, "--micro", "--run"); code == 0 || !strings.Contains(errb, "skipped micro check is a failure") {
		t.Fatalf("--micro --run with a missing prerequisite: exit %d, stderr %q", code, errb)
	}
}
