"""pfstep.units: the failure units and signatures read out of a command's
output — the Go table of verifyrun/units_test.go, asserting the Go
behavior (TestShellCommentIsNotSimple lives in test_shellwords.py)."""

import os
import unittest
from typing import Dict, List, Optional, Sequence

from pfstep import units
from pfstep.units import (
    KIND_E2E_SUITE,
    KIND_GO,
    KIND_GO_PKG,
    KIND_LIBTEST,
    KIND_MJS,
    KIND_PYTEST,
    KIND_UNITTEST,
    Unit,
    find_unit,
    norm_sig,
    parse_units,
    unit_passed,
)

TESTDATA = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata")


def lines(s: str) -> List[str]:
    """Go's lines(): strings.Split(strings.TrimPrefix(s, "\\n"), "\\n")."""
    if s.startswith("\n"):
        s = s[1:]
    return s.split("\n")


class _UnitsCase(unittest.TestCase):
    def must_unit(self, us: Sequence[Unit], kind: str, name: str) -> Unit:
        u = find_unit(us, kind, name)
        if u is None:
            self.fail("no %s unit %r in %r" % (kind, name, [x.kind + ":" + x.name for x in us]))
        return u


class ParseTest(_UnitsCase):
    def test_parse_go_non_verbose(self) -> None:
        """Port of TestParseGoNonVerbose (units_test.go)."""
        out = """
--- FAIL: TestAlpha (0.00s)
    alpha_test.go:12: want 3, got 4
FAIL
FAIL\texample.com/m/alpha\t0.004s
ok  \texample.com/m/beta\t0.010s
# example.com/m/gamma
gamma/g.go:3:1: syntax error
FAIL\texample.com/m/gamma [build failed]
panic: test timed out after 1s
FAIL\texample.com/m/delta\t1.002s
?   \texample.com/m/cmd\t[no test files]"""
        us = parse_units(lines(out))
        a = self.must_unit(us, KIND_GO, "TestAlpha")
        self.assertTrue(a.failed, a)
        self.assertEqual(a.pkg, "example.com/m/alpha")
        self.assertEqual(a.signature, "alpha_test.go:N: want 3, got 4")
        self.assertIsNone(find_unit(us, KIND_GO_PKG, "example.com/m/alpha"),
                          "a package whose failure is explained by its tests is no unit of its own")
        self.assertFalse(self.must_unit(us, KIND_GO_PKG, "example.com/m/beta").failed, "ok package must pass")
        g = self.must_unit(us, KIND_GO_PKG, "example.com/m/gamma")
        self.assertTrue(g.failed and g.build_failed, g)
        d = self.must_unit(us, KIND_GO_PKG, "example.com/m/delta")
        self.assertTrue(d.failed and not d.build_failed, d)

    def test_parse_go_verbose_signature(self) -> None:
        """Port of TestParseGoVerboseSignature (units_test.go)."""
        out = """
=== RUN   TestBeta
=== RUN   TestBeta/case_1
    beta_test.go:40: boom at port 34567
--- FAIL: TestBeta (0.01s)
    --- FAIL: TestBeta/case_1 (0.00s)
=== RUN   TestGamma
--- PASS: TestGamma (0.00s)
FAIL
FAIL\texample.com/m/beta\t0.02s"""
        us = parse_units(lines(out))
        b = self.must_unit(us, KIND_GO, "TestBeta")
        self.assertTrue(b.failed, b)
        self.assertEqual(b.signature, "beta_test.go:N: boom at port 34567")
        self.assertIsNone(find_unit(us, KIND_GO, "TestBeta/case_1"), "an indented subtest header is not a top-level unit")
        g = self.must_unit(us, KIND_GO, "TestGamma")
        self.assertFalse(g.failed, g)
        self.assertEqual(g.pkg, "example.com/m/beta")

    def test_parse_harness_fail_lines(self) -> None:
        """Port of TestParseHarnessFailLines (units_test.go)."""
        out = ("ok  \texample.com/m/unit\t0.1s\n"
               "FAIL TestW1Thing\n"
               "slowest:\n"
               "=== output of TestW1Thing\n"
               "=== RUN   TestW1Thing\n"
               "    w1_test.go:9: daemon never came up\n"
               "--- FAIL: TestW1Thing (3.00s)\n"
               "FAIL\n"
               "FAIL\te2e\t5/5 tests ran, failed: TestW1Thing \t9s (E2E_JOBS=4)")
        us = parse_units(lines(out))
        n = 0
        for u in us:
            if u.kind == KIND_GO and u.name == "TestW1Thing":
                n += 1
                self.assertTrue(u.failed, u)
                self.assertEqual(u.signature, "w1_test.go:N: daemon never came up")
        self.assertEqual(n, 1, "TestW1Thing parsed %d times, want once" % n)
        self.assertIsNone(find_unit(us, KIND_GO_PKG, "e2e"), "the harness summary's failure is explained by its tests")

    def test_parse_pytest(self) -> None:
        """Port of TestParsePytest (units_test.go)."""
        out = """
tests/test_a.py::test_ok PASSED                                [ 33%]
tests/test_a.py::TestC::test_bad FAILED                         [ 66%]
_____________________________ TestC.test_bad _____________________________
    def test_bad(self):
>       assert 1 == 2
E       assert 1 == 2
=========================== short test summary info ============================
FAILED tests/test_a.py::TestC::test_bad - assert 1 == 2
FAILED tests/test_b.py::test_other"""
        us = parse_units(lines(out))
        self.assertFalse(self.must_unit(us, KIND_PYTEST, "tests/test_a.py::test_ok").failed, "PASSED node must pass")
        u = self.must_unit(us, KIND_PYTEST, "tests/test_a.py::TestC::test_bad")
        self.assertTrue(u.failed, u)
        self.assertEqual(u.signature, "assert 1 == 2")
        self.assertTrue(self.must_unit(us, KIND_PYTEST, "tests/test_b.py::test_other").failed)

    def test_parse_unittest(self) -> None:
        """Port of TestParseUnittest (units_test.go)."""
        out = """
test_ok (pkg.test_m.TestM.test_ok) ... ok
======================================================================
FAIL: test_bad (pkg.test_m.TestM)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "pkg/test_m.py", line 7, in test_bad
    self.assertEqual(1, 2)
AssertionError: 1 != 2
----------------------------------------------------------------------
Ran 2 tests in 0.001s"""
        us = parse_units(lines(out))
        self.assertFalse(self.must_unit(us, KIND_UNITTEST, "pkg.test_m.TestM.test_ok").failed, "ok case must pass")
        u = self.must_unit(us, KIND_UNITTEST, "pkg.test_m.TestM.test_bad")
        self.assertTrue(u.failed, u)
        self.assertEqual(u.signature, "AssertionError: 1 != 2")

    def test_parse_libtest(self) -> None:
        """Port of TestParseLibtest (units_test.go)."""
        out = """
running 2 tests
test tests::good ... ok
test tests::bad ... FAILED

failures:

---- tests::bad stdout ----
thread 'tests::bad' panicked at src/lib.rs:10:9:
assertion failed: 1 == 2
note: run with RUST_BACKTRACE=1

failures:
    tests::bad"""
        us = parse_units(lines(out))
        self.assertFalse(self.must_unit(us, KIND_LIBTEST, "tests::good").failed, "ok test must pass")
        u = self.must_unit(us, KIND_LIBTEST, "tests::bad")
        self.assertTrue(u.failed, u)
        self.assertEqual(u.signature, "assertion failed: 1 == 2")

    def test_parse_mjs_markers(self) -> None:
        """Port of TestParseMjsMarkers (units_test.go)."""
        out = """
PASS: MP1 opens the console
FAIL: MP2 renders the list
  expected 3 rows, saw 0
PASS: MP3 pages"""
        us = parse_units(lines(out))
        self.assertFalse(self.must_unit(us, KIND_MJS, "MP1 opens the console").failed, "PASS marker must pass")
        u = self.must_unit(us, KIND_MJS, "MP2 renders the list")
        self.assertTrue(u.failed, u)
        self.assertEqual(u.signature, "expected 3 rows, saw 0")

    def test_parse_nothing_locatable(self) -> None:
        """Port of TestParseNothingLocatable (units_test.go)."""
        self.assertEqual(parse_units(lines("make: *** [lint] Error 1\nsomething broke")), [])

    def test_parse_go_same_name_two_packages(self) -> None:
        """Port of TestParseGoSameNameTwoPackages (units_test.go)."""
        out = """
=== RUN   TestNew
--- PASS: TestNew (0.00s)
PASS
ok  \texample.com/m/b\t0.01s
=== RUN   TestNew
    new_test.go:5: broken in a
--- FAIL: TestNew (0.00s)
FAIL
FAIL\texample.com/m/a\t0.01s
--- FAIL: TestNew (0.00s)
    new_test.go:9: broken in c
FAIL
FAIL\texample.com/m/c\t0.01s"""
        got: Dict[str, bool] = {}
        for u in parse_units(lines(out)):
            if u.kind == KIND_GO and u.name == "TestNew":
                got[u.pkg] = u.failed
        self.assertEqual(got, {"example.com/m/b": False, "example.com/m/a": True, "example.com/m/c": True})

    def test_package_that_did_not_finish_is_a_unit(self) -> None:
        """Port of TestPackageThatDidNotFinishIsAUnit (units_test.go)."""
        head = "--- FAIL: TestA (0.00s)\n    a_test.go:9: old breakage\n"
        for name, out, failfast, want in (
            ("finished", head + "FAIL\nFAIL\texample.invalid/x\t0.003s", False, False),
            ("goroutine panic", head + "panic: boom in goroutine\nexample.invalid/x.TestZ.func1()\nFAIL\texample.invalid/x\t0.005s",
             False, True),
            ("timeout", head + "panic: test timed out after 3s\nFAIL\texample.invalid/x\t3.019s", False, True),
            ("os.Exit", head + "FAIL\texample.invalid/x\t0.003s", False, True),
            ("failfast", head + "FAIL\nFAIL\texample.invalid/x\t0.003s", True, True),
        ):
            with self.subTest(name):
                us = parse_units(lines(out), failfast=failfast)
                u: Optional[Unit] = find_unit(us, KIND_GO_PKG, "example.invalid/x")
                self.assertEqual(u is not None and u.failed, want, "package unit = %r" % u)
                a = find_unit(us, KIND_GO, "TestA")
                self.assertTrue(a is not None and a.failed, "TestA must stay a failing unit")


# aggregate_out is the aggregate e2e runner's output shape (a passing and a
# failing suite block, the failures section with the failing suite's lines
# and its re-run, the summary).
AGGREGATE_OUT = """e2e/all: 2 suite(s), browser pool 2, serial lane 1

===== PASS a.mjs (3s) =====
PASS: A1 opens
FAIL: A0 expected-fail note echoed by a passing suite

===== FAIL b.mjs (9s) =====
PASS: B1 loads
FAIL: B2 clicks
  locator.click: Timeout 8000ms exceeded

===== failures =====
FAIL e2e/b.mjs: B2 clicks — locator.click: Timeout 8000ms exceeded
FAIL e2e/b.mjs: B3 saves — expected 2 rows at e2e/b.mjs:41
re-run: npm run test:e2e:all -- b.mjs

===== summary =====
PASS a.mjs 3s
FAIL b.mjs 9s
1/2 passed in 12s"""


class AggregateTest(_UnitsCase):
    def test_parse_aggregate_e2e_suites(self) -> None:
        """Port of TestParseAggregateE2ESuites (units_test.go)."""
        us = parse_units(lines(AGGREGATE_OUT))
        b = self.must_unit(us, KIND_E2E_SUITE, "e2e/b.mjs")
        self.assertTrue(b.failed, b)
        self.assertEqual(b.printed_rerun, "npm run test:e2e:all -- b.mjs")
        self.assertEqual(b.signature,
                         "B2 clicks — locator.click: Timeout 8000ms exceeded | B3 saves — expected 2 rows at e2e/b.mjs:N")
        self.assertFalse(self.must_unit(us, KIND_E2E_SUITE, "e2e/a.mjs").failed, "passing suite block")
        for u in us:
            self.assertEqual(u.kind, KIND_E2E_SUITE, "a marker inside a suite block or the summary became a unit: %r" % u)

    def test_parse_aggregate_outside_the_grammar(self) -> None:
        """Port of TestParseAggregateOutsideTheGrammar (units_test.go)."""
        cut = "===== FAIL b.mjs (9s) =====\nFAIL: B2 clicks\n  boom\n"
        for u in parse_units(lines(cut)):
            self.assertFalse(u.failed, "a failed block without its failure line located %r" % u)
        self.assertEqual(parse_units(lines("FAIL e2e/b.mjs 9s\nFAIL b.mjs: B2 clicks — boom")), [],
                         "off-grammar FAIL lines located units")
        us = parse_units(lines("===== failures =====\nFAIL e2e/b.mjs: B2 — boom\nre-run: npm run test:e2e:all -- c.mjs"))
        b = self.must_unit(us, KIND_E2E_SUITE, "e2e/b.mjs")
        self.assertTrue(b.failed, b)
        self.assertEqual(b.printed_rerun, "", "a re-run naming another suite was taken")

    def test_e2e_suite_rerun_passes_only_on_its_pass_block(self) -> None:
        """Port of TestE2ESuiteRerunPassesOnlyOnItsPassBlock (units_test.go)."""
        pass_ = lines("===== PASS b.mjs (2s) =====\nPASS: B2 clicks\n\n===== summary =====\nPASS b.mjs 2s")
        self.assertTrue(unit_passed(KIND_E2E_SUITE, "e2e/b.mjs", 0, parse_units(pass_), pass_),
                        "the suite's own PASS block at exit 0 is a pass")
        self.assertFalse(unit_passed(KIND_E2E_SUITE, "e2e/b.mjs", 1, parse_units(pass_), pass_), "a non-zero exit is no pass")
        other = lines("===== PASS a.mjs (2s) =====\n\n===== summary =====")
        self.assertFalse(unit_passed(KIND_E2E_SUITE, "e2e/b.mjs", 0, parse_units(other), other),
                         "a run that never showed the suite is no pass")

    def test_mjs_rerun_passes_only_whole(self) -> None:
        """Port of TestMjsRerunPassesOnlyWhole (units_test.go)."""
        ls = lines("PASS: A\nTypeError: boom")
        self.assertFalse(unit_passed(KIND_MJS, "A", 1, parse_units(ls), ls), "a crash after the PASS line is no pass")
        self.assertTrue(unit_passed(KIND_MJS, "A", 0, parse_units(ls), ls), "a clean whole run passes")


class SignatureTest(_UnitsCase):
    def test_go_signature_covers_the_whole_failure(self) -> None:
        """Port of TestGoSignatureCoversTheWholeFailure (units_test.go)."""
        verbose = """
=== RUN   TestSub
=== RUN   TestSub/one
    sub_test.go:10: setting up after 1.5s
    sub_test.go:12: got 3, want 4
=== RUN   TestSub/two
--- FAIL: TestSub (0.00s)
    --- FAIL: TestSub/one (0.00s)
    --- PASS: TestSub/two (0.00s)
FAIL
FAIL\texample.com/m\t0.01s"""
        want = "sub_test.go:N: setting up after 1.5s | sub_test.go:N: got 3, want 4"
        self.assertEqual(self.must_unit(parse_units(lines(verbose)), KIND_GO, "TestSub").signature, want, "-v signature")
        plain = """
--- FAIL: TestSub (0.00s)
    --- FAIL: TestSub/one (0.00s)
        sub_test.go:11: setting up after 1.5s
        sub_test.go:13: got 3, want 4
FAIL
FAIL\texample.com/m\t0.01s"""
        self.assertEqual(self.must_unit(parse_units(lines(plain)), KIND_GO, "TestSub").signature, want,
                         "plain signature (same failure, other positions)")
        other = verbose.replace("got 3", "got 5", 1)
        self.assertNotEqual(self.must_unit(parse_units(lines(other)), KIND_GO, "TestSub").signature, want,
                            "a different message digit must make a different signature")

    def test_cap_text(self) -> None:
        """Port of TestCapText (units_test.go)."""
        got = units.cap_text("é" * 4000)
        raw = got.encode("utf-8")  # raises on a cut inside a rune (a lone surrogate)
        self.assertLessEqual(len(raw), units.MAX_TEXT_BYTES)
        self.assertTrue(got.endswith("…[cut]"), got[-20:])
        self.assertEqual(units.MAX_TEXT_BYTES, 4 << 10)
        self.assertEqual(units.cap_text("short"), "short", "a short text is kept")

    def test_signature_keeps_messages_and_bodies(self) -> None:
        """Port of TestSignatureKeepsMessagesAndBodies (units_test.go)."""
        self.assertEqual(norm_sig("x_test.go:12: user.id:42 latency 12ms"), "x_test.go:N: user.id:42 latency 12ms")
        self.assertNotEqual(norm_sig("latency 12ms"), norm_sig("latency 900ms"), "a duration inside a message is content")

        def diff(got: str) -> str:
            return ("--- FAIL: TestDiff (0.00s)\n    d_test.go:9: mismatch (-want +got):\n          map[string]int{\n"
                    "        -   \"a\": 1,\n        +   \"a\": " + got + ",\n          }\nFAIL\nFAIL\texample.com/m\t0.01s")

        a = self.must_unit(parse_units(lines(diff("2"))), KIND_GO, "TestDiff").signature
        b = self.must_unit(parse_units(lines(diff("3"))), KIND_GO, "TestDiff").signature
        self.assertNotEqual(a, b, "a diff body must be part of the signature")
        self.assertIn('+ "a": 2,', a)
        race = ("=== RUN   TestRace\n==================\nWARNING: DATA RACE\nWrite at 0x00c000012345 by goroutine 8:\n"
                "  example.com/m.TestRace.func1()\n      /src/r_test.go:10 +0x44\n==================\n"
                "    testing.go:1490: race detected during execution of test\n--- FAIL: TestRace (0.00s)\nFAIL\n"
                "FAIL\texample.com/m\t0.01s")
        sig = self.must_unit(parse_units(lines(race)), KIND_GO, "TestRace").signature
        self.assertIn("WARNING: DATA RACE | example.com/m.TestRace.func1()", sig)

    def test_testify_signature_across_runs(self) -> None:
        """Port of TestTestifySignatureAcrossRuns (units_test.go); reads the
        testify-*.txt fixtures copied byte-identically into tests/testdata."""

        def read(name: str, root: str) -> str:
            with open(os.path.join(TESTDATA, name), "rb") as f:
                raw = f.read().decode("utf-8")
            us = parse_units(units.normalize_output(lines(raw), root, ""))
            return self.must_unit(us, KIND_GO, "TestEq").signature

        head = read("testify-plain.txt", "/work/repo")
        base = read("testify-v.txt", "/base/wt")
        other = read("testify-v-other.txt", "/base/wt")
        self.assertEqual(head, base, "the same testify failure signs differently")
        self.assertIn("Error Trace: <root>/a_test.go:N", head)
        self.assertIn("actual : 4", head)
        self.assertNotEqual(other, base, "a different actual value must sign differently")

    def test_norm_sig_drops_the_temp_dir_suffix(self) -> None:
        """Port of TestNormSigDropsTheTempDirSuffix (units_test.go)."""
        a = norm_sig("x_test.go:5: open <tmp>/TestOld2938475/001/data: missing")
        b = norm_sig("x_test.go:7: open <tmp>/TestOld11/001/data: missing")
        self.assertEqual(a, b)
        self.assertEqual(a, "x_test.go:N: open <tmp>/TestOld/001/data: missing")
        self.assertNotEqual(norm_sig("open <tmp>/TestOld1/002/data"), norm_sig("open <tmp>/TestOld1/001/data"),
                            "the TempDir sequence number is kept")


if __name__ == "__main__":
    unittest.main()
