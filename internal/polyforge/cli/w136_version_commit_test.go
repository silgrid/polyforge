package cli

// w136_version_commit_test.go (polyforge#136): the build's source commit —
// where the development channel's provenance lives. Three pins:
//
//   - `pf --json version` gains a `commit` field when the build carries one
//     (internal/version.Commit, stamped by make dev-build), and omits it
//     entirely on a release build — the release line stays byte-identical
//     to the pre-#136 shape, so Pi Web's capability read
//     (pf --json --version) and every script that keys on {version,
//     capabilities} are untouched.
//   - `pf doctor` gains the Host-level `pf build` row: the version, and the
//     source commit beside it on a development build.
//   - the documents that describe the two (the version verb's page, the
//     command reference, the doctor verb's check enumeration) name them —
//     here the text IS the behaviour the acceptance pins.
//
// carries: internal/version/version.go, internal/polyforge/cli/verbs_version.go, internal/polyforge/cli/doctor_build.go, internal/polyforge/cli/verbs_doctor.go, skills/polyforge/verbs/version.md, skills/polyforge/verbs/doctor.md, docs/reference/commands.md

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/version"
)

// w136RunJSON runs pf with argv and answers stdout.
func w136RunJSON(t *testing.T, argv []string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(argv, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("pf %v: exit %d (%s)", argv, code, stderr.String())
	}
	return stdout.String()
}

// TestW136VersionJSONCommitField: the machine line carries the build's
// source commit when one is stamped, and never a commit key when none is
// (a release build: the tag already names the commit, and the line keeps
// the exact pre-#136 bytes Pi Web's capability read parses).
func TestW136VersionJSONCommitField(t *testing.T) {
	ver, com := version.Version, version.Commit
	defer func() { version.Version, version.Commit = ver, com }()

	version.Version, version.Commit = "v0.2.4-dev.3", "05819e2cafe"
	for _, argv := range [][]string{{"--json", "version"}, {"--json", "--version"}} {
		var doc struct {
			Version      string   `json:"version"`
			Commit       string   `json:"commit"`
			Capabilities []string `json:"capabilities"`
		}
		raw := w136RunJSON(t, argv)
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("pf %v: %v (%q)", argv, err, raw)
		}
		if doc.Version != "v0.2.4-dev.3" || doc.Commit != "05819e2cafe" {
			t.Fatalf("pf %v: version %q commit %q, want the stamped pair (the machine line must carry the source commit)", argv, doc.Version, doc.Commit)
		}
		if len(doc.Capabilities) == 0 {
			t.Fatalf("pf %v: the commit field must be additive - the capabilities list is gone (%q)", argv, raw)
		}
	}

	// a release build (no stamped commit): no commit key at all — the line
	// stays byte-identical to the shape that shipped before #136
	version.Commit = ""
	for _, argv := range [][]string{{"--json", "version"}, {"--json", "--version"}} {
		if raw := w136RunJSON(t, argv); strings.Contains(raw, "commit") {
			t.Fatalf("pf %v with no stamped commit: %q must carry no commit key (a release build's line stays byte-identical)", argv, raw)
		}
	}
}

// TestW136DoctorBuildRow: the `pf build` row runs on the Host level, out
// of --harness, always pass — it reports the build's version and, on a
// development build, the source commit beside it.
func TestW136DoctorBuildRow(t *testing.T) {
	ver, com := version.Version, version.Commit
	defer func() { version.Version, version.Commit = ver, com }()

	version.Version, version.Commit = "v0.2.4-dev.3", "05819e2cafe"
	c := buildCheck()
	if c.Name != "pf build" || c.Status != "pass" {
		t.Fatalf("the pf build row = %+v, want name %q pass", c, "pf build")
	}
	if !strings.Contains(c.Detail, "v0.2.4-dev.3") || !strings.Contains(c.Detail, "05819e2cafe") {
		t.Fatalf("the pf build row's detail %q must name the version and the source commit of a development build", c.Detail)
	}
	version.Commit = ""
	if c := buildCheck(); strings.Contains(c.Detail, "commit") || !strings.Contains(c.Detail, version.Version) {
		t.Fatalf("a release build's pf build row %q names the version alone (the tag is the commit)", c.Detail)
	}

	// the row sits in the ladder, Host-level, needing nothing
	found := false
	for _, s := range doctorChecks {
		if s.name == docChBuild {
			found = true
			if s.scope != scopeHost {
				t.Fatalf("the pf build row's scope = %v, want Host (one row per machine)", s.scope)
			}
		}
	}
	if !found {
		t.Fatal("the pf build row is not in the doctor ladder: `pf doctor` must show which build the machine runs")
	}
}

// TestW136VersionSourceDocs: the documents that describe the version and
// doctor surfaces name the new field and row in the same commit as the
// code (the repo's coupled-obligation rule).
func TestW136VersionSourceDocs(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	// the version verb's page: the machine line's commit field, the --dev
	// channel it serves, and the version string kept free of it
	vm := read(filepath.Join("skills", "polyforge", "verbs", "version.md"))
	for _, want := range []string{"commit", "--dev"} {
		if !strings.Contains(vm, want) {
			t.Fatalf("skills/polyforge/verbs/version.md lacks %q: the development channel's source commit rides the machine line, the page must say so", want)
		}
	}
	// the command reference: the same field on the version verb's line
	cm := read(filepath.Join("docs", "reference", "commands.md"))
	if !strings.Contains(cm, "commit") {
		t.Fatal("docs/reference/commands.md does not name the version line's commit field (pf subcommands change → the reference changes, same commit)")
	}
	// the doctor verb's check enumeration: the new row, its version and commit
	dm := read(filepath.Join("skills", "polyforge", "verbs", "doctor.md"))
	if !strings.Contains(dm, "pf build") {
		t.Fatal("skills/polyforge/verbs/doctor.md's check enumeration lacks the pf build row (a new doctor check needs its row, same commit)")
	}
}
