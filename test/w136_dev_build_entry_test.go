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
//     diff of «latest formal tag..green commit» hitting a path OUTSIDE
//     the interface list warns the dev channel's run (since
//     polyforge#188; the interface subset refuses — see below);
//   - tools/aihub-interface-paths.conf (polyforge#188), the one
//     client-server interface list the installer and the tests both
//     read: a diff of «latest formal tag..green commit» hitting it
//     refuses the dev channel in prepare, judged independently of the
//     total list.
//
// The Makefile recipe is the interface here (the installer calls it with
// named facts and asserts on the built binary); its behavioural pins —
// the exact argv install.sh passes and the version it stamps — live in
// w136_install_dev_test.go over the make shim's journal. The confs'
// matching behaviour is pinned by TestW136InstallDevRefusesInterfaceChange
// and TestW136InstallDevWarnsOnAIHubInternalChange.
//
// carries: Makefile, tools/aihub-paths.conf, tools/aihub-interface-paths.conf, test/w136_dev_build_entry_test.go

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
	// both link-time stamps: the dev-build recipe stamps the version through
	// GO_LDFLAGS (as make release does) and the source commit beside it,
	// in one -ldflags invocation
	if !strings.Contains(mf, `ldflags "$(GO_LDFLAGS) -X github.com/silgrid/polyforge/internal/version.Commit=$(COMMIT)"`) {
		t.Fatal("make dev-build must stamp both internal/version fields at link time: the version through GO_LDFLAGS and the commit through its own -X")
	}
	if !strings.Contains(mf, "internal/version.Commit=$(COMMIT)") {
		t.Fatal("make dev-build must stamp the source commit into internal/version.Commit beside the version")
	}
}

// w136ConfLines answers one repository path list's entries: the
// non-blank, non-comment lines of its own file.
func w136ConfLines(t *testing.T, rel string) ([]string, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(w136Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s is missing: the path scope must live in one repository file the installer and the tests both read: %v", rel, err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		lines = append(lines, l)
	}
	return lines, string(b)
}

// TestW136AIHubPathsConf: the AIHub-side path list is the ticket's
// first-version scope, one entry per line, and the installer and the
// tests read this one file (AC2's 安装器与测试读同一份清单).
func TestW136AIHubPathsConf(t *testing.T) {
	lines, raw := w136ConfLines(t, "tools/aihub-paths.conf")
	// the ticket's first version, verbatim
	want := []string{"deploy/**", "internal/aihub/**", "internal/common/api/**", "internal/common/protocol/**", ".polyforge/deploy.sh"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tools/aihub-paths.conf = %q, want exactly the ticket's first-version list %v", lines, want)
	}
	// the documented semantics: a /** entry is the directory prefix, an
	// exact entry matches one path — the semantics install.sh and the
	// tests implement
	head := strings.ToLower(raw)
	if !strings.Contains(head, "**") || !strings.Contains(head, "prefix") {
		t.Fatal("tools/aihub-paths.conf must document its two shapes: a trailing /** covers the directory recursively, anything else is one exact path")
	}
}

// TestW136AIHubInterfacePathsConf (polyforge#188): the client-server
// interface list is the ticket's first-version scope — the two shared
// wire-contract packages — one entry per line, read by the installer
// from the green commit's tree and by the tests from the repository
// (one list, both readers). The two lists' first-version shape: every
// interface entry is also a total-list entry, the refuse-grade subset
// of the AIHub-side set (the installer's independence — an interface
// hit outside the total list still refuses — is pinned by
// TestW136InstallDevJudgesTheGreenCommitLists).
func TestW136AIHubInterfacePathsConf(t *testing.T) {
	lines, raw := w136ConfLines(t, "tools/aihub-interface-paths.conf")
	// the ticket's first version, verbatim
	want := []string{"internal/common/api/**", "internal/common/protocol/**"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tools/aihub-interface-paths.conf = %q, want exactly the ticket's first-version list %v", lines, want)
	}
	// the documented semantics, the same two shapes the total list documents
	head := strings.ToLower(raw)
	if !strings.Contains(head, "**") || !strings.Contains(head, "prefix") {
		t.Fatal("tools/aihub-interface-paths.conf must document its two shapes: a trailing /** covers the directory recursively, anything else is one exact path")
	}
	// the first-version relationship: every interface entry is also a
	// total-list entry (the refuse-grade subset of the AIHub-side set)
	total, _ := w136ConfLines(t, "tools/aihub-paths.conf")
	set := map[string]bool{}
	for _, l := range total {
		set[l] = true
	}
	for _, e := range want {
		if !set[e] {
			t.Fatalf("the interface entry %s is not a total-list entry: the first-version shape keeps the interface set inside tools/aihub-paths.conf's AIHub-side set", e)
		}
	}
}
