package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// compatReason answers the typed reason of one refused pi dispatch, or ""
// when the error carries none.
func compatReason(err error) string {
	var r interface{ Reason() string }
	if errors.As(err, &r) {
		return r.Reason()
	}
	return ""
}

// compatFakePi writes an executable shell "pi" whose --version and --help
// answers are scripted; the process never reaches the capability probe.
func compatFakePi(t *testing.T, versionScript, helpScript string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "pi")
	body := "#!/bin/sh\ncase \"$1\" in\n--version)\n" + versionScript + "\n;;\n--help)\n" + helpScript + "\n;;\nesac\nexit 0\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

const compatAllFlags = `printf '%s\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions --no-approve'`

// TestPiCompatReasons pins the stable reason and the readable prompt of the
// compatibility gate (polyforge#227 AC1): a version the target cannot report
// and a required flag it does not advertise are each their own class, never
// a generic failure.
func TestPiCompatReasons(t *testing.T) {
	t.Run("version", func(t *testing.T) {
		bin := compatFakePi(t, "exit 3", compatAllFlags)
		err := validatePiCompatibility(context.Background(), bin)
		if err == nil {
			t.Fatal("a target that cannot report its version must be refused")
		}
		if got := compatReason(err); got != "pi_version" {
			t.Fatalf("reason = %q, want pi_version (%v)", got, err)
		}
		for _, want := range []string{"pi compatibility rejected [pi_version]", "cannot determine executable version"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
	t.Run("version-empty", func(t *testing.T) {
		bin := compatFakePi(t, "printf '\\n'", compatAllFlags)
		err := validatePiCompatibility(context.Background(), bin)
		if got := compatReason(err); got != "pi_version" {
			t.Fatalf("reason = %q, want pi_version (%v)", got, err)
		}
	})
	t.Run("flag", func(t *testing.T) {
		bin := compatFakePi(t, "printf '9.9.9\\n'", `printf '%s\n' '--no-skills --no-context-files --no-prompt-templates --no-extensions'`)
		err := validatePiCompatibility(context.Background(), bin)
		if err == nil {
			t.Fatal("a target missing --no-approve must be refused")
		}
		if got := compatReason(err); got != "pi_flag" {
			t.Fatalf("reason = %q, want pi_flag (%v)", got, err)
		}
		for _, want := range []string{"pi compatibility rejected [pi_flag]", "required flag is unavailable: --no-approve"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
	t.Run("resources", func(t *testing.T) {
		dir := t.TempDir()
		// a snapshot carrying the three files the pre-#227 check compared,
		// and missing the guard: the full built-in set is the gate's unit.
		for _, rel := range []string{
			"agents/worker.md",
			"extensions/subagent/index.ts",
			"extensions/subagent/agents.ts",
			"extensions/subagent/SOURCE.md",
			"extensions/guard/rules.ts",
		} {
			raw, err := stepagentassets.FS.ReadFile(rel)
			if err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		err := validateEmbeddedStepAgentDir(dir)
		if err == nil {
			t.Fatal("a snapshot missing the guard extension must be refused")
		}
		for _, want := range []string{"embedded resource validation failed", "extensions/guard/index.ts"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
}
