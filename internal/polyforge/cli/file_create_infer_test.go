package cli

// file_create_infer_test.go: the file-create default — the cwd →
// workspace containment through the expanded cli.toml [workspace]
// mapping, the single-project default, the ambiguity guards, and the
// bypass of an explicit --project.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withWorkspaceMappingAt writes a scratch cli.toml whose [workspace]
// section maps the GIVEN workspace root to the given projects, and
// answers the XDG_CONFIG_HOME env entry pointing a runPf call at it.
func withWorkspaceMappingAt(t *testing.T, ws string, projects ...string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"http://infer-config.invalid:1\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"" + strings.Join(projects, "\", \"") + "\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"XDG_CONFIG_HOME": xdg}
}

// runPfInCwd drives runPf with the inference cwd seam pinned to cwd (the
// production seam reads the process working directory; the test pins it
// — the CLI itself never chdirs).
func runPfInCwd(t *testing.T, cwd string, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	orig := osGetwd
	osGetwd = func() (string, error) { return cwd, nil }
	defer func() { osGetwd = orig }()
	return runPf(t, env, args...)
}

// inferFixture builds the shared shape: a fake aihub and a scratch
// workspace mapped (cli.toml) to the given projects.
func inferFixture(t *testing.T, projects ...string) (env map[string]string, ws string) {
	t.Helper()
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	ws = t.TempDir()
	env = withWorkspaceMappingAt(t, ws, projects...)
	env["PF_SERVER"] = srv.URL
	env["PF_API_KEY"] = "k_cli"
	return env, ws
}

// A cwd inside a mapped workspace that lists exactly one project infers
// it: the create proceeds as if --project had been passed.
func TestFileCreateInferenceSingleProject(t *testing.T) {
	env, ws := inferFixture(t, "e2e")
	code, _, stderr := runPfInCwd(t, filepath.Join(ws, "sub"), env, "file", "create", "--goal", "inferred goal")
	if code != 0 {
		t.Fatalf("inferred create: code=%d stderr=%s", code, stderr)
	}
	code, _, stderr = runPfInCwd(t, ws, env, "file", "create", "--goal", "inferred at root")
	if code != 0 {
		t.Fatalf("inferred create at the workspace root: code=%d stderr=%s", code, stderr)
	}
}

// A workspace mapping several projects errors asking for --project.
func TestFileCreateInferenceMultiProjectAsksForFlag(t *testing.T) {
	env, ws := inferFixture(t, "e2e", "second")
	code, _, stderr := runPfInCwd(t, ws, env, "file", "create", "--goal", "no pick")
	if code != 1 {
		t.Fatalf("multi-project inference: code=%d, want 1", code)
	}
	if !strings.Contains(stderr, "--project") || !strings.Contains(stderr, "e2e") || !strings.Contains(stderr, "second") {
		t.Fatalf("multi-project stderr %q must ask for --project and name the projects", stderr)
	}
}

// A cwd matching no mapped workspace keeps the #113 required-flag error.
func TestFileCreateInferenceOutsideKeepsRequiredFlagError(t *testing.T) {
	env, _ := inferFixture(t, "e2e")
	code, _, stderr := runPfInCwd(t, t.TempDir(), env, "file", "create", "--goal", "elsewhere")
	if code != 1 {
		t.Fatalf("cwd-outside inference: code=%d, want 1", code)
	}
	for _, tok := range []string{"--project is required", "polyforge#113", "[workspace", "doctor"} {
		if !strings.Contains(stderr, tok) {
			t.Fatalf("stderr %q misses %q (the #113 error must stand verbatim)", stderr, tok)
		}
	}
}

// An explicit --project bypasses inference: it creates even where
// inference alone would be ambiguous (two projects mapped).
func TestFileCreateInferenceExplicitProjectBypasses(t *testing.T) {
	env, ws := inferFixture(t, "e2e", "second")
	code, _, stderr := runPfInCwd(t, ws, env, "file", "create", "--project", "e2e", "--goal", "explicit wins")
	if code != 0 {
		t.Fatalf("explicit create over inference: code=%d stderr=%s", code, stderr)
	}
}

// Nested (degenerate) mapping config: the cwd inside two mapped
// workspaces errors asking for --project, naming the ambiguity.
func TestFileCreateInferenceNestedWorkspacesAskForFlag(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"http://infer-nested.invalid:1\"\n\n" +
		"[workspace.\"" + outer + "\"]\nprojects = [\"outer\"]\n\n" +
		"[workspace.\"" + inner + "\"]\nprojects = [\"inner\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, cannedWI)
	})
	env := map[string]string{"XDG_CONFIG_HOME": xdg, "PF_SERVER": srv.URL, "PF_API_KEY": "k_cli"}
	code, _, stderr := runPfInCwd(t, inner, env, "file", "create", "--goal", "nested")
	if code != 1 {
		t.Fatalf("nested-workspace inference: code=%d, want 1", code)
	}
	if !strings.Contains(stderr, "--project") || !strings.Contains(stderr, outer) || !strings.Contains(stderr, inner) {
		t.Fatalf("nested stderr %q must ask for --project and name the ambiguity", stderr)
	}
}
