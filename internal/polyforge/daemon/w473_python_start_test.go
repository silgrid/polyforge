package daemon

// w473_python_start_test.go: the daemon resolves the step
// scripts' interpreter once at its start — [daemon] python, else python3
// on its PATH — and refuses to start (exit 78, naming why) when it is
// missing or older than 3.9, before any machine state is touched.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonRefusesAnOldOrMissingPython(t *testing.T) {
	// the mapped workspace does not exist: a start that got past the
	// interpreter check would be refused there instead (naming the
	// workspace, not the interpreter) — never run a daemon in this test
	ws := filepath.Join(t.TempDir(), "absent-ws")
	old := filepath.Join(t.TempDir(), "python3.8")
	if err := os.WriteFile(old, []byte("#!/bin/sh\necho 3.8.10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := func(t *testing.T, daemonTable string) (int, string, string) {
		t.Helper()
		cfgPath := filepath.Join(t.TempDir(), "cli.toml")
		body := "server = \"http://127.0.0.1:1\"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n" + daemonTable
		if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		data := filepath.Join(t.TempDir(), "data")
		var stderr bytes.Buffer
		code := Main([]string{"--config", cfgPath, "-data-dir", data}, nil, &bytes.Buffer{}, &stderr)
		return code, stderr.String(), data
	}

	t.Run("configured 3.8", func(t *testing.T) {
		code, errb, data := start(t, "[daemon]\npython = \""+old+"\"\n")
		if code != ExitNeedsOperator {
			t.Fatalf("exit %d, want %d; stderr %q", code, ExitNeedsOperator, errb)
		}
		for _, w := range []string{"refusing to start", "[daemon] python", old, "Python 3.8.10", "older than 3.9"} {
			if !strings.Contains(errb, w) {
				t.Errorf("the refusal lacks %q: %q", w, errb)
			}
		}
		if _, err := os.Stat(data); !os.IsNotExist(err) {
			t.Errorf("the refused start touched the data dir: %v", err)
		}
	})
	t.Run("no python3 on PATH", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		code, errb, _ := start(t, "")
		if code != ExitNeedsOperator || !strings.Contains(errb, "python3 is not on PATH") {
			t.Fatalf("exit %d; stderr %q", code, errb)
		}
	})
}
