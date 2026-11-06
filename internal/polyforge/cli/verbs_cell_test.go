package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCellAddWritesTheFileAndRefusesArgvKeys: pf cell add writes the named
// Cell's file (the key from stdin), refuses a key on argv, an existing
// file and a reserved name; with no daemon the change waits for the next
// Host start.
func TestCellAddWritesTheFileAndRefusesArgvKeys(t *testing.T) {
	dir := cellScopeFixture(t)
	run := func(stdin string, argv ...string) (int, string, string) {
		var stdout, stderr strings.Builder
		code := Run(argv, strings.NewReader(stdin), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	if code, _, errOut := run("", "cell", "add", "beta", "--server", "http://hub-b:1", "--api-key", "k"); code == 0 || !strings.Contains(errOut, "stdin") {
		t.Fatalf("--api-key must be refused: %d %s", code, errOut)
	}
	if code, _, errOut := run("kb\n", "cell", "add", "default", "--server", "http://hub-b:1"); code == 0 || !strings.Contains(errOut, "reserved") {
		t.Fatalf("default is reserved: %d %s", code, errOut)
	}
	code, out, errOut := run("kb\n", "cell", "add", "beta", "--server", "http://hub-b:1", "--model-source", "shared-default")
	if code != 0 {
		t.Fatalf("add: %d %s %s", code, out, errOut)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cli-beta.toml"))
	if err != nil || !strings.Contains(string(raw), `server = "http://hub-b:1"`) || !strings.Contains(string(raw), `api_key = "kb"`) || !strings.Contains(string(raw), `source = "shared-default"`) {
		t.Fatalf("cli-beta.toml: %v\n%s", err, raw)
	}
	if !strings.Contains(out, "next start") {
		t.Fatalf("with no daemon the add waits for the next start: %q", out)
	}
	if code, _, errOut := run("kb\n", "cell", "add", "beta", "--server", "http://hub-b:1"); code == 0 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("an existing file must be refused: %d %s", code, errOut)
	}
}

// TestCellRefusedInStepAndTaskMode: every pf cell verb is refused inside an
// agent with DAEMON-403, before anything is written.
func TestCellRefusedInStepAndTaskMode(t *testing.T) {
	for _, marker := range []string{"PF_WI_ID", "PF_TASK_ID"} {
		t.Run(marker, func(t *testing.T) {
			dir := cellScopeFixture(t)
			t.Setenv(marker, "x")
			t.Setenv("PF_DAEMON_ID", "m-x")
			var stdout, stderr strings.Builder
			code := Run([]string{"cell", "add", "gamma", "--server", "http://hub-g:1", "--api-key", "k"}, strings.NewReader("k\n"), &stdout, &stderr)
			if code == 0 || !strings.Contains(stderr.String(), "DAEMON-403") {
				t.Fatalf("exit %d %s", code, stderr.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "cli-gamma.toml")); err == nil {
				t.Fatal("a refused agent call wrote a Cell file")
			}
		})
	}
}

// TestCellCleanupRefusesDefaultAndBadArgs: the local refusals of
// `pf cell cleanup` happen before any daemon call.
func TestCellCleanupRefusesDefaultAndBadArgs(t *testing.T) {
	cellScopeFixture(t)
	for _, argv := range [][]string{
		{"cell", "cleanup", "default", "--confirm", "default"},
		{"cell", "cleanup"},
		{"cell", "cleanup", "alpha", "--bogus"},
	} {
		var stdout, stderr strings.Builder
		if code := Run(argv, strings.NewReader(""), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "cell cleanup") {
			t.Fatalf("%v: exit %d %s", argv, code, stderr.String())
		}
	}
}
