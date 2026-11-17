package test

// carries: docs/upgrade.md, test/w254_docs_set_acceptance_test.go, test/pf123_upgrade_section_version_test.go, internal/contracttest/pf101_forensics_skill_contract_test.go, internal/contracttest/pf118_upgrade_section_contract_test.go

// pf123_upgrade_section_version_test.go (polyforge#123, AC4): every
// section appended to docs/upgrade.md after the latest tag shares one
// heading version, and that version is the latest tag's next patch —
// unless the explicit constant below names another version for the
// whole batch (a ticket that states its release version overrides the
// default, and the batch stays uniform). The latest tag and the file
// bytes it carries are read from git at run time, so the guard never
// goes stale after a release: once v0.2.3 ships, the sections it covers
// leave the check and the next append must say v0.2.4 (the #121, #122
// and #118 sections were mislabeled v0.2.4 exactly because a build
// copied the file's newest heading instead of deriving the next tag's
// patch). A clone without tags, a tag without the file, a rewritten
// released byte (the tagged bytes stop being the file's prefix) and an
// appended `## ` heading without a vX.Y.Z version all fail loudly —
// the guard never silently skips.

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// pf123WantNextVersion overrides the expected heading version for the
// WHOLE batch of sections appended after the latest tag (polyforge#123,
// settled Q1/Q3): empty means the default — the latest tag's next
// patch. A ticket that names its release version sets it here, and the
// batch must still be uniform.
const pf123WantNextVersion = ""

// pf123TagRe matches one release tag, "vX.Y.Z", nothing else.
var pf123TagRe = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// pf123HeadingRe matches one versioned section heading,
// "## vX.Y.Z, ..."; the capture groups carry the three numbers.
var pf123HeadingRe = regexp.MustCompile(`^## v([0-9]+)\.([0-9]+)\.([0-9]+)\b`)

// pf123Git runs one git command in the repository root and answers its
// stdout; a non-zero exit fails the test with git's own stderr.
func pf123Git(t *testing.T, root string, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String()
}

// pf123Less answers a < b on the two three-part versions.
func pf123Less(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// TestPF123UpgradeSectionsAfterTheLatestTagShareTheNextPatchVersion: the
// sections appended after the latest tag all carry one same heading
// version, the tag's next patch (or the explicit override). Authored
// BEFORE the fix: on the tree this change starts from, four appended
// sections (the #121, #122 and both #118 ones) say v0.2.4 while the
// latest tag v0.2.2 expects v0.2.3, and the version-mismatch assertions
// below are the red — the tag reads and the prefix check pass there.
func TestPF123UpgradeSectionsAfterTheLatestTagShareTheNextPatchVersion(t *testing.T) {
	root := e2e344Root(t)

	// The latest release tag: the highest vX.Y.Z the repository carries.
	var latest string
	var latestV [3]int
	for _, line := range strings.Split(pf123Git(t, root, "tag"), "\n") {
		line = strings.TrimSpace(line)
		m := pf123TagRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		var v [3]int
		for i := 0; i < 3; i++ {
			fmt.Sscanf(m[i+1], "%d", &v[i])
		}
		if latest == "" || pf123Less(latestV, v) {
			latest, latestV = line, v
		}
	}
	if latest == "" {
		t.Fatal("the repository carries no vX.Y.Z tag — the released prefix of docs/upgrade.md cannot be known; a clone without tags fails loudly and never silently skips (polyforge#123, settled Q2)")
	}

	// The tagged file bytes are the released prefix: every byte before
	// them a released section owns, and it is never rewritten (an
	// unreleased section may be corrected in place — the new-section
	// version rule carries that exception, AGENTS.md).
	tagged := pf123Git(t, root, "show", latest+":docs/upgrade.md")
	current := e2e344Read(t, root, "docs/upgrade.md")
	if !strings.HasPrefix(current, tagged) {
		t.Fatalf("docs/upgrade.md no longer starts with the bytes %s carries — a released section was rewritten (append-only: an unreleased section may be corrected in place, a released byte never)", latest)
	}
	if !strings.HasSuffix(tagged, "\n") {
		t.Fatalf("the docs/upgrade.md bytes of %s end mid-line — the appended sections cannot be told apart from the released prefix", latest)
	}

	// The one expected version: the explicit override for the whole
	// batch, else the latest tag's next patch.
	want := pf123WantNextVersion
	if want == "" {
		want = fmt.Sprintf("v%d.%d.%d", latestV[0], latestV[1], latestV[2]+1)
	}

	// Every `## ` heading past the released prefix: one same version,
	// the expected one.
	prefix := len(tagged)
	off, lineNo, seen := 0, 0, 0
	for _, line := range strings.Split(current, "\n") {
		lineNo++
		at := off
		off += len(line) + 1
		if at < prefix {
			continue
		}
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		seen++
		m := pf123HeadingRe.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("docs/upgrade.md:%d: an appended section heading carries no vX.Y.Z version — a section appended after %s must head with the tag's next patch (%s): %q", lineNo, latest, want, line)
			continue
		}
		if got := "v" + m[1] + "." + m[2] + "." + m[3]; got != want {
			t.Errorf("docs/upgrade.md:%d: an appended section heads with %s, but every section appended after %s must share one version, its next patch (%s): %q", lineNo, got, latest, want, line)
		}
	}
	if seen == 0 {
		t.Logf("no section appended after %s yet — nothing to check", latest)
	}
}
