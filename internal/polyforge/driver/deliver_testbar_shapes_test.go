package driver

// deliver_testbar_shapes_test.go: the test-bar comparison against the
// REAL recordings of the three incident dispatches (workbench#60).
//
// The synthetic repro (deliver_testbar_setmatch_repro_test.go) encodes
// the two reported failure modes. It is not enough on its own: two
// plausible-looking fixes each pass it and still refuse a real delivery.
// Rejoining a multi-word argv and re-splitting it breaks workbench#58
// entry 29, where an unquoted `|` inside one argv word is an argument
// (`-run A|B`), not a pipe. Re-scanning only argv words with shell
// syntax breaks workbench#27 entry 18, `test -z "$(gofmt -l .)"`, which
// matches today. These fixtures use the recorded command lines and argv
// words from the three incident capsules' deliver steps:
//
//   - workbench-58 deliver s9
//   - workbench-27 deliver s8
//   - workbench-52 deliver s20
//
// (their `artifacts/steps/deliver/<step>/result.json` command_evidence
// and their `runtime-state/execution.json` verification test_bar). The
// repo names and PR URLs below use the public shapes (`silgrid/…`): the
// comparison only sees the command/argv shapes the fixtures replay, so
// the file stays REWRITE-clean and projects with the driver.

import (
	"encoding/json"
	"strings"
	"testing"
)

// evLine is one recorded command whose argv was a plain word list.
func evLine(cwd, line string, exit int) CommandEvidence {
	return CommandEvidence{Argv: strings.Fields(line), Cwd: cwd, ExitCode: exit, DurationMs: 5}
}

// TestTestBarRerunWorkbench58Shapes: the s9 bar and its recorded argv.
// The dispatch re-ran all ten commands green after the merge and before
// `gh pr merge`, yet was refused: entry 27 holds the bar's
// `$(GOWORK=off go list ./... | grep -v '/test$')` verbatim in one argv
// word, and entries 29/30 hold an unquoted `|` inside `-run`, which is
// one argv argument while the bar quotes it.
func TestTestBarRerunWorkbench58Shapes(t *testing.T) {
	const repo = "polyforge"
	bar := []testBarCmd{
		{Repo: repo, Command: "GOWORK=off go build ./..."},
		{Repo: repo, Command: "GOWORK=off go vet ./..."},
		{Repo: repo, Command: "gofmt -l ."},
		{Repo: repo, Command: "python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py"},
		{Repo: repo, Command: "GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./..."},
		{Repo: repo, Command: "GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./..."},
		{Repo: repo, Command: "GOWORK=off go test $(GOWORK=off go list ./... | grep -v '/test$') -count=1"},
		{Repo: repo, Command: "sh test/e2e-parallel.sh"},
		{Repo: repo, Command: "GOWORK=off go test ./test/ -run 'TestW58GrillAttentionContract|TestGrillStepsRequireQuestionTable|TestW32OutputFormatSkillContract|TestW241GuideExistsForNewMember' -count=1"},
		{Repo: repo, Command: "GOWORK=off go test ./internal/polyforge/driver/ ./internal/common/workflow/ -run 'TestShippedPrinciplesRenderedOnce|TestShippedBundlesCarryNoKind|TestReadStepDefinitionPrinciples' -count=1"},
	}
	cwd := "repos/" + repo
	evidence := []CommandEvidence{
		evLine(cwd, "git merge --no-edit origin/main", 0),
		evLine(cwd, "GOWORK=off go build ./...", 0),
		evLine(cwd, "GOWORK=off go vet ./...", 0),
		evLine(cwd, "gofmt -l .", 0),
		evLine(cwd, "python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py", 0),
		evLine(cwd, "GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./...", 0),
		evLine(cwd, "GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./...", 0),
		{Argv: []string{"GOWORK=off", "go", "test", "$(GOWORK=off go list ./... | grep -v '/test$')", "-count=1"}, Cwd: cwd, DurationMs: 5},
		evLine(cwd, "sh test/e2e-parallel.sh", 0),
		{Argv: []string{"GOWORK=off", "go", "test", "./test/", "-run", "TestW58GrillAttentionContract|TestGrillStepsRequireQuestionTable|TestW32OutputFormatSkillContract|TestW241GuideExistsForNewMember", "-count=1"}, Cwd: cwd, DurationMs: 5},
		{Argv: []string{"GOWORK=off", "go", "test", "./internal/polyforge/driver/", "./internal/common/workflow/", "-run", "TestShippedPrinciplesRenderedOnce|TestShippedBundlesCarryNoKind|TestReadStepDefinitionPrinciples", "-count=1"}, Cwd: cwd, DurationMs: 5},
		evLine(cwd, "git push origin HEAD:refs/heads/polyforge/workbench-58", 0),
		evLine(cwd, "gh pr merge https://github.com/silgrid/polyforge/pull/705 --merge", 0),
	}
	const prURL = "https://github.com/silgrid/polyforge/pull/705"
	if !testBarRerun(repo, prURL, bar, evidenceEvents(evidence, t.TempDir())) {
		t.Fatal("workbench#58 s9: the recorded re-run of all ten bar commands (multi-word argv holding $(... | ...), literal `|` inside -run) was not recognised")
	}
}

// TestTestBarRerunWorkbench27Shapes: the s8 bar and its recorded argv.
// Here the argv words keep the bar's single quotes (`-run '^TestWB(20|27)'`)
// and hold a substitution the bar double-quotes
// (`test -z "$(gofmt -l .)"` as argv `["test","-z","$(gofmt -l .)"]`).
// Both match today and must keep matching; the quoted -run words are the
// reason an evidence-side-only rescan is not a fix.
func TestTestBarRerunWorkbench27Shapes(t *testing.T) {
	const repo = "polyforge"
	bar := []testBarCmd{
		{Repo: repo, Command: "GOWORK=off go build ./..."},
		{Repo: repo, Command: "GOWORK=off go vet ./..."},
		{Repo: repo, Command: `test -z "$(gofmt -l .)"`},
		{Repo: repo, Command: "python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py"},
		{Repo: repo, Command: "GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./..."},
		{Repo: repo, Command: "GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./..."},
		{Repo: repo, Command: "GOWORK=off go test ./test/ -run '^TestWB27ScopedSelectionIncludesTestOnlyImporters$' -count=1 -v"},
		{Repo: repo, Command: "GOWORK=off go test ./test/ -run '^TestWB(20|27)' -count=1 -v"},
		{Repo: repo, Command: "GOWORK=off go test ./test/ -run '^TestS18SyncProjectionAcceptance$' -count=1 -v"},
		{Repo: repo, Command: "GOWORK=off go test ./cmd/... ./internal/... -count=1"},
		{Repo: repo, Command: "GOWORK=off go test ./test/fakepi/ -count=1"},
		{Repo: repo, Command: "sh test/e2e-parallel.sh"},
	}
	cwd := "repos/" + repo
	evidence := []CommandEvidence{
		evLine(cwd, "git merge --no-edit origin/main", 0),
		evLine(cwd, "GOWORK=off go build ./...", 0),
		evLine(cwd, "GOWORK=off go vet ./...", 0),
		{Argv: []string{"test", "-z", "$(gofmt -l .)"}, Cwd: cwd, DurationMs: 5},
		evLine(cwd, "python3 -I docs/design/polyforge-vnext/tools/check-xrefs.py", 0),
		evLine(cwd, "GOWORK=off GOOS=darwin GOARCH=arm64 go build -trimpath ./...", 0),
		evLine(cwd, "GOWORK=off GOOS=darwin GOARCH=amd64 go build -trimpath ./...", 0),
		{Argv: []string{"GOWORK=off", "go", "test", "./test/", "-run", "'^TestWB27ScopedSelectionIncludesTestOnlyImporters$'", "-count=1", "-v"}, Cwd: cwd, DurationMs: 5},
		{Argv: []string{"GOWORK=off", "go", "test", "./test/", "-run", "'^TestWB(20|27)'", "-count=1", "-v"}, Cwd: cwd, DurationMs: 5},
		{Argv: []string{"GOWORK=off", "go", "test", "./test/", "-run", "'^TestS18SyncProjectionAcceptance$'", "-count=1", "-v"}, Cwd: cwd, DurationMs: 5},
		evLine(cwd, "GOWORK=off go test ./cmd/... ./internal/... -count=1", 1),
		evLine(cwd, "GOWORK=off go test ./internal/polyforge/daemon -run ^TestRestartMissingCapsuleBlocksOnlyThatWI$ -count=1 -v", 0),
		evLine(cwd, "GOWORK=off go test ./cmd/... ./internal/... -count=1", 0),
		evLine(cwd, "GOWORK=off go test ./test/fakepi/ -count=1", 0),
		evLine(cwd, "sh test/e2e-parallel.sh", 0),
		evLine(cwd, "git push origin HEAD:refs/heads/polyforge/workbench-27", 0),
		evLine(cwd, "gh pr merge https://github.com/silgrid/polyforge/pull/681 --merge", 0),
	}
	const prURL = "https://github.com/silgrid/polyforge/pull/681"
	if !testBarRerun(repo, prURL, bar, evidenceEvents(evidence, t.TempDir())) {
		t.Fatal("workbench#27 s8: the recorded re-run of all twelve bar commands (quoted argv words, argv substitution) was not recognised")
	}
	// The first ./cmd/... ./internal/... run exited 1 and the bar command
	// was then re-run green: a red event must not satisfy it.
	redOnly := []CommandEvidence{
		evLine(cwd, "git merge --no-edit origin/main", 0),
		evLine(cwd, "GOWORK=off go test ./cmd/... ./internal/... -count=1", 1),
	}
	if testBarRerun(repo, "", []testBarCmd{{Repo: repo, Command: "GOWORK=off go test ./cmd/... ./internal/... -count=1"}}, evidenceEvents(redOnly, t.TempDir())) {
		t.Fatal("workbench#27 s8: a bar command whose only recorded run exited 1 was counted as green")
	}
}

// TestTestBarRerunWorkbench52Order: the s20 bar and its recorded order.
// Every command re-ran green inside the window, but the evidence records
// the polyforge-widget e2e (bar entry 18) before polyforge-config-panel
// (bar entry 17): the execution order, not the bar's list.
func TestTestBarRerunWorkbench52Order(t *testing.T) {
	const repo = "pi-web-gmi"
	bar := []testBarCmd{
		{Repo: repo, Command: "npm run lint"},
		{Repo: repo, Command: "node_modules/.bin/tsc --noEmit"},
		{Repo: repo, Command: "npm test"},
		{Repo: repo, Command: "node e2e/sync-projection.mjs"},
		{Repo: repo, Command: "node e2e/sync-monitor.mjs"},
		{Repo: repo, Command: "node e2e/sync-absorb.mjs"},
		{Repo: repo, Command: "node e2e/sync-remote-safety.mjs"},
		{Repo: repo, Command: "node e2e/sync-docs.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/deploy-install-channel.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/deploy-lane-scripts.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/deploy-tunnel-dead-connector.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/deploy-tunnel-variants.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/deploy-tunnel.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/release-flow-contract.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/release-lane-scripts.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/verify-scope-acceptance.mjs"},
		{Repo: repo, Command: "timeout 600 node e2e/polyforge-config-panel.mjs"},
		{Repo: repo, Command: "timeout 1200 node e2e/polyforge-widget.mjs"},
		{Repo: repo, Command: "timeout 1200 node e2e/polyforge-graph.mjs"},
		{Repo: repo, Command: "timeout 1800 node e2e/polyforge-settings.mjs"},
		{Repo: repo, Command: "timeout 2400 node e2e/polyforge.mjs"},
	}
	cwd := "repos/" + repo
	var evidence []CommandEvidence
	evidence = append(evidence, evLine(cwd, "git fetch origin", 0))
	for i, tb := range bar {
		if i == 16 { // recorded in execution order: widget ran first
			evidence = append(evidence, evLine(cwd, bar[17].Command, 0), evLine(cwd, bar[16].Command, 0))
			continue
		}
		if i == 17 {
			continue
		}
		evidence = append(evidence, evLine(cwd, tb.Command, 0))
	}
	evidence = append(evidence, evLine(cwd, "git push origin HEAD:refs/heads/polyforge/workbench-52", 0))
	evidence = append(evidence, evLine(cwd, "gh pr merge https://github.com/silgrid/pi-web-gmi/pull/7 --merge", 0))
	const prURL = "https://github.com/silgrid/pi-web-gmi/pull/7"
	if !testBarRerun(repo, prURL, bar, evidenceEvents(evidence, t.TempDir())) {
		t.Fatal("workbench#52 s20: a complete green re-run recorded in execution order (bar entry 18 before 17) was not recognised")
	}
}

// TestTestBarRerunSubstitutionExactness pins what the substitution-aware
// key must not relax: the substitution's inner command is part of the
// command, and only a substitution is canonicalised (a plain argv word is
// never re-split or re-joined).
func TestTestBarRerunSubstitutionExactness(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name string
		bar  string
		argv []string
		want bool
	}{
		{"a different substitution is another command", "go test $(go list ./a)", []string{"go", "test", "$(go list ./b)"}, false},
		{"a dropped substitution is another command", "go test $(go list ./...)", []string{"go", "test"}, false},
		{"the same substitution, different inner quoting", `test -z "$(gofmt -l .)"`, []string{"test", "-z", `$(gofmt -l ".")`}, true},
		{"a backtick substitution is the same word", "test -z `gofmt -l .`", []string{"test", "-z", "$(gofmt -l .)"}, true},
		{"a literal-pipe argv word stays one word", "go test -run 'A|B'", []string{"go", "test", "-run", "A|B"}, true},
		{"a different literal-pipe word is refused", "go test -run 'A|B'", []string{"go", "test", "-run", "A|C"}, false},
		{"an argv word is never split at a pipe", "go test -run A", []string{"go", "test", "-run", "A|B"}, false},
	}
	for _, c := range cases {
		ev := []CommandEvidence{{Argv: []string{"git", "merge", "--no-edit", "origin/main"}, Cwd: "repos/demo", DurationMs: 5}}
		ev = append(ev, CommandEvidence{Argv: c.argv, Cwd: "repos/demo", DurationMs: 5})
		got := testBarRerun("demo", "", []testBarCmd{{Repo: "demo", Command: c.bar}}, evidenceEvents(ev, root))
		if got != c.want {
			t.Errorf("%s: bar %q, argv %q: testBarRerun = %v, want %v", c.name, c.bar, c.argv, got, c.want)
		}
	}
}

// TestTestBarRerunNestedMergeWindow: a `git merge` inside a command
// substitution still starts the window (it ran), so a bar command
// recorded before it does not count.
func TestTestBarRerunNestedMergeWindow(t *testing.T) {
	root := t.TempDir()
	bar := []testBarCmd{{Repo: "demo", Command: "make test"}}
	before := []CommandEvidence{
		{Argv: []string{"make", "test"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"x=$(git merge origin/main)"}, Cwd: "repos/demo", DurationMs: 5},
	}
	if testBarRerun("demo", "", bar, evidenceEvents(before, root)) {
		t.Fatal("a bar command recorded before a merge hidden in a substitution counted")
	}
	after := []CommandEvidence{
		{Argv: []string{"x=$(git merge origin/main)"}, Cwd: "repos/demo", DurationMs: 5},
		{Argv: []string{"make", "test"}, Cwd: "repos/demo", DurationMs: 5},
	}
	if !testBarRerun("demo", "", bar, evidenceEvents(after, root)) {
		t.Fatal("a bar command recorded after a merge hidden in a substitution was not recognised")
	}
}

// TestLightRecheckEvidenceSubstitutionShape: the light recheck shares the
// comparison key, so a bound command holding a substitution recorded as a
// multi-word argv is recognised there too (workbench#60's format fix
// reaches both paths through barKey).
func TestLightRecheckEvidenceSubstitutionShape(t *testing.T) {
	root := t.TempDir()
	bound := []mechBound{{Check: "lint", Repo: "app", Command: "test -z $(gofmt -l .)"}}
	raw, err := json.Marshal([]CommandEvidence{{Argv: []string{"test", "-z", "$(gofmt -l .)"}, Cwd: "repos/app", ExitCode: 0, DurationMs: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := lightRecheckEvidence(bound, raw, root); reason != "" {
		t.Fatalf("the substitution-shaped evidence must cover the bound command: %s", reason)
	}
	raw, err = json.Marshal([]CommandEvidence{{Argv: []string{"test", "-z", "$(gofmt -l ./other)"}, Cwd: "repos/app", ExitCode: 0, DurationMs: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := lightRecheckEvidence(bound, raw, root); reason == "" {
		t.Fatal("a different substitution covered the bound command")
	}
}

// TestTestBarRerunNestedWindowFacts pins the window facts a command
// substitution hides when the dispatch ran from the capsule root (cwd
// ""): the substitution's own `git -C`/`cd` repo, its exit status and its
// words (the PR URL) must count, not the outer assignment's. A bar run
// after a hidden `gh pr merge` must not count, and one before a hidden
// merge must not either.
func TestTestBarRerunNestedWindowFacts(t *testing.T) {
	root := t.TempDir()
	const prURL = "https://github.com/silgrid/demo/pull/1"
	bar := []testBarCmd{{Repo: "demo", Command: "make test"}}
	at := func(cwd string, argv ...string) CommandEvidence {
		return CommandEvidence{Argv: argv, Cwd: cwd, ExitCode: 0, DurationMs: 5}
	}
	cases := []struct {
		name string
		ev   []CommandEvidence
		want bool
	}{
		{
			"a hidden PR merge from the capsule root ends the window",
			[]CommandEvidence{
				at("", "git", "-C", "repos/demo", "merge", "--no-edit", "origin/main"),
				at("repos/demo", "make", "test"),
				at("", "x=$(gh pr merge "+prURL+" --merge)"),
			},
			true,
		},
		{
			"a bar run after a hidden PR merge does not count",
			[]CommandEvidence{
				at("", "git", "-C", "repos/demo", "merge", "--no-edit", "origin/main"),
				at("", "x=$(gh pr merge "+prURL+" --merge)"),
				at("repos/demo", "make", "test"),
			},
			false,
		},
		{
			"a hidden `git -C` merge from the capsule root starts the window for its repo",
			[]CommandEvidence{
				at("repos/demo", "make", "test"),
				at("", "x=$(git -C repos/demo merge origin/main)"),
			},
			false,
		},
		{
			"a bar run after a hidden `git -C` merge counts",
			[]CommandEvidence{
				at("", "x=$(git -C repos/demo merge origin/main)"),
				at("repos/demo", "make", "test"),
			},
			true,
		},
		{
			"a hidden `cd` merge from the capsule root starts the window for its repo",
			[]CommandEvidence{
				at("repos/demo", "make", "test"),
				at("", "x=$(cd repos/demo && git merge origin/main)"),
			},
			false,
		},
		{
			"a bar run after a hidden `cd` merge counts",
			[]CommandEvidence{
				at("", "x=$(cd repos/demo && git merge origin/main)"),
				at("repos/demo", "make", "test"),
			},
			true,
		},
		{
			"a merge hidden in a backtick substitution counts too",
			[]CommandEvidence{
				at("repos/demo", "make", "test"),
				at("", "x=`git -C repos/demo merge origin/main`"),
			},
			false,
		},
		{
			"a substitution inside single quotes is literal, not a merge",
			[]CommandEvidence{
				at("repos/demo", "make", "test"),
				at("", "echo '$(git -C repos/demo merge origin/main)'"),
			},
			true,
		},
		{
			"a double-quoted substitution still runs, so it starts the window",
			[]CommandEvidence{
				at("repos/demo", "make", "test"),
				at("", `echo "$(git -C repos/demo merge origin/main)"`),
			},
			false,
		},
		{
			// A multi-word argv can carry several words with substitutions;
			// a plain $VAR word before the substitution must not hide it.
			"a hidden PR merge in a later argv word after a $VAR word ends the window",
			[]CommandEvidence{
				at("", "git", "-C", "repos/demo", "merge", "--no-edit", "origin/main"),
				at("repos/demo", "make", "test"),
				at("", "echo", "$HOME", "$(gh pr merge "+prURL+" --merge)"),
			},
			true,
		},
		{
			"a bar run after a hidden PR merge in a later argv word does not count",
			[]CommandEvidence{
				at("", "git", "-C", "repos/demo", "merge", "--no-edit", "origin/main"),
				at("", "echo", "$HOME", "$(gh pr merge "+prURL+" --merge)"),
				at("repos/demo", "make", "test"),
			},
			false,
		},
		{
			"a hidden merge in a later argv word after a $VAR word starts the window for its repo",
			[]CommandEvidence{
				at("repos/demo", "make", "test"),
				at("", "echo", "$(true)", "$(git -C repos/demo merge origin/main)"),
			},
			false,
		},
		{
			"a bar run after a hidden merge in a later argv word counts",
			[]CommandEvidence{
				at("", "echo", "$(true)", "$(git -C repos/demo merge origin/main)"),
				at("repos/demo", "make", "test"),
			},
			true,
		},
	}
	for _, c := range cases {
		if got := testBarRerun("demo", prURL, bar, evidenceEvents(c.ev, root)); got != c.want {
			t.Errorf("%s: testBarRerun = %v, want %v", c.name, got, c.want)
		}
	}
}
