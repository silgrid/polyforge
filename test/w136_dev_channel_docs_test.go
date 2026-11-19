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
//     pin (pinned by TestW254UpgradeByteIdentical, not re-pinned here).
//     The file is append-only and later tickets append after this one
//     (polyforge#143's step tier catalog did — verification r1's red), so
//     the channel's section is pinned by its own heading, never by
//     being the file's last.
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

	// docs/reference/release.md: the channel beside the formal release
	rm := read(filepath.Join("docs", "reference", "release.md"))
	for _, want := range []string{"--dev", "green tag", "AIHub", "fast-forward"} {
		if !strings.Contains(rm, want) {
			t.Fatalf("docs/reference/release.md lacks %q: the release page must name the development channel, its green-tag source and its boundaries", want)
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

	// docs/upgrade.md: the channel's appended section, heading the shared
	// next-patch version, naming the channel — found by its heading
	// (later tickets append after it; polyforge#143 did)
	um := read(filepath.Join("docs", "upgrade.md"))
	heads := regexp.MustCompile(`(?m)^## .+$`).FindAllString(um, -1)
	if len(heads) == 0 {
		t.Fatal("docs/upgrade.md carries no section heading")
	}
	var head string
	for _, h := range heads {
		if strings.Contains(h, "polyforge#136") && strings.Contains(h, "development channel") {
			head = h
		}
	}
	if head == "" {
		t.Fatal("docs/upgrade.md carries no development-channel section: no heading names the channel and polyforge#136")
	}
	if !strings.HasPrefix(head, "## v0.2.4, ") {
		t.Fatalf("the channel section's heading %q must take the next patch of the latest tag (v0.2.4), like every section appended after one tag", head)
	}
	start := strings.Index(um, head)
	end := len(um)
	if next := strings.Index(um[start+len(head):], "\n## "); next >= 0 {
		end = start + len(head) + next
	}
	body := um[start:end]
	for _, want := range []string{"--dev", "green tag", "AIHub", "v0.2.4-dev."} {
		if !strings.Contains(body, want) {
			t.Fatalf("the channel's upgrade section lacks %q", want)
		}
	}
}
