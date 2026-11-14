package test

// w341_release_pr_association_repro_test.go: a bug repro.
//
// Reported failure (#340): `.polyforge/release.sh` proves that every first-parent
// commit entering main since the previous release came through a merged PR by
// asking GitHub `commits/<sha>/pulls`. For some merge commits that endpoint
// returns `[]` even though the commit is the PR's `merge_commit_sha` (observed
// for b47fc6c9, the merge commit of PR #797), so the gate deterministically
// refuses a legitimate release: "no merged PR has it as its merge commit".
//
// The fixture below models that unreliability explicitly: the commit->pulls
// endpoint answers `[]` for a PR-associated commit flagged flaky, while the
// reliable PR reads (the merged-PR list, a single pull, `gh pr list`) carry the
// same truth. The test asserts the user-visible contract, not which endpoint the
// script picks. Two shapes are covered, as the work item requires: a PR merged
// with a merge commit and a PR merged as a one-parent squash commit.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW341ReleasePROnlyReliableAssociation$' -count=1

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w341PR is one row of the fake GitHub PR truth. flaky models the observed
// endpoint gap: commits/<sha>/pulls returns `[]` for that merge_commit_sha.
type w341PR struct {
	number      int
	mergeCommit string
	flaky       bool
}

// w341FakeGitHub replaces the release-script harness's gh stub with one that
// keeps PR truth independent of the flaky commit->pulls endpoint. The old
// endpoint answers `[]` for a flaky commit; the reliable REST reads (a pull
// list, a single pull, `gh pr list`) answer from the same truth, so a script
// that stops relying on the flaky endpoint can still find the association.
func w341FakeGitHub(t *testing.T, w *relWorld, prs []w341PR) {
	t.Helper()

	original, err := os.ReadFile(filepath.Join(w.bin, "gh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.fakeRel, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.fakeRel, "gh.base"), original, 0o755); err != nil {
		t.Fatal(err)
	}

	var rest, list, flaky strings.Builder
	rest.WriteString("[")
	list.WriteString("[")
	for i, pr := range prs {
		if i > 0 {
			rest.WriteString(",")
			list.WriteString(",")
		}
		fmt.Fprintf(&rest,
			`{"number":%d,"state":"closed","merged_at":"2026-11-04T10:57:42Z","merge_commit_sha":%q,"title":"PR %d"}`,
			pr.number, pr.mergeCommit, pr.number)
		fmt.Fprintf(&list,
			`{"number":%d,"title":"PR %d","state":"MERGED","mergedAt":"2026-11-04T10:57:42Z","mergeCommit":{"oid":%q}}`,
			pr.number, pr.number, pr.mergeCommit)
		if pr.flaky {
			fmt.Fprintf(&flaky, "%s\n", pr.mergeCommit)
		}
	}
	rest.WriteString("]")
	list.WriteString("]")
	relWrite(t, filepath.Join(w.fakeRel, "w341-prs.json"), rest.String()+"\n", 0o644)
	relWrite(t, filepath.Join(w.fakeRel, "w341-pr-list.json"), list.String()+"\n", 0o644)
	relWrite(t, filepath.Join(w.fakeRel, "w341-flaky"), flaky.String(), 0o644)

	relWrite(t, filepath.Join(w.bin, "gh"), `#!/bin/sh
# w341 fake gh: GitHub's PR truth, with the flaky commits/<sha>/pulls gap.
jqexpr=
prev=
for a in "$@"; do
  if [ "$prev" = "--jq" ] || [ "$prev" = "-q" ]; then jqexpr=$a; fi
  case "$a" in
    --jq=*) jqexpr=${a#--jq=} ;;
    -q=*) jqexpr=${a#-q=} ;;
  esac
  prev=$a
done
w341_filter() {
  if [ -n "$jqexpr" ]; then jq -r "$jqexpr"; else cat; fi
}
if [ "$1" = "api" ]; then
  url=$2
  case "$url" in
    */commits/*/pulls)
      sha=${url##*/commits/}
      sha=${sha%%/*}
      if grep -qx "$sha" "$FAKE_GH/w341-flaky" 2>/dev/null; then
        body='[]'
      elif grep -qx "$sha" "$FAKE_GH/pr-merges" 2>/dev/null; then
        body=$(printf '[{"number":7,"state":"closed","merged_at":"2026-11-04T10:57:42Z","merge_commit_sha":"%s"}]' "$sha")
      else
        body='[]'
      fi
      printf '%s\n' "$body" | w341_filter
      exit 0 ;;
    */pulls*)
      cat "$FAKE_GH/w341-prs.json" | w341_filter
      exit 0 ;;
  esac
elif [ "$1" = "pr" ] && [ "$2" = "list" ]; then
  cat "$FAKE_GH/w341-pr-list.json" | w341_filter
  exit 0
fi
exec "$FAKE_GH/gh.base" "$@"
`, 0o755)
}

// TestW341ReleasePROnlyReliableAssociation: a commit that is the
// merge_commit_sha of a merged PR must pass the PR-only gate even when the
// commits/<sha>/pulls endpoint returns an empty list for it. Both the merge
// commit and the squash commit shapes are pinned.
func TestW341ReleasePROnlyReliableAssociation(t *testing.T) {
	const (
		floor    = "v1.2.0"
		released = "v1.2.1"
	)

	cases := []struct {
		name  string
		setup func(w *relWorld) string // returns the flaky PR-associated commit
	}{
		{
			name: "merge commit missing from commits/<sha>/pulls",
			setup: func(w *relWorld) string {
				return w.mergeOnMain("w341-merge", "merge\n", "Merge pull request #797 from w341", false)
			},
		},
		{
			name: "squash commit missing from commits/<sha>/pulls",
			setup: func(w *relWorld) string {
				return w.commitOnMain("w341-squash", "squashed PR #798")
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, []string{floor}, nil)
			// newRelWorld lands its second change through a recorded merged PR;
			// carry that association into the fake GitHub truth too.
			known := relGit(t, w.work, "rev-parse", "HEAD")
			flaky := c.setup(w)
			w341FakeGitHub(t, w, []w341PR{
				{number: 7, mergeCommit: known},
				{number: 797, mergeCommit: flaky, flaky: true},
			})

			out, err := w.run(nil)
			if err != nil {
				t.Fatalf("release must pass when %s is the merge_commit_sha of a merged PR; the commits/<sha>/pulls endpoint is unreliable for it: %v\n%s",
					flaky, err, out)
			}
			if !strings.Contains(out, "version="+released) {
				t.Fatalf("release did not publish %s:\n%s", released, out)
			}
			if strings.Contains(out, "without a merged PR") {
				t.Fatalf("the gate still reports no merged PR for %s:\n%s", flaky, out)
			}
			if got := w.originTags(); !strings.Contains(got, released) {
				t.Fatalf("origin tags = %q: want %s", got, released)
			}
		})
	}
}
