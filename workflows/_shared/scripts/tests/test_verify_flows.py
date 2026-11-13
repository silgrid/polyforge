"""verify.py's flows over a real capsule: a unit failing once and passing on
its re-run is flaky n/m; a failure the base carries the same way is
pre-existing (and the three shapes that never are); a command whose located
failures were all explained is confirmed whole; a Go package that did not
finish, or a pytest -x stop, never hides behind an explained failure; a
t.TempDir() path signs alike across runs; an aggregate e2e runner's suites
are units of their own — ported from verifyrun/flows_test.go."""

import json
import os
import tempfile
import unittest
from typing import Any, Dict, Mapping, Tuple
from unittest import mock

import verify
from pfstep.preexisting import short
from pfstep.units import KIND_COMMAND, KIND_E2E_SUITE

from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    HAVE_GO,
    Fixture,
    Run,
    cmd_of,
    executable,
    fake_engine,
    git_t,
    report,
    run_verify,
)

# fake_pytest's script: it reads the tree's tests/test_*.py — a `def` line
# carrying `# FAIL <msg>` fails with that message, `# FLAKY` fails until
# $FLAKY_STATE exists (creating it), `# FLAKY2` fails twice (counting in
# $FLAKY_STATE.n), anything else passes; a node id argument selects one
# test and --deselect drops one; a tests/ENV_BROKEN file makes every run an
# environmental failure, and tests/LINT_BROKEN fails a whole run after the
# tests with nothing a parser locates.
FAKE_PYTEST = r'''#!/bin/bash
if [ -f tests/ENV_BROKEN ]; then echo "ImportError: the environment is broken"; exit 4; fi
sel=""; skip=" "; xfirst=""
while [ $# -gt 0 ]; do
  case "$1" in
    --deselect) skip="$skip$2 "; shift 2;;
    -x) xfirst=1; shift;;
    -*) shift;;
    *) sel="$1"; shift;;
  esac
done
fail=0; pass=0
for f in tests/test_*.py; do
  [ -f "$f" ] || continue
  while IFS= read -r line; do
    case "$line" in "def "*) ;; *) continue;; esac
    fn=${line#def }; fn=${fn%%(*}
    id="$f::$fn"
    if [ -n "$sel" ] && [ "$sel" != "$id" ]; then continue; fi
    case "$skip" in *" $id "*) continue;; esac
    case "$line" in
      *"# FAIL "*) echo "FAILED $id - ${line#*# FAIL }"; fail=$((fail+1)); [ -n "$xfirst" ] && break 2;;
      *"# FLAKY2"*) n=$(cat "$FLAKY_STATE.n" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" >"$FLAKY_STATE.n"
        if [ "$n" -ge 3 ]; then echo "$id PASSED"; pass=$((pass+1)); else echo "FAILED $id - assert flaky twice"; fail=$((fail+1)); fi;;
      *"# FLAKY"*) if [ -f "$FLAKY_STATE" ]; then echo "$id PASSED"; pass=$((pass+1)); else : >"$FLAKY_STATE"; echo "FAILED $id - assert flaky"; fail=$((fail+1)); fi;;
      *) echo "$id PASSED"; pass=$((pass+1));;
    esac
  done <"$f"
done
echo "$pass passed, $fail failed"
if [ -z "$sel" ] && [ -f tests/LINT_BROKEN ]; then echo "lint: something no parser locates"; exit 1; fi
[ "$fail" -eq 0 ]
'''

# fake_npm's script: an aggregate e2e runner (`npm run <script> [--
# <suite>...]`) over the tree's e2e/*.mjs — a suite whose first line is
# `// FAIL <msg>` fails with that message, `// ABORT <msg>` aborts with it
# (only its block shows it: the failure line says `no scenario marker`),
# `// FLAKY [<msg>]` fails (with msg when given) until $FLAKY_STATE exists
# (creating it), anything else passes; the output is the runner's suite
# blocks, the failures section with each failing suite's line and re-run,
# and the summary.
FAKE_NPM = r'''#!/bin/bash
[ "$1" = run ] || exit 2
shift 2
[ "$1" = -- ] && shift
sel=" $* "; fail=0; failures=""
for f in e2e/*.mjs; do
  s=${f#e2e/}
  if [ "$sel" != "  " ]; then case "$sel" in *" $s "*|*" e2e/$s "*) ;; *) continue;; esac; fi
  line=$(head -n 1 "$f"); ok=1; msg=""; abort=""
  case "$line" in
    "// FAIL "*) ok=0; msg=${line#// FAIL };;
    "// ABORT "*) ok=0; abort=${line#// ABORT };;
    "// FLAKY"*) if [ -f "$FLAKY_STATE" ]; then ok=1; else : >"$FLAKY_STATE"; ok=0; msg="locator.click: Timeout 8000ms exceeded"
      [ "$line" = "// FLAKY" ] || msg=${line#// FLAKY }; fi;;
  esac
  if [ $ok = 1 ]; then
    printf '\n===== PASS %s (1s) =====\nPASS: S1 works\n' "$s"
  elif [ -n "$abort" ]; then
    printf '\n===== FAIL %s (1s) =====\nABORT %s\n1/1 checks passed\n' "$s" "$abort"
    fail=1; failures="$failures
FAIL e2e/$s: no scenario marker — 1/1 checks passed
re-run: npm run test:e2e:all -- $s"
  else
    printf '\n===== FAIL %s (1s) =====\nFAIL: S1 works\n  %s\n' "$s" "$msg"
    fail=1; failures="$failures
FAIL e2e/$s: S1 works — $msg
re-run: npm run test:e2e:all -- $s"
  fi
done
if [ $fail = 1 ]; then printf '\n===== failures =====%s\n' "$failures"; fi
printf '\n===== summary =====\n'
exit $fail
'''


def fake_bin(root: str, name: str, script: str) -> Dict[str, str]:
    """A step environment's PATH with a private directory carrying the
    script as name in front."""
    d = tempfile.mkdtemp(prefix="bin-", dir=root)
    executable(os.path.join(d, name), script)
    return {"PATH": d + os.pathsep + os.environ.get("PATH", "/usr/bin:/bin")}


def pytest_fixture(root: str, base: Mapping[str, str], feature: Mapping[str, str]) -> Tuple[Fixture, str]:
    """base on origin's main, the feature branch writing feature; the engine
    plans one full command `pytest`."""
    fx = Fixture(root, {"app": base})
    fx.commit_in_repo("app", feature, "feature")
    return fx, fake_engine(root, {"app": report("full", cmd_of("tests", "full", "pytest"))})


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class FlowsTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def sub(self, name: str) -> str:
        d = os.path.join(self.root, name)
        os.makedirs(d)
        return d

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def pytest_env(self, **extra: str) -> Dict[str, str]:
        env = fake_bin(self.root, "pytest", FAKE_PYTEST)
        env.update(extra)
        return env

    def test_flaky_unit_passes_on_rerun(self) -> None:
        """Port of TestFlakyUnitPassesOnRerun (flows_test.go): a unit failing
        once, then passing on its targeted re-run at HEAD, is a pass
        recorded flaky n/m."""
        fx, eng = pytest_fixture(self.root, {"tests/test_a.py": "def test_ok():\n"},
                                 {"tests/test_a.py": "def test_ok():\ndef test_wobbly(): # FLAKY\n"})
        r = run_verify(fx.capsule, eng, env=self.pytest_env(FLAKY_STATE=os.path.join(self.root, "flaky")))
        u = r.unit("tests/test_a.py::test_wobbly")
        self.assertEqual(u["status"], "flaky", u)
        self.assertIn("flaky 1/2", u.get("detail", ""), u)
        c = r.check("app:tests")
        self.assertEqual(c["status"], "pass", c)
        self.assertIn("flaky 1/2", c.get("detail", ""), c)
        d = self.doc(r)
        self.assertEqual(d["mechanical_verdict"], "pass", d.get("fail_reason"))
        self.assertEqual(r.exit, 0, r.result.get("summary"))

    def test_pre_existing_proven_on_base(self) -> None:
        """Port of TestPreExistingProvenOnBase (flows_test.go): a failure that
        exists at the base and fails there the same way is pre-existing —
        its check, its pre_existing_failures entry and the baseline (the
        merge-base) in the detail; a test the base does not have is this
        change's failure; the base worktree is removed afterwards."""
        fx, eng = pytest_fixture(self.root, {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
                                 {"tests/test_b.py": "def test_new(): # FAIL assert new breakage\n"})
        r = run_verify(fx.capsule, eng, env=self.pytest_env())
        d = self.doc(r)
        baseline = d["merge_outcomes"]["app"].get("baseline", "")
        self.assertNotEqual(baseline, "", d["merge_outcomes"])
        base = short(baseline)
        old = r.unit("tests/test_a.py::test_old")
        self.assertEqual(old["status"], "pre_existing", old)
        self.assertIn(base, old.get("detail", ""), old)
        nw = r.unit("tests/test_b.py::test_new")
        self.assertEqual(nw["status"], "fail", nw)
        self.assertIn("base does not have this test", nw.get("detail", ""), nw)
        self.assertEqual(d["pre_existing_failures"], ["tests/test_a.py: test_old"])
        c = r.check("app:tests")
        self.assertEqual(c["status"], "fail", "a check with a new failure is fail: %s" % c)
        self.assertIn("test_new", c.get("detail", ""), c)
        # only the pre-existing one: the check itself is pre_existing
        fx.commit_in_repo("app", {"tests/test_b.py": "def test_new():\n"}, "fix")
        r = run_verify(fx.capsule, eng, env=self.pytest_env(),
                       out=os.path.join(fx.capsule, "artifacts", "verify-2", "result.json"))
        c = r.check("app:tests")
        self.assertEqual(c["status"], "pre_existing", c)
        self.assertIn(base, c.get("detail", ""), c)
        self.assertEqual(self.doc(r)["mechanical_verdict"], "pass")
        listed = git_t(fx.repo("app"), "worktree", "list")
        self.assertEqual(len([ln for ln in listed.split("\n") if ln.strip()]), 1,
                         "the base worktree was not removed:\n" + listed)

    def test_pre_existing_negative_controls(self) -> None:
        """Port of TestPreExistingNegativeControls (flows_test.go): the three
        false-positive shapes are each a new failure — a different signature
        on the base, an environmental failure on the base, and a protected
        (authored) test."""
        with self.subTest("different signature"):
            root = self.sub("signature")
            fx, eng = pytest_fixture(root, {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
                                     {"tests/test_a.py": "def test_old(): # FAIL assert something else\n"})
            r = run_verify(fx.capsule, eng, env=self.pytest_env())
            u = r.unit("tests/test_a.py::test_old")
            self.assertEqual(u["status"], "fail", u)
            self.assertIn("fails differently", u.get("detail", ""), u)
        with self.subTest("environmental at base"):
            root = self.sub("environmental")
            fx = Fixture(root, {"app": {"tests/test_a.py": "def test_old(): # FAIL assert old\n", "tests/ENV_BROKEN": "x\n"}})
            os.remove(os.path.join(fx.repo("app"), "tests", "ENV_BROKEN"))
            fx.commit_in_repo("app", {"c.txt": "x\n"}, "feature")
            eng = fake_engine(root, {"app": report("full", cmd_of("tests", "full", "pytest"))})
            r = run_verify(fx.capsule, eng, env=self.pytest_env())
            u = r.unit("tests/test_a.py::test_old")
            self.assertEqual(u["status"], "fail", u)
            self.assertIn("environmental", u.get("detail", ""), u)
        with self.subTest("protected by a unit run's test commit"):
            root = self.sub("protected")
            fx, eng = pytest_fixture(root, {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
                                     {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n# the unit's test commit\n"})
            test_commit = fx.head("app")
            units = os.path.join(root, "units.json")
            with open(units, "w", encoding="utf-8") as f:
                json.dump([{"id": "u1", "test_commit": test_commit, "impl_commit": "",
                            "run": {"repo": "app", "cwd": "", "argv": ["sh", "-c", "echo UNIT-OK"]},
                            "marker": "UNIT-OK"}], f)
            r = run_verify(fx.capsule, eng, "--unit-runs", units, env=self.pytest_env())
            u = r.unit("tests/test_a.py::test_old")
            self.assertEqual(u["status"], "fail", u)
            self.assertTrue(u.get("protected"), u)
            self.assertIn("protected", u.get("detail", ""), u)
            self.assertEqual(self.doc(r)["pre_existing_failures"], [])

    def test_unparseable_failure_is_one_unit_never_pre_existing(self) -> None:
        """Port of TestUnparseableFailureIsOneUnitNeverPreExisting
        (flows_test.go): a failed command with no locatable unit is the
        whole command, never pre-existing, never re-run."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("full", cmd_of("lint", "fast", "echo 'lint: 3 issues'; exit 1"))})
        u = run_verify(fx.capsule, eng).unit("app:lint")
        self.assertEqual((u["kind"], u["status"]), (KIND_COMMAND, "fail"), u)
        self.assertIn("never pre-existing", u.get("detail", ""), u)

    def test_explained_failures_are_confirmed_whole(self) -> None:
        """Port of TestExplainedFailuresAreConfirmedWhole (flows_test.go): a
        command whose located failures all turned out flaky is re-run whole;
        an exit no unit explains (a lint step after the tests) leaves a
        residual failure — never a pass; without it the whole re-run clears
        the check."""
        fx, eng = pytest_fixture(self.root, {"tests/test_a.py": "def test_ok():\n"},
                                 {"tests/test_a.py": "def test_ok():\ndef test_wobbly(): # FLAKY\n", "tests/LINT_BROKEN": "x\n"})
        r = run_verify(fx.capsule, eng, env=self.pytest_env(FLAKY_STATE=os.path.join(self.root, "flaky")))
        u = r.unit("tests/test_a.py::test_wobbly")
        self.assertEqual(u["status"], "flaky", u)
        c = r.check("app:tests")
        self.assertEqual(c["status"], "fail", c)
        self.assertIn("a failure no unit locates", c.get("detail", ""), c)
        # without the unlocated failure the whole re-run clears it
        fx.commit_in_repo("app", {"tests/LINT_BROKEN": ""}, "noop")
        os.remove(os.path.join(fx.repo("app"), "tests", "LINT_BROKEN"))
        git_t(fx.repo("app"), "commit", "-q", "-am", "fixed lint")
        r = run_verify(fx.capsule, eng, env=self.pytest_env(FLAKY_STATE=os.path.join(self.root, "flaky2")),
                       out=os.path.join(fx.capsule, "artifacts", "verify-2", "result.json"))
        c = r.check("app:tests")
        self.assertEqual(c["status"], "pass", c)
        self.assertIn("re-run whole as `pytest`: exit 0", c.get("detail", ""), c)

    def test_pre_existing_confirmed_through_the_bar_form(self) -> None:
        """Port of TestPreExistingConfirmedThroughTheBarForm (flows_test.go):
        a pre-existing exclusion is confirmed by running the bar's own skip
        form once."""
        fx, eng = pytest_fixture(self.root, {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"},
                                 {"tests/test_b.py": "def test_new():\n"})
        c = run_verify(fx.capsule, eng, env=self.pytest_env()).check("app:tests")
        self.assertEqual(c["status"], "pre_existing", c)
        self.assertIn("re-run whole as `pytest --deselect tests/test_a.py::test_old`: exit 0", c.get("detail", ""), c)

    def test_flaky_counts_passes_over_runs(self) -> None:
        """Port of TestFlakyCountsPassesOverRuns (flows_test.go): with two
        re-runs allowed (Options.Reruns; the light default is one), a unit
        passing on its second re-run is one pass in three runs."""
        fx, eng = pytest_fixture(self.root, {"tests/test_a.py": "def test_ok():\n"},
                                 {"tests/test_a.py": "def test_ok():\ndef test_twice(): # FLAKY2\n"})
        with mock.patch.object(verify, "RERUNS", 2):
            r = run_verify(fx.capsule, eng, env=self.pytest_env(FLAKY_STATE=os.path.join(self.root, "flaky")))
        u = r.unit("tests/test_a.py::test_twice")
        self.assertEqual(u["status"], "flaky", u)
        self.assertIn("flaky 1/3", u.get("detail", ""), u)

    def test_pytest_stop_at_first_never_excuses_the_rest(self) -> None:
        """Port of TestPytestStopAtFirstNeverExcusesTheRest (flows_test.go):
        `pytest -x` stopped at a pre-existing failure never ran the rest;
        the confirming whole re-run in the skip form runs it and finds the
        new failure — the check fails."""
        fx = Fixture(self.root, {"app": {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"}})
        fx.commit_in_repo("app", {"tests/test_b.py": "def test_new(): # FAIL assert new breakage\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("full", cmd_of("tests", "full", "pytest -x"))})
        r = run_verify(fx.capsule, eng, env=self.pytest_env())
        u = r.unit("tests/test_a.py::test_old")
        self.assertEqual(u["status"], "pre_existing", u)
        c = r.check("app:tests")
        self.assertEqual(c["status"], "fail", c)
        self.assertIn("a failure no unit locates", c.get("detail", ""), c)

    @unittest.skipUnless(HAVE_GO, "go is not on PATH")
    def test_go_package_stops_never_hide_behind_an_explained_failure(self) -> None:
        """Port of TestGoPackageStopsNeverHideBehindAnExplainedFailure
        (flows_test.go): a package whose one failing test is pre-existing
        but whose binary did not finish — a goroutine panic, a hang past the
        plan's -timeout, -failfast so a later test never ran — keeps the
        check failed."""
        base = {
            "go.mod": "module example.invalid/stop\n\ngo 1.21\n",
            "a_test.go": "package stop\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal(\"old breakage\") }\n",
        }
        cases = {
            "goroutine panic": ("GOWORK=off go test -count=1 ./...",
                                "package stop\n\nimport \"testing\"\n\nfunc TestZ(t *testing.T) {\n\tdone := make(chan bool)\n"
                                "\tgo func() { panic(\"boom in goroutine\") }()\n\t<-done\n}\n"),
            "hang past -timeout": ("GOWORK=off go test -count=1 -timeout 3s ./...",
                                   "package stop\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n"
                                   "func TestZ(t *testing.T) { time.Sleep(8 * time.Second) }\n"),
            "failfast": ("GOWORK=off go test -count=1 -failfast ./...",
                         "package stop\n\nimport \"testing\"\n\nfunc TestZ(t *testing.T) { t.Fatal(\"new breakage\") }\n"),
        }
        for i, (name, (cmd, z)) in enumerate(sorted(cases.items())):
            with self.subTest(name):
                root = self.sub("case%d" % i)
                fx = Fixture(root, {"app": base})
                fx.commit_in_repo("app", {"z_test.go": z}, "feature")
                r = run_verify(fx.capsule, fake_engine(root, {"app": report("full", cmd_of("tests", "full", cmd))}))
                ck = r.check("app:tests")
                verdict = self.doc(r)["mechanical_verdict"]
                self.assertEqual((ck["status"], verdict), ("fail", "fail"), "check %s, verdict %s" % (ck, verdict))

    @unittest.skipUnless(HAVE_GO, "go is not on PATH")
    def test_temp_dir_paths_sign_alike(self) -> None:
        """Port of TestTempDirPathsSignAlike (flows_test.go): a failure naming
        a t.TempDir() path signs the same in the bar, the HEAD re-run and the
        base runs — only the random suffix differs — so it can be proven
        pre-existing; the same failure with a message that differs elsewhere
        cannot."""
        def old(word: str) -> str:
            return ("package td\n\nimport (\n\t\"path/filepath\"\n\t\"testing\"\n)\n\n"
                    "func TestOld(t *testing.T) { t.Fatalf(\"open %s: " + word + "\", filepath.Join(t.TempDir(), \"data\")) }\n")

        cases = {"same failure": (old("missing"), "pre_existing"), "message differs a word": (old("absent"), "fail")}
        for i, (name, (head, want)) in enumerate(sorted(cases.items())):
            with self.subTest(name):
                root = self.sub("case%d" % i)
                fx = Fixture(root, {"app": {"go.mod": "module example.invalid/td\n\ngo 1.21\n", "old_test.go": old("missing")}})
                fx.commit_in_repo("app", {"old_test.go": head, "new.go": "package td\n"}, "feature")
                eng = fake_engine(root, {"app": report("full", cmd_of("tests", "full", "GOWORK=off go test -count=1 ./..."))})
                u = run_verify(fx.capsule, eng).unit("TestOld")
                self.assertEqual(u["status"], want, u)
                self.assertIn("<tmp>/TestOld/001/data", u.get("signature", ""),
                              "want the suffix-free TempDir in its signature: %s" % u)

    def test_aggregate_e2e_suite_flaky_on_its_own_rerun(self) -> None:
        """Port of TestAggregateE2ESuiteFlakyOnItsOwnRerun (flows_test.go): a
        suite of an aggregate e2e runner failing once is located as its
        suite, re-run alone through the runner's printed `re-run:` command,
        recorded flaky 1/2 when that re-run passes, and the command then
        confirmed whole."""
        fx = Fixture(self.root, {"app": {"e2e/a.mjs": "// a\n"}})
        fx.commit_in_repo("app", {"e2e/b.mjs": "// FLAKY\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("full", cmd_of("e2e", "full", "npm run test:e2e:all"))})
        env = fake_bin(self.root, "npm", FAKE_NPM)
        env["FLAKY_STATE"] = os.path.join(self.root, "flaky")
        r = run_verify(fx.capsule, eng, env=env)
        u = r.unit("e2e/b.mjs")
        self.assertEqual((u["kind"], u.get("file"), u["status"]), (KIND_E2E_SUITE, "e2e/b.mjs", "flaky"), u)
        self.assertIn("flaky 1/2", u.get("detail", ""), u)
        c = r.check("app:e2e")
        self.assertEqual(c["status"], "pass", c)

    def test_aggregate_e2e_suite_pre_existing_on_base(self) -> None:
        """Port of TestAggregateE2ESuitePreExistingOnBase (flows_test.go): a
        suite failing the same way on the base is pre-existing per suite (its
        siblings unaffected); the aggregate runner has no skip switch, so the
        command itself stays failed with that note (fail closed)."""
        fx = Fixture(self.root, {"app": {"e2e/a.mjs": "// a\n", "e2e/c.mjs": "// FAIL expected 2 rows\n"}})
        fx.commit_in_repo("app", {"e2e/b.mjs": "// b\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("full", cmd_of("e2e", "full", "npm run test:e2e:all"))})
        r = run_verify(fx.capsule, eng, env=fake_bin(self.root, "npm", FAKE_NPM))
        self.assertEqual(r.unit("e2e/c.mjs")["status"], "pre_existing", r.unit("e2e/c.mjs"))
        self.assertEqual(r.unit("e2e/b.mjs")["status"], "pass", r.unit("e2e/b.mjs"))
        c = r.check("app:e2e")
        self.assertEqual(c["status"], "fail", c)
        self.assertIn("no skip switch", c.get("detail", ""), c)


    def test_failed_units_class_browser_crash_or_assertion(self) -> None:
        # every unit that failed its first run carries `failure`: browser_crash
        # when its output shows Chromium's `Target crashed` / `Page crashed`
        # (a suite's ABORT line inside its block included), else assertion; a
        # passing unit carries none; the report marks a flaky browser crash
        fx = Fixture(self.root, {"app": {"e2e/e.mjs": "// e\n", "e2e/b.mjs": "// FAIL locator.waitFor: Page crashed\n"}})
        fx.commit_in_repo("app", {"e2e/a.mjs": "// FAIL expect(rows).toHaveLength(2): received 1\n",
                                  "e2e/c.mjs": "// ABORT page.setViewportSize: Target crashed\n",
                                  "e2e/d.mjs": "// FLAKY page.screenshot: Target crashed\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("full", cmd_of("e2e", "full", "npm run test:e2e:all"),
                                                    cmd_of("lint", "fast", "echo 'page.click: Target crashed'; exit 1"))})
        units = os.path.join(self.root, "units.json")
        with open(units, "w", encoding="utf-8") as f:
            json.dump([{"id": "u-crash", "test_commit": "", "impl_commit": "",
                        "run": {"repo": "app", "cwd": "", "argv": ["sh", "-c", "echo 'page.goto: Page crashed'; exit 1"]},
                        "marker": "UNIT-OK"},
                       {"id": "u-ok", "test_commit": "", "impl_commit": "",
                        "run": {"repo": "app", "cwd": "", "argv": ["sh", "-c", "echo UNIT-OK"]}, "marker": "UNIT-OK"}], f)
        env = fake_bin(self.root, "npm", FAKE_NPM)
        env["FLAKY_STATE"] = os.path.join(self.root, "flaky")
        r = run_verify(fx.capsule, eng, "--unit-runs", units, env=env)
        want = {
            ("app:e2e", "e2e/a.mjs"): ("fail", "assertion"),
            ("app:e2e", "e2e/b.mjs"): ("pre_existing", "browser_crash"),
            ("app:e2e", "e2e/c.mjs"): ("fail", "browser_crash"),
            ("app:e2e", "e2e/d.mjs"): ("flaky", "browser_crash"),
            ("app:lint", "app:lint"): ("fail", "browser_crash"),
            ("app:unit-run:u-crash", "u-crash"): ("fail", "browser_crash"),
            ("app:e2e", "e2e/e.mjs"): ("pass", None),
            ("app:unit-run:u-ok", "u-ok"): ("pass", None),
        }
        got = {(u["check"], u["name"]): (u["status"], u.get("failure")) for u in self.doc(r)["units"]}
        self.assertEqual({k: got.get(k) for k in want}, want, got)
        in_details = {(c["id"], u["unit"]): (u["status"], u.get("failure"))
                      for c in r.details["checks"] for u in c.get("units", [])}
        self.assertEqual({k: in_details.get(k) for k in want}, want, in_details)
        flaky = r.report.split("### Flaky units", 1)[-1].split("\n### ", 1)[0]
        line = [ln for ln in flaky.split("\n") if "`e2e/d.mjs`" in ln]
        self.assertEqual(len(line), 1, r.report)
        self.assertIn("browser crash", line[0], r.report)

    def test_budget_spent_lists_units_not_rerun(self) -> None:
        # a failed unit whose first re-run never started (the budget spent)
        # carries not_rerun on units[] and details.checks[].units[], the
        # report lists it under `### Not re-run` by check, and its check
        # stays failed
        fx = Fixture(self.root, {"app": {"e2e/c.mjs": "// c\n"}})
        fx.commit_in_repo("app", {"e2e/a.mjs": "// FAIL expected 2 rows\n", "e2e/b.mjs": "// FAIL expected 3 rows\n"}, "feature")
        eng = fake_engine(self.root, {"app": report("full", cmd_of("e2e", "full", "npm run test:e2e:all"))})
        units = os.path.join(self.root, "units.json")
        with open(units, "w", encoding="utf-8") as f:
            json.dump([{"id": "u-red", "test_commit": "", "impl_commit": "",
                        "run": {"repo": "app", "cwd": "", "argv": ["sh", "-c", "exit 1"]}, "marker": "UNIT-OK"},
                       {"id": "u-ok", "test_commit": "", "impl_commit": "",
                        "run": {"repo": "app", "cwd": "", "argv": ["sh", "-c", "echo UNIT-OK"]}, "marker": "UNIT-OK"}], f)
        with mock.patch.object(verify, "BUDGET", 1):
            r = run_verify(fx.capsule, eng, "--unit-runs", units, env=fake_bin(self.root, "npm", FAKE_NPM))
        want = {
            ("app:e2e", "e2e/a.mjs"): ("fail", True),
            ("app:e2e", "e2e/b.mjs"): ("fail", True),
            ("app:unit-run:u-red", "u-red"): ("fail", True),
            ("app:e2e", "e2e/c.mjs"): ("pass", None),
            ("app:unit-run:u-ok", "u-ok"): ("pass", None),
        }
        got = {(u["check"], u["name"]): (u["status"], u.get("not_rerun")) for u in self.doc(r)["units"]}
        self.assertEqual({k: got.get(k) for k in want}, want, got)
        in_details = {(c["id"], u["unit"]): (u["status"], u.get("not_rerun"))
                      for c in r.details["checks"] for u in c.get("units", [])}
        self.assertEqual({k: in_details.get(k) for k in want}, want, in_details)
        self.assertEqual((r.check("app:e2e")["status"], r.check("app:unit-run:u-red")["status"]), ("fail", "fail"))
        self.assertEqual(self.doc(r)["mechanical_verdict"], "fail")
        self.assertIn("\n### Not re-run", r.report)
        section = r.report.split("\n### Not re-run", 1)[1].split("\n### ", 1)[0]
        listed = [ln for ln in section.split("\n") if ln.startswith("- ")]
        self.assertEqual(len(listed), 2, section)
        self.assertIn("`app:e2e`", listed[0], section)
        self.assertIn("`e2e/a.mjs`", listed[0], section)
        self.assertIn("`e2e/b.mjs`", listed[0], section)
        self.assertIn("`app:unit-run:u-red`", listed[1], section)
        self.assertNotIn("e2e/c.mjs", section)
        self.assertNotIn("u-ok", section)

if __name__ == "__main__":
    unittest.main()
