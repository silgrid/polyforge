package test

// w136_dev_channel_docs_test.go (polyforge#136): the documents that
// describe the local development channel change in the same commit as
// the channel (the repo's coupled-obligation rule). AC5's judged tokens:
//
//   - docs/install.md (the ops page): the --dev entry, its build
//     prerequisites, the boundaries (this machine only; the two AIHubs
//     do not follow dev builds; main only fast-forwards — the N
//     precondition) and what happens after a dev install — the formal
//     channel is refused until the next formal release, with the way
//     back;
//   - docs/reference/release.md (the release page): the channel beside
//     the formal one — the green-tag source, the local-only boundary,
//     and the green tag's residual risk carrying over unchanged;
//   - docs/upgrade.md: one appended section, its heading the shared
//     next-patch version (v0.2.4), moved together with the w254 sha256
//     pin (pinned by TestW254UpgradeByteIdentical, not re-pinned here);
//   - polyforge#188 grades the channel's AIHub-side preflight: all
//     three pages name the two lists and the two grades — the interface
//     hits that refuse, the remaining AIHub-side hits that pass with
//     one WARN — and upgrade.md carries its own appended section.
//
// carries: docs/install.md, docs/reference/release.md, docs/upgrade.md, test/w136_dev_channel_docs_test.go

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestW136DevChannelDocs(t *testing.T) {
	root := w136Root(t)
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}

	// docs/install.md: the entry, the prerequisites, the boundaries, the
	// formal-channel refusal and the way back
	im := read(filepath.Join("docs", "install.md"))
	// distinctive sentences, not bare tool names: the prerequisites carry
	// their own wording (the node pin), the boundaries their precondition,
	// and the way back its mechanism (review r1 N10)
	for _, want := range []string{
		"--dev", "green tag", "AIHub", "fast-forward",
		"formal channel",
		"`node` of the major the\ncheckout's `.nvmrc` pins",
		"rebuild the formal release from its tag",
	} {
		if !strings.Contains(im, want) {
			t.Fatalf("docs/install.md lacks %q: the ops page must document the development channel's entry, its build prerequisites, its boundaries and the way back to the formal channel", want)
		}
	}
	// the graded preflight (polyforge#188): the ops page names both lists
	// and the two grades — the interface hits that refuse, the remaining
	// AIHub-side hits that pass with one WARN, the fail-closed rule
	for _, want := range []string{
		"`tools/aihub-interface-paths.conf`", "`tools/aihub-paths.conf`",
		"one WARN", "fail closed",
	} {
		if !strings.Contains(im, want) {
			t.Fatalf("docs/install.md lacks %q: the ops page must document the development channel's graded AIHub-side preflight (polyforge#188)", want)
		}
	}

	// docs/reference/release.md: the channel beside the formal release
	rm := read(filepath.Join("docs", "reference", "release.md"))
	for _, want := range []string{"--dev", "green tag", "AIHub", "fast-forward"} {
		if !strings.Contains(rm, want) {
			t.Fatalf("docs/reference/release.md lacks %q: the release page must name the development channel, its green-tag source and its boundaries", want)
		}
	}
	// the graded preflight (polyforge#188): the release page names the
	// interface list and the two grades too
	for _, want := range []string{"`tools/aihub-interface-paths.conf`", "`tools/aihub-paths.conf`", "one WARN"} {
		if !strings.Contains(rm, want) {
			t.Fatalf("docs/reference/release.md lacks %q: the release page must document the development channel's graded AIHub-side preflight (polyforge#188)", want)
		}
	}

	// the scanned doc pages this change touched carry no `pf build` code
	// span: the doctor row's name is not a pf verb, and the doc set's
	// drift checker (TestDocsSetNoDrift) rejects a code span naming a
	// verb that does not exist — verification r1's red. docs/upgrade.md
	// is not in the drift checker's page set (w254 pins its bytes), and
	// the row's own enumeration lives in skills/polyforge/verbs/doctor.md.
	for _, page := range []string{"docs/install.md", "docs/reference/release.md"} {
		if strings.Contains(read(page), "`pf build`") {
			t.Fatalf("%s carries a `pf build` code span: the doctor row's name is not a pf verb and the doc-set drift checker rejects the span - name the row in prose or point at doctor.md", page)
		}
	}

	// docs/upgrade.md: one appended section, heading the shared
	// next-patch version, naming the channel. A later ticket may append
	// after this one (polyforge#153 did): the channel's section is found
	// by its heading's tokens, not by position.
	um := read(filepath.Join("docs", "upgrade.md"))
	heads := regexp.MustCompile(`(?m)^## .+$`).FindAllString(um, -1)
	if len(heads) == 0 {
		t.Fatal("docs/upgrade.md carries no section heading")
	}
	last := heads[len(heads)-1]
	if !strings.HasPrefix(last, "## v0.2.4, ") {
		t.Fatalf("the appended section's heading %q must take the next patch of the latest tag (v0.2.4), like every section appended after one tag", last)
	}
	mine := -1
	for i, h := range heads {
		if strings.Contains(h, "development channel") && strings.Contains(h, "polyforge#136") {
			mine = i
			break
		}
	}
	if mine < 0 {
		t.Fatal("docs/upgrade.md carries no section whose heading names the development channel and its ticket")
	}
	start := strings.Index(um, heads[mine])
	body := um[start:]
	if end := strings.Index(body[len(heads[mine]):], "\n## "); end >= 0 {
		body = body[:len(heads[mine])+end]
	}
	for _, want := range []string{"--dev", "green tag", "AIHub", "v0.2.4-dev."} {
		if !strings.Contains(body, want) {
			t.Fatalf("the development channel's upgrade section lacks %q", want)
		}
	}

	// polyforge#188: the graded preflight's own appended section, found
	// by its heading's tokens like the #136 one above (a later ticket may
	// append after it), its body naming the two lists and the two grades
	mine188 := -1
	for i, h := range heads {
		if strings.Contains(h, "graded AIHub-side preflight") && strings.Contains(h, "polyforge#188") {
			mine188 = i
			break
		}
	}
	if mine188 < 0 {
		t.Fatal("docs/upgrade.md carries no section whose heading names the graded AIHub-side preflight and polyforge#188")
	}
	start188 := strings.Index(um, heads[mine188])
	body188 := um[start188:]
	if end := strings.Index(body188[len(heads[mine188]):], "\n## "); end >= 0 {
		body188 = body188[:len(heads[mine188])+end]
	}
	for _, want := range []string{"--dev", "tools/aihub-interface-paths.conf", "tools/aihub-paths.conf", "WARN", "formal release", "fail closed"} {
		if !strings.Contains(body188, want) {
			t.Fatalf("the graded preflight's upgrade section lacks %q", want)
		}
	}
}
