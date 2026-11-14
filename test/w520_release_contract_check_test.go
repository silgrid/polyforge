package test

// w520_release_contract_check_test.go: the release's
// workflow-contract check. ContractVersion moves only on an incompatible
// change, at most once per release; an incompatible change is registered in
// internal/common/workflow/contract-pending.md when it merges. The release
// (.polyforge/release.sh, before anything is built, tagged or published)
// compares the version at HEAD with the previous release's and counts the
// list's entries: a registered change with the version not bumped refuses
// with the way out; an empty list needs no bump; a bump releases only with
// the list emptied, and only by one.

import (
	"path/filepath"
	"strings"
	"testing"
)

// w520Land lands one PR that writes every file in files (path -> content)
// the way `gh pr merge --merge` does, and records it as a merged PR.
func w520Land(w *relWorld, topic, msg string, files map[string]string) {
	w.t.Helper()
	relGit(w.t, w.work, "checkout", "-q", "-b", topic)
	for p, body := range files {
		relWrite(w.t, filepath.Join(w.work, filepath.FromSlash(p)), body, 0o644)
	}
	relGit(w.t, w.work, "add", "-A")
	relGit(w.t, w.work, "commit", "-q", "-m", msg)
	relGit(w.t, w.work, "checkout", "-q", "main")
	relGit(w.t, w.work, "merge", "-q", "--no-ff", "-m", "Merge pull request from "+topic, topic)
	relGit(w.t, w.work, "branch", "-q", "-D", topic)
	relGit(w.t, w.work, "push", "-q", "origin", "main")
	w.recordPR(relGit(w.t, w.work, "rev-parse", "HEAD"))
}

// w520Pending is the pending list with one registered incompatible change.
const w520Pending = relContractPendingEmpty + "\n## lane key `retired` removed\n\n" +
	"User lanes drop the key; work items in flight that use it are drained.\n"

func w520Register(w *relWorld) {
	w520Land(w, "topic-register", "register an incompatible contract change",
		map[string]string{relContractPending: w520Pending})
}

// w520Refused asserts a refusal before any work: nothing ran, no tag cut.
func w520Refused(t *testing.T, w *relWorld, out string, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected the contract check to refuse the release\n%s", out)
	}
	for _, want := range append(wants, "contract-pending.md", "nothing built or published") {
		if !strings.Contains(out, want) {
			t.Errorf("refusal lacks %q\n%s", want, out)
		}
	}
	if calls := w.calls(); len(calls) != 0 {
		t.Fatalf("work happened despite the refusal: %v", calls)
	}
	if got := w.originTags(); strings.Contains(got, "v1.2.1") {
		t.Fatalf("tag cut despite the refusal: %q", got)
	}
}

// w520Released asserts a published release whose contract line is want.
func w520Released(t *testing.T, w *relWorld, out string, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("release output lacks the contract line %q\n%s", want, out)
	}
	if !strings.Contains(out, "version=v1.2.1") {
		t.Errorf("release did not publish v1.2.1\n%s", out)
	}
	if got := w.originTags(); !strings.Contains(got, "v1.2.1") {
		t.Fatalf("origin tags = %q: want v1.2.1", got)
	}
}

func TestW520ReleaseContractCheck(t *testing.T) {
	t.Run("registered change without a bump refuses with the way out", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w520Register(w)
		out, err := w.run(nil)
		w520Refused(t, w, out, err,
			"1 incompatible", "ContractVersion", "still 1", "to 2", "docs/upgrade.md", "empt")
	})

	t.Run("empty list needs no bump and releases", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w.landPR("unrelated", "an unrelated change")
		out, err := w.run(nil)
		w520Released(t, w, out, err, "release: contract: 1 (previous release 1), no incompatible change registered")
	})

	t.Run("bump with the list emptied releases", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w520Register(w)
		w520Land(w, "topic-bump", "bump the contract version, empty the list", map[string]string{
			relContractGo:      relContractGoAt(2),
			relContractPending: relContractPendingEmpty,
		})
		out, err := w.run(nil)
		w520Released(t, w, out, err, "release: contract: 2 (previous release 1), no incompatible change registered")
	})

	t.Run("bump with the list not emptied refuses", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w520Register(w)
		w520Land(w, "topic-bump", "bump the contract version only", map[string]string{
			relContractGo: relContractGoAt(2),
		})
		out, err := w.run(nil)
		w520Refused(t, w, out, err, "ContractVersion", "2", "still holds 1", "empty")
	})

	t.Run("a bump by two refuses", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		w520Register(w)
		w520Land(w, "topic-bump", "bump the contract version twice", map[string]string{
			relContractGo:      relContractGoAt(3),
			relContractPending: relContractPendingEmpty,
		})
		out, err := w.run(nil)
		w520Refused(t, w, out, err, "ContractVersion", "3", "at most once")
	})
}
