package test

// a caller that runs a downloaded copy of tools/install.sh
// (Pi Web's install entry) passes the api key as `-` and writes the key to
// the script's stdin, so the key never rides any process's argv. A first
// install reads it there and writes cli.toml through `pf config` exactly as
// the positional key does; an upgrade (cli.toml already carries its
// api_key) needs no key, so its stdin may be empty; a first install whose
// stdin carries no key is refused before anything changes. Driven on the
// hermetic w174 installer world (scratch HOME/PF_BIN, private tmux, stubbed
// gh). The overridable refusals' own exit status (3) is pinned by the two
// w174 job-check tests.

import (
	"os"
	"strings"
	"testing"
)

// w418Stdin is the stdin fixture; its name carries no credential keyword, so
// it has no secret-scan shape.
const w418Stdin = "w418-STDIN-ONLY-EXAMPLE"

// TestW418InstallReadsKeyFromStdin: `install.sh - <host>` with the key on
// stdin writes the key into the new cli.toml, and no pf call the installer
// makes carries it in argv (the pf wrapper journals every argv).
func TestW418InstallReadsKeyFromStdin(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w418")
	if err := os.WriteFile(in.cfg, []byte("# no key yet\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in.resetJournal(t)
	in.stdin = w418Stdin + "\n"
	out, code := in.runArgs(t, "-", "127.0.0.1")
	if code != 0 {
		t.Fatalf("a first install with the key on stdin: exit %d\n%s", code, out)
	}
	cfg := w174ReadFile(t, in.cfg)
	if !strings.Contains(cfg, w418Stdin) || !strings.Contains(cfg, `server = "http://127.0.0.1:8080"`) {
		t.Fatalf("the config must carry the stdin key and the host; config:\n%s\noutput:\n%s", cfg, out)
	}
	if strings.Contains(out, w418Stdin) {
		t.Fatalf("the installer must never print the key:\n%s", out)
	}
	for _, l := range in.journalLines() {
		if strings.Contains(l, w418Stdin) {
			t.Fatalf("the key rode an argv: %q", l)
		}
	}
}

// TestW418InstallUpgradeWithEmptyStdin: an upgrade needs no key — `-` with
// an empty stdin upgrades and leaves the config byte-unchanged.
func TestW418InstallUpgradeWithEmptyStdin(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w418")
	cfgBefore := w174ReadFile(t, in.cfg)
	out, code := in.runArgs(t, "-")
	if code != 0 {
		t.Fatalf("an upgrade with `-` and no key on stdin: exit %d\n%s", code, out)
	}
	if got := w174ReadFile(t, in.bin); !strings.Contains(got, "v0.0.1-w418") {
		t.Fatal("the binary was not installed")
	}
	if got := w174ReadFile(t, in.cfg); got != cfgBefore {
		t.Fatalf("the existing config must stay unchanged (was %q, now %q)", cfgBefore, got)
	}
}

// TestW418InstallFirstInstallNeedsStdinKey: a first install given `-` and
// an empty stdin is refused, naming the missing key, before one byte of the
// machine changes.
func TestW418InstallFirstInstallNeedsStdinKey(t *testing.T) {
	in := w174NewInstall(t)
	in.release(t, "v0.0.1-w418")
	cfgBefore := "# no key yet\n"
	if err := os.WriteFile(in.cfg, []byte(cfgBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	in.resetJournal(t)
	out, code := in.runArgs(t, "-", "127.0.0.1")
	if code == 0 {
		t.Fatalf("a first install without a key on stdin must refuse; observed exit 0:\n%s", out)
	}
	if !strings.Contains(out, "api key") || !strings.Contains(out, "stdin") {
		t.Fatalf("the refusal must name the missing api key and stdin; observed:\n%s", out)
	}
	w218AssertMachineUntouched(t, in, "v0.0.1-w418", cfgBefore)
}
