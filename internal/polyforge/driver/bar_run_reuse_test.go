package driver

// bar_run_reuse_test.go: the deliver merge rule reads the
// daemon-recorded bar runs (barruns.go) beside the dispatch's own evidence
// under the unchanged window/key/one-use rules: an earlier spawn's run of
// the same deliver run counts when it is at the pushed head with exit 0
// (so a re-dispatch runs only the missing key), this spawn's `pf bar-run`
// counts by its record where it sits in the window, and nothing else is a
// credential. An opted-in repository's full-tier delivery (smoke_suite= at
// the delivered head) has no HEAD-equality shortcut; a non-opted or scoped
// one keeps it. The record survives everything but the rewind/re-entry
// boundary, where the driver clears it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	brFast = "make test"
	brE2E  = "npm run test:e2e:all"
)

// brSpec is a moved delivery (HEAD past the verified head by a clean
// merge, so a re-run is owed) with a two-key carried bar, judged as spawn
// 5 of the deliver run.
func brSpec(t *testing.T, atHead string) (ResultSpec, string, string) {
	t.Helper()
	spec, verified, head := w116Moved(t, "", atHead)
	spec.VerifiedTestBar = []testBarCmd{{Repo: "demo", Command: brFast}, {Repo: "demo", Command: brE2E}}
	spec.SpawnN = 5
	return spec, verified, head
}

// brRun is one recorded bar run of repos/demo.
func brRun(spawn int64, line, head string, exit int) BarRun {
	return BarRun{Repo: "demo", SpawnN: spawn, Argv: []string{line}, Exit: exit, Head: head}
}

// brBarRun is the evidence of one `pf bar-run` of this dispatch.
func brBarRun(line string, exit int) CommandEvidence {
	return w116Sh("cd repos/demo && pf bar-run -- '"+line+"'", exit)
}

// brBlocker judges repos/demo's merged record with the given evidence.
func brBlocker(spec ResultSpec, evidence ...CommandEvidence) string {
	yes := true
	r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166", PRURL: w116PR, MergeCommit: "abc", TestsPassed: &yes}
	return mergeBlocker(spec, r, "polyforge/polyforge-166", evidence)
}

func TestBarRunReuseAcrossSpawns(t *testing.T) {
	spec, verified, head := brSpec(t, "")
	const owed = "the tests are not shown to pass"

	t.Run("an earlier spawn's run at the pushed head counts; only the missing key runs", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(4, brFast, head, 0), brRun(5, brE2E, head, 0)}
		if why := brBlocker(s, w116Push, brBarRun(brE2E, 0), w116PRMerge); why != "" {
			t.Fatalf("the re-dispatch must reuse the dead spawn's recorded run: %s", why)
		}
	})
	t.Run("a no-op merge in the re-dispatch does not void a run at the pushed head", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(4, brFast, head, 0), brRun(5, brE2E, head, 0)}
		if why := brBlocker(s, w116Merge, w116Push, brBarRun(brE2E, 0), w116PRMerge); why != "" {
			t.Fatalf("a run at the pushed head stays reusable: %s", why)
		}
	})
	t.Run("a run recorded at another head is never reused", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(4, brFast, verified, 0), brRun(5, brE2E, head, 0)}
		if why := brBlocker(s, w116Merge, w116Push, brBarRun(brE2E, 0), w116PRMerge); !strings.Contains(why, owed) {
			t.Fatalf("a run before the merge that moved HEAD must not count, got %q", why)
		}
	})
	t.Run("a failed run is no credential", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(4, brFast, head, 2), brRun(5, brE2E, head, 0)}
		if why := brBlocker(s, w116Push, brBarRun(brE2E, 0), w116PRMerge); !strings.Contains(why, owed) {
			t.Fatalf("a failed recorded run must not count, got %q", why)
		}
	})
	t.Run("a bar-run claim the daemon never recorded is no credential", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(5, brE2E, head, 0)}
		if why := brBlocker(s, w116Push, brBarRun(brFast, 0), brBarRun(brE2E, 0), w116PRMerge); !strings.Contains(why, owed) {
			t.Fatalf("an unrecorded pf bar-run must not count, got %q", why)
		}
		if why := brBlocker(spec, w116Push, w116PRMerge); !strings.Contains(why, owed) {
			t.Fatalf("no record and no evidence is no evidence, got %q", why)
		}
	})
	t.Run("this spawn's recorded run after its gh pr merge does not count", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(5, brFast, head, 0), brRun(5, brE2E, head, 0)}
		if why := brBlocker(s, w116Push, brBarRun(brFast, 0), w116PRMerge, brBarRun(brE2E, 0)); !strings.Contains(why, owed) {
			t.Fatalf("a run after the PR merge must not count, got %q", why)
		}
		if why := brBlocker(s, w116Push, brBarRun(brFast, 0), brBarRun(brE2E, 0), w116PRMerge); why != "" {
			t.Fatalf("both runs inside the window: %s", why)
		}
	})
	t.Run("one recorded run serves one bar key, and another work item's file is never read", func(t *testing.T) {
		s := spec
		s.BarRuns = []BarRun{brRun(4, brFast, head, 0)}
		s.VerifiedTestBar = []testBarCmd{{Repo: "demo", Command: brFast}, {Repo: "demo", Command: brFast}}
		if why := brBlocker(s, w116Push, w116PRMerge); !strings.Contains(why, owed) {
			t.Fatalf("one entry must not serve two keys, got %q", why)
		}
	})
	t.Run("the bar-key comparison is unchanged: a multi-word quoted line", func(t *testing.T) {
		s := spec
		line := `go test ./... -run '^(A|B)$'`
		s.VerifiedTestBar = []testBarCmd{{Repo: "demo", Command: line}}
		s.BarRuns = []BarRun{brRun(5, line, head, 0)}
		quoted := w116Sh(`cd repos/demo && pf bar-run -- "go test ./... -run '^(A|B)$'"`, 0)
		if why := brBlocker(s, w116Push, quoted, w116PRMerge); why != "" {
			t.Fatalf("the recorded line must key exactly like the carried test_bar: %s", why)
		}
	})
}

func TestBarRunOptedInFullTierHasNoHeadShortcut(t *testing.T) {
	const optIn = "stack=npm\nsmoke_suite=e2e/smoke.mjs\n"
	repos := deliverCapsule(t)
	dir := filepath.Join(repos, "demo")
	if err := os.MkdirAll(filepath.Join(dir, ".polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	dwrite(t, filepath.Join(dir, verifyScopeConf), optIn)
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "opt in")
	pushFeature(t, dir)
	spec := deliverSpec(repos, true, 0) // verified == reviewed == HEAD
	spec.VerifiedTestBar = []testBarCmd{{Repo: "demo", Command: brFast}, {Repo: "demo", Command: brE2E}}
	spec.SpawnN = 5
	head := spec.VerifiedHeads["demo"]

	if why := brBlocker(spec, w116Push, w116PRMerge); !strings.Contains(why, "opted into the full-tier e2e selection") {
		t.Fatalf("an opted-in full-tier delivery at the verified head must re-run its carried full bar, got %q", why)
	}
	s := spec
	s.BarRuns = []BarRun{brRun(4, brFast, head, 0), brRun(5, brE2E, head, 0)}
	if why := brBlocker(s, w116Push, brBarRun(brE2E, 0), w116PRMerge); why != "" {
		t.Fatalf("the recorded full bar at the verified head covers it: %s", why)
	}

	// a scoped delivery of the same repo keeps the HEAD-equality rule (its
	// carried scoped bar is never upgraded)
	scoped := spec
	scoped.VerificationScoped = true
	yes := true
	scoped.ReviewScopeAdequate = &yes
	scoped.ReviewScopePresent = true
	scoped.ReviewScope = &reviewScope{Mode: "scoped", DeclaredPaths: []string{"a.txt"}}
	scoped.VerificationScope = &scopeObject{Mode: "scoped", DeclaredPaths: []string{"a.txt"}}
	if why := brBlocker(scoped, w116Push, w116PRMerge); why != "" {
		t.Fatalf("a scoped delivery at the verified head keeps its shortcut: %s", why)
	}
}

func TestBarRunNonOptedKeepsHeadShortcut(t *testing.T) {
	repos := deliverCapsule(t)
	spec := deliverSpec(repos, true, 0)
	if why := brBlocker(spec, w116Push, w116PRMerge); why != "" {
		t.Fatalf("a non-opted delivery at the verified head needs no re-run: %s", why)
	}
}

// TestBarRunRecordClearedOnlyAtTheRewindBoundary: a run without a rewind
// keeps the record; the re-entry a gate FAIL rewinds clears it before the
// new round's first dispatch.
func TestBarRunRecordClearedOnlyAtTheRewindBoundary(t *testing.T) {
	data := t.TempDir()
	seed := func(wi string) {
		if err := AppendBarRun(data, wi, brRun(1, brFast, "h", 0)); err != nil {
			t.Fatal(err)
		}
	}
	keep := infraRetryEnv(t)
	keep.d.cfg.DataDir = data
	seed(keep.d.wi.ID)
	seed("wi_neighbour")
	keep.sp.script = []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}
	if err := keep.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, _ := ReadBarRuns(data, keep.d.wi.ID); len(got) != 1 {
		t.Fatalf("a run with no rewind must keep the record, got %+v", got)
	}

	cleared := infraRetryEnv(t)
	cleared.d.cfg.DataDir = data
	cleared.sp.script = []fakeEntry{done(`{}`), gateFAIL(), done(`{}`), done(`{"verdict":"PASS"}`)}
	if err := cleared.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, _ := ReadBarRuns(data, cleared.d.wi.ID); len(got) != 0 {
		t.Fatalf("the rewind re-entry must clear the record, got %+v", got)
	}
	if got, _ := ReadBarRuns(data, "wi_neighbour"); len(got) != 1 {
		t.Fatalf("another work item's record must survive, got %+v", got)
	}
}
