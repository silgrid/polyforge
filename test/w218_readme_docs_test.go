package test

// polyforge#218 e2e: the platform support matrix (darwin
// hardware-verified, per polyforge#247 after the Mac run passed against the
// released darwin assets) and the macOS Quickstart's machine registration
// (B6). Spec (artifacts/spec.md, "## Requirements"): the documentation
// MUST state the platform support matrix (WSL2 one line, treated as Linux;
// native Windows out of scope), with darwin stated hardware-verified; and
// "The README macOS Quickstart MUST make machine registration real and
// MUST state it accurately" — step 3 grants the machine user the
// maintainer role (`projects members add demo --user <MACHINE_USER_ID>
// --role maintainer`, run with the bootstrap key) so the daemon's first
// heartbeat actually registers the machine, and step 6 states the accurate
// conditional (registers because its key's user is a maintainer of demo;
// registration requires an admin or a project maintainer; a failure shows
// in ~/.local/share/polyforge/daemon.log) — no unconditional
// self-registration claim remains. (polyforge#254 moved the matrix into
// docs/install.md and the Quickstart into docs/quickstart-macos.md; the
// assertions follow, intact.)
//
// The assertions are on the statements, not the wording: any line pairing
// the platform with its verdict matches; the Quickstart's commands and
// conditions are matched whitespace-tolerantly (a long command may wrap).

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	w218ReDarwinArm64  = regexp.MustCompile(`(?i)\bdarwin\b[^\n]*\barm64\b`)
	w218ReDarwinAmd64  = regexp.MustCompile(`(?i)\bdarwin\b[^\n]*\bamd64\b`)
	w218ReWsl2Linux    = regexp.MustCompile(`(?i)(\bwsl[\s-]?2\b[^\n]*\blinux\b|\blinux\b[^\n]*\bwsl[\s-]?2\b)`)
	w218ReWindowsScope = regexp.MustCompile(`(?i)\bwindows\b[^\n]*(\bout of scope\b|\bnot in scope\b|\bbeyond scope\b|\bnot supported\b|\bunsupported\b)`)

	// darwin support is stated hardware-verified (the Mac run passed
	// against the released darwin assets) — matched on the
	// whitespace-normalized body so a wrapped sentence still pairs.
	w218ReHardwareVerified = regexp.MustCompile(`(?i)darwin\b.{0,400}?\bhardware[\s-]verified\b`)

	// B6: the maintainer grant (the spec pins the verb) ...
	w218ReGrant = regexp.MustCompile(`(?i)projects\s+members\s+add\s+demo\b.{0,200}?--user\b.{0,80}?--role\s+maintainer\b`)
	// ... and the accurate conditional: the first heartbeat registers
	// this machine BECAUSE its key's user is a maintainer of demo
	// (registration requires an admin or a project maintainer).
	w218ReFirstHeartbeat  = regexp.MustCompile(`(?i)first\s+heartbeat\b.{0,400}?\bmaintainer\b`)
	w218ReAdminMaintainer = regexp.MustCompile(`(?i)\badmin\b.{0,120}?\bmaintainer\b`)
	w218ReFailDaemonLog   = regexp.MustCompile(`(?i)\bfail(?:s|ed|ure)?\b.{0,240}?daemon\.log\b`)
	// the false unconditional claim B6 removed (the reference's sentence)
	w218ReNoSeparateRegistration = regexp.MustCompile(`(?i)no\s+separate\s+registration`)
)

// w218Page answers one doc-set page's body and its
// whitespace-normalized form (polyforge#254 moved the platform matrix
// into docs/install.md and the macOS Quickstart into
// docs/quickstart-macos.md; the assertions follow the content).
func w218Page(t *testing.T, rel string) (string, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	body := string(raw)
	norm := regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
	return body, norm
}

// TestW218ReadmePlatformMatrix: the docs MUST state the platform support
// matrix — darwin arm64 and amd64 are supported platforms, stated
// hardware-verified; one line says WSL2 is treated as Linux; native
// Windows is out of scope. (polyforge#254 moved the matrix from README
// into docs/install.md; the assertions are intact, reading the new
// location.)
func TestW218ReadmePlatformMatrix(t *testing.T) {
	body, norm := w218Page(t, filepath.Join("docs", "install.md"))
	var missing []string
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"darwin arm64 is a supported platform", w218ReDarwinArm64.MatchString(body)},
		{"darwin amd64 is a supported platform", w218ReDarwinAmd64.MatchString(body)},
		{"darwin support stated hardware-verified", w218ReHardwareVerified.MatchString(norm)},
		{"one line saying WSL2 is treated as Linux", w218ReWsl2Linux.MatchString(body)},
		{"native Windows is out of scope", w218ReWindowsScope.MatchString(body)},
	} {
		if !c.ok {
			missing = append(missing, c.name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("docs/install.md must state the platform support matrix (darwin arm64 + amd64 supported, hardware-verified; WSL2 treated as Linux; native Windows out of scope); missing statements: %s", strings.Join(missing, "; "))
	}
}

// TestW218ReadmeQuickstartRegistration: the macOS Quickstart MUST make
// machine registration real and state it accurately (B6): the
// project-creation step grants the machine user the maintainer role on
// demo (the spec's pinned verb), BEFORE the daemon start whose accurate
// conditional — the first heartbeat registers this machine because its
// key's user is a maintainer of demo (registration requires an admin or a
// project maintainer), a failure shows in daemon.log — replaces the false
// unconditional self-registration claim.
func TestW218ReadmeQuickstartRegistration(t *testing.T) {
	_, norm := w218Page(t, filepath.Join("docs", "quickstart-macos.md"))
	grant := w218ReGrant.FindStringIndex(norm)
	if grant == nil {
		t.Fatalf("the Quickstart's project-creation step must grant the machine user the maintainer role on demo (projects members add demo --user <MACHINE_USER_ID> --role maintainer, run with the bootstrap key) so the daemon's first heartbeat actually registers the machine; no such command exists in docs/quickstart-macos.md")
	}
	cond := w218ReFirstHeartbeat.FindStringIndex(norm)
	if cond == nil {
		t.Fatalf("the Quickstart's daemon-start step must state the accurate conditional: the first heartbeat registers this machine because its key's user is a maintainer of demo (registration requires an admin or a project maintainer); no such statement exists in docs/quickstart-macos.md")
	}
	if cond[0] < grant[0] {
		t.Fatalf("the maintainer grant must precede the daemon start (the machine registers on the first heartbeat BECAUSE its key's user already is a maintainer); grant at %d, conditional at %d", grant[0], cond[0])
	}
	window := norm[cond[0]:]
	if !w218ReAdminMaintainer.MatchString(window[:min(len(window), 500)]) {
		t.Fatalf("the conditional must state the registration requirement (an admin or a project maintainer); observed near the conditional: %q", window[:min(len(window), 300)])
	}
	if !w218ReFailDaemonLog.MatchString(norm) {
		t.Fatal("the Quickstart must state that a registration failure shows in ~/.local/share/polyforge/daemon.log")
	}
	if m := w218ReNoSeparateRegistration.FindString(norm); m != "" {
		t.Fatalf("docs/quickstart-macos.md still carries the unconditional self-registration claim B6 removed (%q): registration requires the maintainer grant of the project-creation step", m)
	}
}
