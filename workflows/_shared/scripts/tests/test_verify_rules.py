"""verify.py's pure rules: the test-file judgment, a command's skip form,
and how a command's output proves its suite ran to completion — the Go
tables of verifyrun/rules_test.go, completion_test.go and
w403_review_test.go, asserting the Go behavior."""

import os
import tempfile
import unittest
from typing import List, Optional

import verify
from pfstep import duration
from pfstep.rerun import UnitState
from pfstep.units import (
    KIND_E2E_SUITE,
    KIND_GO,
    KIND_GO_PKG,
    KIND_LIBTEST,
    KIND_MJS,
    KIND_PYTEST,
    Unit,
    find_unit,
    parse_units,
)

HARNESS = "test/e2e-parallel.sh"


def lines(s: str) -> List[str]:
    """Go's lines(): strings.Split(strings.TrimPrefix(s, "\\n"), "\\n")."""
    if s.startswith("\n"):
        s = s[1:]
    return s.split("\n")


def harness_repo() -> verify.RepoRun:
    """Go's harnessRepo(): a repo run whose conf declares the Go harness."""
    return verify.RepoRun(name="app", dir="", cwd_rel="repos/app", log_dir="", go_harness=HARNESS)


def cmd_run(cmd: str, units: Optional[List[UnitState]] = None, harness: bool = False, log_path: str = "",
            exit_code: int = 0) -> verify.CmdRun:
    return verify.CmdRun(repo="app", check="app:x", name="x", phase="full", cmd=cmd, origin=verify.ORIGIN_PLAN,
                         timeout=0, exit=exit_code, log_path=log_path, units=list(units or []), harness=harness)


def pre(kind: str, name: str) -> UnitState:
    return UnitState(unit=Unit(kind=kind, name=name, failed=True), pre_existing=True)


def write_log(d: str, header: str, output: str) -> str:
    p = os.path.join(d, "1.log")
    with open(p, "w", encoding="utf-8") as f:
        f.write(header + output + "\n")
    return p


class IsTestFileTest(unittest.TestCase):
    def test_is_test_file(self) -> None:
        """Port of TestIsTestFile (rules_test.go)."""
        for p, want in {
            "pkg/a_test.go": True, "web/x.test.ts": True, "web/x.spec.js": True, "py/test_x.py": True,
            "test/fixtures/data.json": True, "tests/x.txt": True, "pkg/a.go": False, "docs/testing.md": False,
        }.items():
            with self.subTest(p):
                self.assertEqual(verify.is_test_file(p, []), want)
        self.assertTrue(verify.is_test_file("e2e/flow.mjs", ["e2e/*.mjs"]), "conf test_glob must match like the engine's globs")
        self.assertTrue(verify.is_test_file("deep/x.check", ["*.check"]), "conf test_glob must match like the engine's globs")


class SkipFormTest(unittest.TestCase):
    def test_skip_form(self) -> None:
        """Port of TestSkipForm (rules_test.go)."""
        for cr, form, note in (
            (cmd_run("GOWORK=off go build ./..."), "GOWORK=off go build ./...", ""),
            (cmd_run("GOWORK=off go test -count=1 ./...", [pre(KIND_GO, "TestA"), pre(KIND_GO, "TestB")]),
             "GOWORK=off go test -count=1 ./... -skip '^(TestA|TestB)$'", ""),
            (cmd_run("go test ./... -skip '^TestZ$'", [pre(KIND_GO, "TestC")]), "go test ./... -skip '^TestZ$'", "TestC"),
            (cmd_run("GOWORK=off make test", [pre(KIND_GO, "TestD")]), "GOWORK=off make test", "TestD"),
            (cmd_run("python3 -m pytest -q", [pre(KIND_PYTEST, "tests/t.py::test_x")]),
             "python3 -m pytest -q --deselect tests/t.py::test_x", ""),
            (cmd_run("sh harness.sh", [pre(KIND_GO, "TestOld")], harness=True), "E2E_SKIP='^(TestOld)$' sh harness.sh", ""),
        ):
            with self.subTest(cr.cmd):
                got_form, got_note = verify.skip_form(cr)
                self.assertEqual(got_form, form)
                self.assertEqual(note == "", got_note == "", got_note)
                self.assertIn(note, got_note)

    def test_harness_skip_form(self) -> None:
        """Port of TestHarnessSkipForm (completion_test.go)."""
        def pres() -> List[UnitState]:
            return [pre(KIND_GO, "TestA"), pre(KIND_GO, "TestB")]

        for cmd, want in {
            "GOWORK=off sh test/e2e-parallel.sh": "E2E_SKIP='^(TestA|TestB)$' GOWORK=off sh test/e2e-parallel.sh",
            "E2E_RUN='^(TestA|TestC)$' sh test/e2e-parallel.sh":
                "E2E_SKIP='^(TestA|TestB)$' E2E_RUN='^(TestA|TestC)$' sh test/e2e-parallel.sh",
        }.items():
            with self.subTest(cmd):
                self.assertEqual(verify.skip_form(cmd_run(cmd, pres(), harness=True)), (want, ""))
        _, note = verify.skip_form(cmd_run("E2E_SKIP=x sh test/e2e-parallel.sh", pres(), harness=True))
        self.assertNotEqual(note, "", "an existing E2E_SKIP must not be merged silently")
        _, note = verify.skip_form(cmd_run("sh test/e2e-parallel.sh", pres()))
        self.assertNotEqual(note, "", "without the harness trailer there is no harness skip form")


class CompletionTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.dir = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def proof_of(self, cmd: str, output: str) -> str:
        """Go's proofOf: completionProof over a command and its output."""
        d = tempfile.mkdtemp(dir=self.dir)
        log = write_log(d, "$ header\n# cwd: x\n", output)
        return verify.completion_proof(cmd_run(cmd, harness=harness_repo().is_harness(cmd), log_path=log, exit_code=1))

    def test_completion_proof_only_the_declared_harness(self) -> None:
        """Port of TestCompletionProofOnlyTheDeclaredHarness (completion_test.go)."""
        trailer = "FAIL TestA\nFAIL\te2e\t3/3 tests ran, failed: TestA \t1s (E2E_JOBS=1)"
        for name, cmd, out, proven in (
            ("declared harness", "GOWORK=off sh test/e2e-parallel.sh", trailer, True),
            ("declared harness, ./ path, bash", "bash ./test/e2e-parallel.sh", trailer, True),
            ("declared harness with exclusions", "E2E_SKIP='^(TestB)$' sh test/e2e-parallel.sh",
             "FAIL\te2e\t2/3 tests ran, 1 excluded by E2E_SKIP, failed: TestA \t1s (E2E_JOBS=1)", True),
            ("harness lost a test", "sh test/e2e-parallel.sh", "FAIL\te2e\t2/3 tests ran, failed: TestA \t1s (E2E_JOBS=1)", False),
            ("trailer not last", "sh test/e2e-parallel.sh", trailer + "\nlint: broken", False),
            ("wrapper script", "bash scripts/ci.sh", trailer, False),
            ("sh -c string", "sh -c 'go test ./...; sh test/e2e-parallel.sh'", trailer, False),
            ("extra argument", "sh test/e2e-parallel.sh -v", trailer, False),
            ("make target", "make test", trailer, False),
            ("go test", "go test -count=1 ./...", "ok  \texample.invalid/a\t0.1s\nFAIL\texample.invalid/b\t0.1s", False),
            ("pytest -x", "pytest -x", "FAILED tests/a.py::test_x - assert 1\n===== 1 failed in 0.12s =====", False),
            ("unittest -f", "python3 -m unittest -f", "Ran 1 test in 0.001s\n\nFAILED (failures=1)", False),
            ("cargo test", "cargo test", "test result: FAILED. 2 passed; 1 failed; 0 ignored", False),
            ("node one file", "node e2e/flow.mjs", "FAIL: A\nTypeError: boom", False),
        ):
            with self.subTest(name):
                got = self.proof_of(cmd, out)
                self.assertEqual(got != "", proven, "proof %r" % got)

    def test_no_skip_form_stays_failed_without_any_proof(self) -> None:
        """Port of TestNoSkipFormStaysFailedWithoutAnyProof (completion_test.go):
        verify.Verification.confirm_phase stands for Go's runner.confirmPhase."""
        trailer = "FAIL\te2e\t3/3 tests ran, failed: TestA \t1s (E2E_JOBS=1)"
        for cmd, kind, name, out in (
            ("cargo test", KIND_LIBTEST, "tests::old", "test result: FAILED. 2 passed; 1 failed; 0 ignored"),
            ("node e2e/flow.mjs", KIND_MJS, "A", "FAIL: A"),
            ("bash scripts/ci.sh", KIND_GO, "TestA", trailer),
        ):
            with self.subTest(cmd):
                d = tempfile.mkdtemp(dir=self.dir)
                log = write_log(d, "$ h\n# c\n", out)
                rr = harness_repo()
                cr = cmd_run(cmd, [pre(kind, name)], harness=rr.is_harness(cmd), log_path=log, exit_code=1)
                rr.runs = [cr]
                v = verify.Verification(capsule=tempfile.mkdtemp(dir=self.dir), engine="x", deadline=duration.HOUR,
                                        run_dir=os.path.join(d, "run"), unit_runs=[], env={},
                                        say=lambda _msg: None, co=None)
                v.confirm_phase(rr)
                ck = verify.command_check(cr)
                self.assertEqual(ck["status"], "fail", ck)
                self.assertIn("no skip switch", ck.get("detail", ""))

    def test_harness_trailer_names_its_failures(self) -> None:
        """Port of TestHarnessTrailerNamesItsFailures (completion_test.go)."""
        out = ("FAIL TestA\n=== output of TestA\n=== RUN   TestA\n    a_test.go:3: boom\n--- FAIL: TestA (0.00s)\nFAIL\n"
               "=== output of TestQ\n=== RUN   TestQ\n"
               "FAIL\te2e\t3/3 tests ran, failed: TestA TestQ \t1s (E2E_JOBS=1)")
        us = parse_units(lines(out))
        self.assertIsNone(find_unit(us, KIND_GO_PKG, "e2e"), "a complete trailer added a package unit")
        for n in ("TestA", "TestQ"):
            u = find_unit(us, KIND_GO, n)
            self.assertTrue(u is not None and u.failed, "%s must be a failing unit: %r" % (n, u))
        lost = "FAIL\te2e\t1/2 tests ran, failed:  \t1s (E2E_JOBS=1)"
        u = find_unit(parse_units(lines(lost)), KIND_GO_PKG, "e2e")
        self.assertTrue(u is not None and u.failed, "a trailer with tests lost is a failure of its own")


def aggregate_summary(announced: str, *summary: str) -> List[str]:
    """Go's aggregateSummary: an aggregate runner's output, the announced
    suite count, then the closing summary of the suites given."""
    out: List[str] = []
    if announced != "":
        out.append("e2e/all: " + announced + " suite(s), browser pool 4, serial lane 1, next start (build once)")
    out += ["", "===== FAIL b.mjs (9s) =====", "FAIL: B2 clicks", "", "===== failures =====",
            "FAIL e2e/b.mjs: B2 clicks — boom", "re-run: npm run test:e2e:all -- b.mjs", "", "===== summary ====="]
    return out + list(summary)


class AggregateCompletionTest(unittest.TestCase):
    def test_aggregate_summary_must_count_the_announced_suites(self) -> None:
        """Port of TestW403AggregateSummaryMustCountTheAnnouncedSuites (w403_review_test.go)."""
        cr = cmd_run("", [UnitState(unit=Unit(kind=KIND_E2E_SUITE, name="e2e/b.mjs", failed=True))])
        full = ("PASS a.mjs 3s", "FAIL b.mjs 9s", "1/2 passed in 12s")
        self.assertIn("all 2 suites", verify.aggregate_completion(cr, aggregate_summary("2", *full)),
                      "the announced 2 suites, all settled: proof expected")
        for name, ls in {
            "no header": aggregate_summary("", *full),
            "fewer than announced": aggregate_summary("3", *full),
            "more than announced": aggregate_summary("1", *full),
        }.items():
            with self.subTest(name):
                self.assertEqual(verify.aggregate_completion(cr, ls), "")


if __name__ == "__main__":
    unittest.main()
