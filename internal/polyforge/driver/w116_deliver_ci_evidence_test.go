package driver

// w116_deliver_ci_evidence_test.go: a repo whose
// .polyforge/verify-scope.conf on the fetched origin default branch
// declares ci_evidence=<workflow-file>/<job-name> (and whose pushed head
// declares exactly the same — a change never grants the evidence to
// itself) may show its tests pass on a pushed head that moved past the
// verified commit with GitHub check-runs instead of the deliver re-run of
// the carried test_bar. The driver accepts that only when every declared job has a
// ci_evidence entry for exactly the pushed head with conclusion success and
// the `gh api …/commits/<head>/check-runs` read is recorded (exit 0) inside
// the re-run window; anything else keeps the re-run rule. A repo that
// declares no ci_evidence behaves exactly as before — same verdict, same
// reason text — whatever the output carries.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

const (
	w116PR = "https://github.com/acme/demo/pull/7"
	// w116Conf declares two jobs: a plain one and a matrix-style check-run
	// name (spaces and parentheses), one indented, around unrelated keys.
	w116Conf = "# CI jobs that run the test bar\nstack=npm\nci_evidence=ci.yml/test\n  ci_evidence=ci.yml/e2e (chromium)\nfast_gate=npm run lint\n"
	// w116LegacyFull is the full-pass reason text before; a
	// repo without ci_evidence must keep it byte for byte.
	w116LegacyFull = "the tests are not shown to pass on the pushed head (it differs from the commit verification passed on and this dispatch did not re-run verification's test_bar in that repo after its last merge and before its gh pr merge)"
)

// w116Workflow is the CI workflow the default branch carries beside a
// declaring conf: the jobs the declaration names.
const w116Workflow = "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n"

// w116Moved lays the deliver capsule: onDefault is the conf the origin
// default branch carries (committed there with w116Workflow and merged
// into the change before verification; none when ""), atHead the conf at
// the pushed head (when it differs, the change's own commit writes it — or
// removes the file when ""), and changeFiles ({path, content} pairs) any
// further files the change's own commit writes. Verification passes on
// main's HEAD, then the delivery's own clean merge of a side branch moves
// HEAD past it. It answers the spec, the verified commit and the pushed
// head.
func w116Moved(t *testing.T, onDefault, atHead string, changeFiles ...[2]string) (ResultSpec, string, string) {
	t.Helper()
	repos := deliverCapsule(t)
	spec := deliverSpec(repos, true, 0)
	dir := filepath.Join(repos, "demo")
	conf := filepath.Join(dir, filepath.FromSlash(verifyScopeConf))
	writeConf := func(body, msg string) {
		if body == "" {
			_ = os.Remove(conf)
		} else {
			if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
				t.Fatal(err)
			}
			dwrite(t, conf, body)
		}
		tgit(t, dir, "add", "-A")
		tgit(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
	}
	if onDefault != "" {
		tgit(t, dir, "checkout", "-q", "-b", "upstream", "origin/main")
		w116WriteFile(t, dir, ".github/workflows/ci.yml", w116Workflow)
		writeConf(onDefault, "conf and CI on the default branch")
		tgit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
		tgit(t, dir, "fetch", "-q", "origin")
		tgit(t, dir, "checkout", "-q", "main")
		tgit(t, dir, "merge", "-q", "--no-edit", "origin/main")
	}
	if atHead != onDefault {
		writeConf(atHead, "the change edits the conf")
	}
	if len(changeFiles) > 0 {
		for _, f := range changeFiles {
			w116WriteFile(t, dir, f[0], f[1])
		}
		tgit(t, dir, "add", "-A")
		tgit(t, dir, "commit", "-q", "-m", "the change edits files")
	}
	tgit(t, dir, "checkout", "-q", "-b", "side", tgit(t, dir, "rev-parse", "HEAD"))
	dwrite(t, filepath.Join(dir, "b.txt"), "b\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "side")
	tgit(t, dir, "checkout", "-q", "main")
	dwrite(t, filepath.Join(dir, "a.txt"), "main\n")
	tgit(t, dir, "commit", "-q", "-am", "main work")
	verified := tgit(t, dir, "rev-parse", "HEAD")
	spec.VerifiedHeads["demo"] = verified
	spec.ReviewedHeads["demo"] = verified // the review covered the pre-delivery tree
	tgit(t, dir, "merge", "-q", "--no-edit", "side")
	pushFeature(t, dir)
	return spec, verified, tgit(t, dir, "rev-parse", "HEAD")
}

// w116WriteFile writes a repo-relative file, creating its directories.
func w116WriteFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	dwrite(t, p, body)
}

// w116Out is a merged deliver output for repos/demo, with ci_evidence
// entries when given.
func w116Out(entries ...string) string {
	out := `{"verdict":"pass","repos":[{"name":"demo","branch":"polyforge/polyforge-166","pr_url":"` + w116PR + `","merged":true,"merge_commit":"abc","tests_passed":true}]`
	if len(entries) > 0 {
		out += `,"ci_evidence":[` + strings.Join(entries, ",") + `]`
	}
	return out + `}`
}

// w116Run is one recorded check-run entry for repos/demo.
func w116Run(job, head, conclusion string) string {
	return fmt.Sprintf(`{"repo":"demo","job":%q,"check_run_id":4242,"url":"https://github.com/acme/demo/runs/4242","head_sha":%q,"conclusion":%q}`, job, head, conclusion)
}

func w116Sh(script string, exit int) CommandEvidence {
	return CommandEvidence{Argv: []string{"sh", "-c", script}, DurationMs: 9, ExitCode: exit}
}

// w116Read is the recorded check-runs read for head.
func w116Read(ownerRepo, head string, exit int) CommandEvidence {
	return w116Sh("cd repos/demo && gh api repos/"+ownerRepo+"/commits/"+head+"/check-runs --jq '.check_runs[] | [.name, .status, .conclusion] | @tsv'", exit)
}

var (
	w116Merge   = w116Sh("cd repos/demo && git merge --no-edit side", 0)
	w116Push    = w116Sh(goodPush, 0)
	w116PRMerge = w116Sh("gh pr merge "+w116PR+" --merge", 0)
)

// TestW116DeliverCIEvidence: the merge rule accepts a declaring repo's
// check-runs of the pushed head instead of the re-run, and refuses every
// incomplete or misdirected form of that evidence with the re-run reason
// plus a note naming what is missing.
func TestW116DeliverCIEvidence(t *testing.T) {
	type built struct{ verified, head string }
	both := func(b built) []string {
		return []string{w116Run("ci.yml/test", b.head, "success"), w116Run("ci.yml/e2e (chromium)", b.head, "success")}
	}
	inWindow := func(b built) []CommandEvidence {
		return []CommandEvidence{w116Merge, w116Push, w116Read("acme/demo", b.head, 0), w116PRMerge}
	}
	cases := []struct {
		name     string
		entries  func(built) []string
		evidence func(built) []CommandEvidence
		bad      string // "" = eligible without a human
	}{
		{"every declared job succeeded on the pushed head", both, inWindow, ""},
		{"the read waited inside one command", both, func(b built) []CommandEvidence {
			loop := "cd repos/demo && end=$(($(date +%s)+600)); until gh api \"repos/acme/demo/commits/" + b.head + "/check-runs?per_page=100\" --jq '[.check_runs[] | select(.status != \"completed\")] | length == 0' | grep -qx true; do [ \"$(date +%s)\" -ge \"$end\" ] && exit 1; sleep 3; done"
			return []CommandEvidence{w116Merge, w116Push, w116Sh(loop, 0), w116PRMerge}
		}, ""},
		{"wrong sha", func(b built) []string {
			return []string{w116Run("ci.yml/test", b.verified, "success"), w116Run("ci.yml/e2e (chromium)", b.head, "success")}
		}, inWindow, "not exactly the pushed head"},
		{"abbreviated sha", func(b built) []string {
			return []string{w116Run("ci.yml/test", b.head[:12], "success"), w116Run("ci.yml/e2e (chromium)", b.head, "success")}
		}, inWindow, "not exactly the pushed head"},
		{"missing job", func(b built) []string {
			return []string{w116Run("ci.yml/test", b.head, "success")}
		}, inWindow, `no ci_evidence entry records the declared job "ci.yml/e2e (chromium)"`},
		{"pending check-run", func(b built) []string {
			return []string{w116Run("ci.yml/test", b.head, "success"),
				`{"repo":"demo","job":"ci.yml/e2e (chromium)","check_run_id":4243,"url":"https://github.com/acme/demo/runs/4243","head_sha":"` + b.head + `","status":"queued","conclusion":null}`}
		}, inWindow, "a queued or in-progress one is not"},
		{"failed check-run", func(b built) []string {
			return []string{w116Run("ci.yml/test", b.head, "failure"), w116Run("ci.yml/e2e (chromium)", b.head, "success")}
		}, inWindow, `conclusion "failure"`},
		{"a failed run beside a success for the same job", func(b built) []string {
			return append(both(b), w116Run("ci.yml/test", b.head, "failure"))
		}, inWindow, `conclusion "failure"`},
		{"entry for another repo", func(b built) []string {
			return []string{strings.Replace(w116Run("ci.yml/test", b.head, "success"), `"repo":"demo"`, `"repo":"other"`, 1), w116Run("ci.yml/e2e (chromium)", b.head, "success")}
		}, inWindow, `no ci_evidence entry records the declared job "ci.yml/test"`},
		{"check_run_id not a number", func(b built) []string {
			return []string{strings.Replace(w116Run("ci.yml/test", b.head, "success"), `"check_run_id":4242`, `"check_run_id":"4242"`, 1), w116Run("ci.yml/e2e (chromium)", b.head, "success")}
		}, inWindow, `no ci_evidence entry records the declared job "ci.yml/test"`},
		{"no read recorded", both, func(b built) []CommandEvidence {
			return []CommandEvidence{w116Merge, w116Push, w116PRMerge}
		}, "recorded no `gh api repos/acme/demo/commits/"},
		{"read failed", both, func(b built) []CommandEvidence {
			return []CommandEvidence{w116Merge, w116Push, w116Read("acme/demo", b.head, 1), w116PRMerge}
		}, "recorded no `gh api"},
		{"read before the last merge", both, func(b built) []CommandEvidence {
			return []CommandEvidence{w116Read("acme/demo", b.head, 0), w116Merge, w116Push, w116PRMerge}
		}, "recorded no `gh api"},
		{"read after gh pr merge", both, func(b built) []CommandEvidence {
			return []CommandEvidence{w116Merge, w116Push, w116PRMerge, w116Read("acme/demo", b.head, 0)}
		}, "recorded no `gh api"},
		{"read names another repository", both, func(b built) []CommandEvidence {
			return []CommandEvidence{w116Merge, w116Push, w116Read("other/demo", b.head, 0), w116PRMerge}
		}, "recorded no `gh api"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, verified, head := w116Moved(t, w116Conf, w116Conf)
			b := built{verified: verified, head: head}
			err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(c.entries(b)...), c.evidence(b)), spec)
			if c.bad == "" {
				if err != nil {
					t.Fatalf("the declared CI evidence must cover the tests: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), w116LegacyFull) || !strings.Contains(err.Error(), c.bad) {
				t.Fatalf("want the re-run reason plus %q, got %v", c.bad, err)
			}
			spec.HumanAnswers = 1 // a human may still let it through
			if err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(c.entries(b)...), c.evidence(b)), spec); err != nil {
				t.Fatalf("rejected after a human answer: %v", err)
			}
		})
	}

	t.Run("the re-run still covers a declaring repo", func(t *testing.T) {
		spec, _, _ := w116Moved(t, w116Conf, w116Conf)
		evidence := []CommandEvidence{w116Merge, w116Sh("cd repos/demo && make test", 0), w116Push, w116PRMerge}
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(), evidence), spec); err != nil {
			t.Fatalf("the re-run path must stay open beside the CI path: %v", err)
		}
	})
	t.Run("no carried test_bar", func(t *testing.T) {
		spec, verified, head := w116Moved(t, w116Conf, w116Conf)
		spec.VerifiedTestBar = nil
		b := built{verified, head}
		err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(both(b)...), inWindow(b)), spec)
		if err == nil || !strings.Contains(err.Error(), "carried no test_bar") {
			t.Fatalf("CI evidence must never stand in for a missing test_bar: %v", err)
		}
	})
	t.Run("malformed declaration voids the CI path", func(t *testing.T) {
		spec, verified, head := w116Moved(t, w116Conf+"ci_evidence=ci.yml\n", w116Conf+"ci_evidence=ci.yml\n")
		b := built{verified, head}
		err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(both(b)...), inWindow(b)), spec)
		if err == nil || !strings.Contains(err.Error(), "not <workflow-file>/<job-name>") {
			t.Fatalf("a malformed declaration must leave the repo without a CI path: %v", err)
		}
	})
	t.Run("the verified head needs no evidence at all", func(t *testing.T) {
		// "Verification proved the merged tree" comes first and is
		// untouched: HEAD equals the verified commit, nothing is recorded.
		spec, _, head := w116Moved(t, w116Conf, w116Conf)
		spec.VerifiedHeads["demo"] = head
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(), []CommandEvidence{w116Push, w116PRMerge}), spec); err != nil {
			t.Fatalf("the verified head must stay covered without a re-run or CI evidence: %v", err)
		}
	})
	t.Run("light pass", func(t *testing.T) {
		spec, verified, head := w116Moved(t, w116Conf, w116Conf)
		spec.VerifiedHeads = nil
		spec.LightRecheckHeads = map[string]string{"demo": verified}
		b := built{verified, head}
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(both(b)...), inWindow(b)), spec); err != nil {
			t.Fatalf("CI evidence must stand in for the light path's re-run too: %v", err)
		}
		err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(), inWindow(b)), spec)
		if err == nil || !strings.Contains(err.Error(), "light recheck") || !strings.Contains(err.Error(), "no ci_evidence entry") {
			t.Fatalf("want the light-path reason plus the CI note, got %v", err)
		}
	})
}

// TestW116DeliverWithoutCIEvidenceUnchanged: a repo with no conf, a conf
// without the key, or the key only in a comment ignores whatever
// ci_evidence the output carries — the verdict and the reason text are
// exactly those of the same delivery without it, and the re-run still
// covers the tests.
func TestW116DeliverWithoutCIEvidenceUnchanged(t *testing.T) {
	for name, conf := range map[string]string{
		"no conf":                   "",
		"conf without the key":      "stack=go\nfast_gate=make lint\n",
		"key only inside a comment": "# ci_evidence=ci.yml/test\n",
	} {
		t.Run(name, func(t *testing.T) {
			spec, _, head := w116Moved(t, conf, conf)
			evidence := []CommandEvidence{w116Merge, w116Push, w116Read("acme/demo", head, 0), w116PRMerge}
			withCI := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(w116Run("ci.yml/test", head, "success")), evidence), spec)
			plain := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(), evidence), spec)
			if withCI == nil || plain == nil || withCI.Error() != plain.Error() {
				t.Fatalf("ci_evidence changed the verdict of a repo that declares none:\n with: %v\nplain: %v", withCI, plain)
			}
			if !strings.Contains(withCI.Error(), w116LegacyFull) || strings.Contains(withCI.Error(), "CI evidence") {
				t.Fatalf("the reason text changed: %v", withCI)
			}
			r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166", PRURL: w116PR, Merged: ptrTrue(), MergeCommit: "abc",
				ciRuns: decodeCIEvidence(json.RawMessage(`[` + w116Run("ci.yml/test", head, "success") + `]`))}
			if why := mergeBlocker(spec, r, "polyforge/polyforge-166", evidence); why != w116LegacyFull {
				t.Fatalf("the blocker = %q, want exactly %q", why, w116LegacyFull)
			}
			rerun := []CommandEvidence{w116Merge, w116Sh("cd repos/demo && make test", 0), w116Push, w116PRMerge}
			if err := ValidateStepResult(deliverRes(OutcomeCompleted, w116Out(), rerun), spec); err != nil {
				t.Fatalf("the re-run must still cover the tests: %v", err)
			}
		})
	}
}

// TestW116DeclarationComesFromTheDefaultBranch (code review): the
// declaration relaxes what counts as proof, so a change must not grant it
// to itself. The job set is read from the fetched origin default branch,
// which the pushed HEAD must contain and go beyond; a HEAD whose
// ci_evidence declaration differs from it in any way (added, narrowed,
// removed, broken) gets no CI path — the re-run rule applies, with a note
// saying why. Other conf edits leave the CI path open.
func TestW116DeclarationComesFromTheDefaultBranch(t *testing.T) {
	both := func(head string) string {
		return w116Out(w116Run("ci.yml/test", head, "success"), w116Run("ci.yml/e2e (chromium)", head, "success"))
	}
	inWindow := func(head string) []CommandEvidence {
		return []CommandEvidence{w116Merge, w116Push, w116Read("acme/demo", head, 0), w116PRMerge}
	}
	reformatted := "fast_gate=npm run lint\nfast_gate=npm test\nci_evidence=ci.yml/e2e (chromium)\nci_evidence=ci.yml/test\nstack=npm\n"
	cases := []struct {
		name, onDefault, atHead string
		bad                     string // "" = the CI path is taken
	}{
		{"identical declaration", w116Conf, w116Conf, ""},
		{"identical job set, other keys and order edited", w116Conf, reformatted, ""},
		{"the change adds the declaration", "", w116Conf, "differs from origin/main's"},
		{"the change adds the key to the default branch's conf", "stack=npm\n", w116Conf, "differs from origin/main's"},
		{"the change narrows the declaration", w116Conf, "stack=npm\nci_evidence=ci.yml/test\n", "differs from origin/main's"},
		{"the change removes the declaration", w116Conf, "stack=npm\n", "differs from origin/main's"},
		{"the change breaks a line", w116Conf, w116Conf + "ci_evidence=ci.yml\n", "differs from origin/main's"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, _, head := w116Moved(t, c.onDefault, c.atHead)
			err := ValidateStepResult(deliverRes(OutcomeCompleted, both(head), inWindow(head)), spec)
			if c.bad == "" {
				if err != nil {
					t.Fatalf("the default branch's unchanged declaration must open the CI path: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), w116LegacyFull) || !strings.Contains(err.Error(), c.bad) {
				t.Fatalf("want the re-run reason plus %q, got %v", c.bad, err)
			}
			rerun := []CommandEvidence{w116Merge, w116Sh("cd repos/demo && make test", 0), w116Push, w116PRMerge}
			if err := ValidateStepResult(deliverRes(OutcomeCompleted, both(head), rerun), spec); err != nil {
				t.Fatalf("the re-run must still cover the tests: %v", err)
			}
		})
	}
	// The default branch must not already contain the change: once it
	// does (a PR merge commit, or main fast-forwarded to HEAD), its conf
	// is the change's own and is no independent declaration.
	for name, land := range map[string]func(t *testing.T, dir string){
		"already merged by a merge commit": func(t *testing.T, dir string) {
			tgit(t, dir, "checkout", "-q", "-b", "landing", "origin/main")
			tgit(t, dir, "merge", "-q", "--no-ff", "--no-edit", "main")
			tgit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
			tgit(t, dir, "checkout", "-q", "main")
		},
		"default branch fast-forwarded to HEAD": func(t *testing.T, dir string) {
			tgit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
		},
	} {
		t.Run(name, func(t *testing.T) {
			spec, _, head := w116Moved(t, w116Conf, w116Conf)
			dir := filepath.Join(spec.ReposDir, "demo")
			land(t, dir)
			tgit(t, dir, "fetch", "-q", "origin")
			err := ValidateStepResult(deliverRes(OutcomeCompleted, both(head), inWindow(head)), spec)
			if err == nil || !strings.Contains(err.Error(), w116LegacyFull) || !strings.Contains(err.Error(), "not independent of this change") {
				t.Fatalf("want the re-run reason plus the not-independent note, got %v", err)
			}
		})
	}
}

// TestW116CIDefinitionEditsVoidTheCIPath (review round 2): a declared
// job is defined by the workflows under .github/ in the change's OWN tree,
// and the PR's check-runs run that version — so a change that weakens
// the job (its steps turned into `true`), adds a workflow with a
// same-named job, or edits an action a workflow uses would earn a genuine
// success for the pushed head. Any change under .github/ between the
// default branch and the pushed head therefore refuses the CI path (the
// re-run applies, with a note); a change that leaves .github/ untouched
// keeps it.
func TestW116CIDefinitionEditsVoidTheCIPath(t *testing.T) {
	cases := []struct {
		name  string
		files [][2]string
		bad   string // "" = the CI path is taken
	}{
		{".github untouched, other files edited", [][2]string{{"src/app.js", "x\n"}, {"README.md", "r\n"}}, ""},
		{"the change weakens the declared job", [][2]string{{".github/workflows/ci.yml", "on: pull_request\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"}}, "edits .github/"},
		{"the change adds a workflow with a same-named job", [][2]string{{".github/workflows/other.yml", w116Workflow}}, "edits .github/"},
		{"the change edits a composite action", [][2]string{{".github/actions/setup/action.yml", "runs:\n  using: composite\n  steps: []\n"}}, "edits .github/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, _, head := w116Moved(t, w116Conf, w116Conf, c.files...)
			out := w116Out(w116Run("ci.yml/test", head, "success"), w116Run("ci.yml/e2e (chromium)", head, "success"))
			evidence := []CommandEvidence{w116Merge, w116Push, w116Read("acme/demo", head, 0), w116PRMerge}
			err := ValidateStepResult(deliverRes(OutcomeCompleted, out, evidence), spec)
			if c.bad == "" {
				if err != nil {
					t.Fatalf("a change that leaves .github/ untouched keeps the CI path: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), w116LegacyFull) || !strings.Contains(err.Error(), c.bad) {
				t.Fatalf("want the re-run reason plus %q, got %v", c.bad, err)
			}
			rerun := []CommandEvidence{w116Merge, w116Sh("cd repos/demo && make test", 0), w116Push, w116PRMerge}
			if err := ValidateStepResult(deliverRes(OutcomeCompleted, out, rerun), spec); err != nil {
				t.Fatalf("the re-run must still cover the tests: %v", err)
			}
		})
	}
}

// TestW116CIEvidenceDecls: the conf is read the way the engine reads it.
func TestW116CIEvidenceDecls(t *testing.T) {
	cases := []struct {
		name, conf string
		want       []string
		bad        bool
	}{
		{"none", "stack=go\nfast_gate=make lint\n", nil, false},
		{"comment and blank lines", "# ci_evidence=ci.yml/test\n\n", nil, false},
		{"repeatable, indented, CRLF", "ci_evidence=ci.yml/test\r\n\t ci_evidence=e2e.yaml/e2e (chromium)\r\n", []string{"ci.yml/test", "e2e.yaml/e2e (chromium)"}, false},
		{"job name keeps later slashes", "ci_evidence=ci.yml/build / test\n", []string{"ci.yml/build / test"}, false},
		{"duplicates count once", "ci_evidence=ci.yml/test\nci_evidence=ci.yml/test\n", []string{"ci.yml/test"}, false},
		{"another key with the prefix", "ci_evidence_extra=ci.yml/test\nci_evidence =ci.yml/test\n", nil, false},
		{"no slash", "ci_evidence=ci.yml\n", nil, true},
		{"empty job", "ci_evidence=ci.yml/\n", nil, true},
		{"empty workflow", "ci_evidence=/test\n", nil, true},
		{"one malformed line voids every declaration", "ci_evidence=ci.yml/test\nci_evidence=\n", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ciEvidenceDecls(c.conf)
			if c.bad != (err != nil) {
				t.Fatalf("err = %v, want error %v", err, c.bad)
			}
			if strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
	for in, want := range map[string]string{
		"https://github.com/acme/demo/pull/7":        "acme/demo",
		"https://ghe.example/acme/demo/pull/7/files": "acme/demo",
		"https://github.com/acme/demo":               "",
		"https://github.com/acme/demo/issues/7":      "",
		"":                                           "",
	} {
		if got := prOwnerRepo(in); got != want {
			t.Errorf("prOwnerRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestW116ThisRepositoryDeclaresNoCIEvidence: this repository has no CI,
// so its own conf must keep the re-run rule (a tripwire: adding CI and
// the declaration is a deliberate change of this test).
func TestW116ThisRepositoryDeclaresNoCIEvidence(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(verifyScopeConf)))
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := ciEvidenceDecls(string(raw))
	if err != nil || len(jobs) != 0 {
		t.Fatalf("this repository's conf declares CI evidence (%q, %v) but the repository has no CI", jobs, err)
	}
}

// TestW116DeliverSchemaAcceptsCIEvidence: every delivery lane's deliver
// schema accepts the ci_evidence list and refuses an entry without its
// head_sha.
func TestW116DeliverSchemaAcceptsCIEvidence(t *testing.T) {
	head := strings.Repeat("a", 40)
	good := w116Out(w116Run("ci.yml/test", head, "success"))
	bad := w116Out(`{"repo":"demo","job":"ci.yml/test","check_run_id":1,"url":"u","conclusion":"success"}`)
	for _, lane := range []string{"feature-full", "bug-fix", "chore", "autopilot"} {
		bundleDir := filepath.Join(shippedWorkflowsDir, lane)
		s, err := loadStepOutputSchema(bundleDir, deliverStepID, shippedVars(t, bundleDir, deliverStepID))
		if err != nil || s == nil {
			t.Fatalf("%s/deliver: %v", lane, err)
		}
		if err := checkOutputSchema(s.Schema, json.RawMessage(good)); err != nil {
			t.Errorf("%s/deliver: a ci_evidence entry was rejected: %v", lane, err)
		}
		if err := checkOutputSchema(s.Schema, json.RawMessage(bad)); err == nil {
			t.Errorf("%s/deliver: a ci_evidence entry without head_sha was accepted", lane)
		}
	}
}

// TestW116DeliverPromptCIEvidenceBullet: every delivery lane's rendered
// deliver prompt carries the CI-evidence bullet, project-neutral, after
// the existing re-run bullets — "Verification proved the merged tree"
// still first — reads the declaration from the default branch (review
// M2), decides tests_passed on the CI path only after the check-runs are
// read, between the PR (step 5) and the merge rule (step 6), while the
// non-CI sentence stays verbatim (review m7), states that the CI path
// exists only where a re-run is owed and that a .github/ edit voids it,
// names the CI path in the tests_passed schema description (review round
// 2), and the merge rule names the evidence among the ways the tests are
// covered. Whitespace-normalised, so the text may re-wrap.
func TestW116DeliverPromptCIEvidenceBullet(t *testing.T) {
	order := []string{
		"**Verification proved the merged tree.**",
		"**Tests are covered only when HEAD equals the commit verification passed on**",
		"**A light-passed workflow**",
		"**CI evidence instead of the re-run** (only for a repo that declares it)",
		"**A conflict:**",
		"On the CI-evidence path (only where a re-run is owed",
		"**Push the feature branch (idempotent).**",
		"**Open or reuse the PR (idempotent).**",
		"**CI-evidence path only** (the CI bullet in step 3, where a re-run is owed): now - after the push, with the PR open, before step 6",
		"**Merge rule.**",
	}
	for _, lane := range []string{"feature-full", "bug-fix", "chore", "autopilot"} {
		flat := w73Flat(spawnStepPrompt(t, lane, deliverStepID))
		for _, want := range []string{
			"`.polyforge/verify-scope.conf` of the fetched default branch (`origin/<default>`, which the HEAD you push contains) declares `ci_evidence=<workflow-file>/<job-name>` lines and your HEAD declares exactly the same ones",
			"The declaration is read from the default branch, never from your change: a change that adds, narrows, removes or breaks those lines gets no CI path",
			// review round 2: the CI definition lives under .github/ in the
			// change's own tree, and the CI path exists only where a re-run
			// is owed.
			"nor does a change that edits anything under `.github/` (where the declared jobs are defined - the check-runs would run your change's own version)",
			"The CI path exists only where one of those re-runs would otherwise be owed: when HEAD equals `verified_heads.<name>` nothing is owed, so read no check-runs and record no `ci_evidence` for this repo.",
			// review m7: the non-CI path keeps its sentence verbatim; on the
			// CI path tests_passed is decided only after the check-runs are
			// read (post-push, pre-merge).
			"Record `tests_passed` for the head you are about to push: true only when it equals the verified commit or you re-ran the test bar on it.",
			"On the CI-evidence path (only where a re-run is owed - never when HEAD equals the verified commit) leave `tests_passed` undecided here: the check-runs it rests on exist only after the push, so you decide it in step 5.",
			"read the check-runs, record the `ci_evidence` entries, and only then decide this repo's `tests_passed`: true when every declared job's check-run is `success` for the pushed head; otherwise re-run the test bar now (still before `gh pr merge`) and decide it on that re-run.",
			"a successful GitHub check-run of EVERY declared job for EXACTLY the pushed head may stand in for the `test_bar` re-run",
			"(never for a missing `test_bar`: that still goes to a human)",
			"`gh api repos/<owner>/<repo>/commits/<sha>/check-runs`",
			"`repo` (`<name>`), `job` (the declaration verbatim), `check_run_id`, `url` (its `html_url`), `head_sha` and `conclusion`",
			"a queued or in-progress one is not",
			"a loop that re-reads the check-runs at most every 3 s and stops at a deadline",
			"a repo that declares no `ci_evidence` keeps the re-run rule exactly",
			"or the repo's declared CI evidence holds for the pushed head) and `tests_passed` is true",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s: the rendered deliver prompt lacks %q", lane, want)
			}
		}
		at := -1
		for _, h := range order {
			i := strings.Index(flat, h)
			if i <= at {
				t.Errorf("%s: %q is missing or out of order (the existing bullets keep their order, the CI bullet follows them)", lane, h)
			}
			at = i
		}
	}
	raw := w73Read(t, "workflows/"+workflow.SharedDir+"/steps/deliver.md")
	// the output schema's tests_passed names the CI path too (review round 2)
	if !strings.Contains(w73Flat(raw), "or (only where that re-run is owed and the repo's default branch declares ci_evidence) every declared job's check-run succeeded on it") {
		t.Error("deliver.md's tests_passed schema description does not name the CI-evidence path")
	}
	if strings.Contains(strings.ToLower(raw), "sleep") {
		t.Error("deliver.md names a sleep: the wait is a condition loop inside one command")
	}
}
