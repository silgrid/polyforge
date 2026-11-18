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
	for _, want := range []string{
		"--dev", "green tag", "AIHub", "fast-forward",
		"formal channel", "go", "make", "node",
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

	// docs/upgrade.md: one appended section, heading the shared
	// next-patch version, naming the channel
	um := read(filepath.Join("docs", "upgrade.md"))
	heads := regexp.MustCompile(`(?m)^## .+$`).FindAllString(um, -1)
	if len(heads) == 0 {
		t.Fatal("docs/upgrade.md carries no section heading")
	}
	last := heads[len(heads)-1]
	if !strings.HasPrefix(last, "## v0.2.4, ") {
		t.Fatalf("the appended section's heading %q must take the next patch of the latest tag (v0.2.4), like every section appended after one tag", last)
	}
	if !strings.Contains(last, "development channel") || !strings.Contains(last, "polyforge#136") {
		t.Fatalf("the appended section's heading %q must name the development channel and its ticket", last)
	}
	body := um[strings.LastIndex(um, "## v0.2.4, "):]
	for _, want := range []string{"--dev", "green tag", "AIHub", "v0.2.4-dev."} {
		if !strings.Contains(body, want) {
			t.Fatalf("the appended upgrade section lacks %q", want)
		}
	}
}
