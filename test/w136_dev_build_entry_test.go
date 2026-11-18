package test

// w136_dev_build_entry_test.go (polyforge#136): the development channel's
// two repository contracts —
//
//   - the Makefile's `dev-build` target, the one build entry
//     tools/install.sh --dev calls so the installer never rewrites build
//     commands: it takes everything explicit (VERSION, COMMIT, OUT),
//     refuses an empty or non-release VERSION (the dev version must be
//     release-shaped so install.sh's SemVer downgrade guard compares it,
//     and must never carry +build metadata — a string the guard would
//     silently let through), injects both internal/version stamps at
//     link time and builds the webui frontend first like every
//     binary-producing target;
//   - tools/aihub-paths.conf, the one AIHub-side path list the installer
//     and the tests both read (the ticket's first-version scope): a
//     diff of «latest formal tag..green commit» hitting it refuses the
//     dev channel in prepare.
//
// The Makefile recipe is the interface here (the installer calls it with
// named facts and asserts on the built binary); its behavioural pins —
// the exact argv install.sh passes and the version it stamps — live in
// w136_install_dev_test.go over the make shim's journal. The conf's
// matching behaviour is pinned by TestW136InstallDevRefusesAIHubChange.
//
// carries: Makefile, tools/aihub-paths.conf, test/w136_dev_build_entry_test.go

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// w136Root answers the repository root (this package's cwd is test/).
func w136Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// TestW136DevBuildEntry: the Makefile carries the development channel's
// build entry with its guards and both link-time stamps.
func TestW136DevBuildEntry(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(w136Root(t), "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mf := string(b)
	// the target exists and builds the locked frontend first (go:embed)
	if !regexp.MustCompile(`(?m)^dev-build: webui-build$`).MatchString(mf) {
		t.Fatal("the Makefile has no `dev-build: webui-build` target: install.sh --dev must not rewrite build commands, and every binary-producing target builds the webui first")
	}
	// everything explicit: an empty VERSION/COMMIT/OUT refuses
	if !strings.Contains(mf, "-z \"$(VERSION)\"") || !strings.Contains(mf, "-z \"$(COMMIT)\"") || !strings.Contains(mf, "-z \"$(OUT)\"") {
		t.Fatal("make dev-build must refuse an empty VERSION, COMMIT or OUT (the dev channel's facts are the caller's; the target never guesses from git)")
	}
	// the VERSION guard: release-shaped, and the guard's own pattern
	// rejects +build metadata (AC3 — a dev string the installer's
	// downgrade regex would not match would silently disable the guard)
	gr := regexp.MustCompile("grep -Eq '([^']+)'").FindStringSubmatch(mf)
	if gr == nil {
		t.Fatal("make dev-build carries no grep -Eq version guard")
	}
	pat := strings.ReplaceAll(gr[1], "$$", "$")
	guard := regexp.MustCompile(pat)
	for _, ok := range []string{"v0.2.4-dev.1", "v0.2.4-dev.12", "v0.2.4", "v1.2.3-rc.4"} {
		if !guard.MatchString(ok) {
			t.Fatalf("the dev-build version guard rejects the release-shaped %q (pattern %q)", ok, gr[1])
		}
	}
	for _, bad := range []string{"dev", "v0.2.4-dev.3+g05819e2", "0.2.4", "green/20261118-1"} {
		if guard.MatchString(bad) {
			t.Fatalf("the dev-build version guard accepts %q (pattern %q): a non-release string must be refused — +build metadata would leave the installer's downgrade guard silently off (AC3)", bad, gr[1])
		}
	}
	// both link-time stamps: the version and the source commit
	if !strings.Contains(mf, "internal/version.Version=$(VERSION)") && !strings.Contains(mf, "GO_LDFLAGS") {
		t.Fatal("make dev-build must stamp the version through GO_LDFLAGS like make release")
	}
	if !strings.Contains(mf, "internal/version.Commit=$(COMMIT)") {
		t.Fatal("make dev-build must stamp the source commit into internal/version.Commit beside the version")
	}
}

// TestW136AIHubPathsConf: the AIHub-side path list is the ticket's
// first-version scope, one entry per line, and the installer and the
// tests read this one file (AC2's 安装器与测试读同一份清单).
func TestW136AIHubPathsConf(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(w136Root(t), "tools", "aihub-paths.conf"))
	if err != nil {
		t.Fatalf("tools/aihub-paths.conf is missing: the AIHub-side scope must live in one repository file the installer and the tests both read: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, l)
	}
	// the ticket's first version, verbatim
	want := []string{"deploy/**", "internal/aihub/**", "internal/common/api/**", "internal/common/protocol/**", ".polyforge/deploy.sh"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tools/aihub-paths.conf = %q, want exactly the ticket's first-version list %v", lines, want)
	}
	// the documented semantics: a /** entry is the directory prefix, an
	// exact entry matches one path — the semantics install.sh and the
	// tests implement
	head := strings.ToLower(string(b))
	if !strings.Contains(head, "**") || !strings.Contains(head, "prefix") {
		t.Fatal("tools/aihub-paths.conf must document its two shapes: a trailing /** covers the directory recursively, anything else is one exact path")
	}
}
