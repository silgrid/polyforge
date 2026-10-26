package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/version"
)

// TestRunVersionFlag pins the --version flag on the pf CLI's real entry
// surface (Run, which cmd/pf/main.go dispatches to with os.Args[1:]).
// The goal (wi_DBOTR7Q6Kcr3) requires: a build-injected version variable
// (ldflags -X, git tag at build) and a --version flag on pf that prints
// it and exits 0. Both the single-dash and double-dash spellings must
// work (Go's flag package accepts either).
func TestRunVersionFlag(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"double dash", []string{"--version"}},
		{"single dash", []string{"-version"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(tc.argv, nil, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("Run(%q) exited %d, want 0; --version must succeed.\nstderr:\n%s",
					tc.argv, code, stderr.String())
			}
			got := strings.TrimSpace(stdout.String())
			if got == "" {
				t.Fatal("--version printed nothing on stdout; it must print the build-injected version string")
			}
			if strings.Contains(got, "Usage") || strings.Contains(got, "polyforge v1 client CLI") {
				t.Fatalf("--version printed usage text instead of a version:\n%s", got)
			}
		})
	}
}

// TestRunVersionVerb pins the `version` verb (wi_gixPL0tarfnV): the
// build string on the dispatch, the same source and output shapes as
// the --version flag path (TestRunVersionFlag), listed by `pf help`,
// and an arity error (exit 1, the doctor convention) on extra
// positionals. NoConfigLoad means it answers with no config at all.
func TestRunVersionVerb(t *testing.T) {
	// Success: exit 0, stdout is exactly the version string (one line),
	// no usage text.
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"version"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("pf version exited %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != version.Version {
		t.Fatalf("pf version printed %q, want the build-injected %q", got, version.Version)
	}
	if strings.Contains(stdout.String(), "Usage") || strings.Contains(stdout.String(), "pf v") {
		t.Fatalf("pf version printed usage text instead of a version:\n%s", stdout.String())
	}

	// --json: the same JSON line the --version flag path prints.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--json", "version"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("pf --json version exited %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != fmt.Sprintf("{\"version\":%q}", version.Version) {
		t.Fatalf("pf --json version printed %q, want the flag path's JSON shape", got)
	}

	// NoConfigLoad: the verb answers even with a malformed --config —
	// LoadConfig refuses loudly on a malformed file (normal verbs exit
	// 1), version must not: the same reachability the pre-dispatch
	// --version flag has.
	broken := filepath.Join(t.TempDir(), "broken.toml")
	if err := os.WriteFile(broken, []byte("not valid toml at all"), 0o600); err != nil {
		t.Fatalf("cannot create the malformed --config fixture: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"--config", broken, "version"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("pf --config <malformed> version exited %d, want 0 (NoConfigLoad:\nthe verb must answer where the config is unloadable, like the --version flag); stderr:\n%s", code, stderr.String())
	}

	// `pf help` lists the verb as a registry row under `Diagnostics:`
	// (the WI's acceptance: reachable without knowing the flag). A plain
	// substring/counter check is vacuous here: the pre-existing global
	// `--version` flag line already contains "version" twice, so only
	// the positional row pins the listing. Registry rows are indented
	// two spaces and group headers sit at column 0, so the pattern also
	// keeps the row inside the Diagnostics group (no other header
	// between `Diagnostics:` and the row).
	helpRow := regexp.MustCompile(`Diagnostics:\n(?:  [^\n]*\n)*  version\s+Print the build version`)
	help := new(bytes.Buffer)
	if code := Run([]string{"help"}, nil, help, &stderr); code != 0 {
		t.Fatalf("pf help exited %d, want 0; stderr:\n%s", code, stderr.String())
	}
	if !helpRow.MatchString(help.String()) {
		t.Fatalf("pf help does not list the version verb as a Diagnostics\nrow (want `  version        Print the build version of this pf binary` after `Diagnostics:`):\n%s", help.String())
	}

	// Arity: extra positionals are a local validation error, exit 1 (not
	// an unknown-command 2), with the doctor-convention wording.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"version", "wi_extra"}, nil, &stdout, &stderr); code != 1 {
		t.Fatalf("pf version wi_extra exited %d, want 1; stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "version: unexpected arguments") {
		t.Fatalf("stderr = %q, want the arity error wording", stderr.String())
	}
}
