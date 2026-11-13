"""facts.replay_verdict: a replayed run that could not run — a Go package
whose setup or build failed, a compile or import error, a Node module or
package not found — is not runnable, never red; a run whose assertions
failed stays red; an exit 0 is a pass. The logs under
testdata/replay/ are the shapes such runs print (an embed pattern with no
files behind it, a bare package name without its installed dependency)."""

import os
import unittest
from typing import List, Tuple
from unittest import mock

import facts

HERE = os.path.dirname(os.path.abspath(__file__))
REPLAY_LOGS = os.path.join(HERE, "testdata", "replay")


def log(name: str) -> str:
    with open(os.path.join(REPLAY_LOGS, name), encoding="utf-8") as f:
        return f.read()


PY_ASSERT_FAIL = """F
======================================================================
FAIL: test_value (tests.test_app.AppTest)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "/work/app/tests/test_app.py", line 7, in test_value
    self.assertEqual(1, 2)
AssertionError: 1 != 2

----------------------------------------------------------------------
Ran 1 test in 0.001s

FAILED (failures=1)
"""

# One line per rule that only that rule reads (beside the fixtures' lines).
EXTRA_LINES = [
    "FAIL\texample.com/app/server [build failed]",
    "server/handler.go:7:2: undefined: Handler",
    "\timports example.com/app/web: web/embed.go:19:12: pattern all:dist: no matching files found",
    "go: example.com/app/server imports example.com/dep: no required module provides package example.com/dep; to add it:",
    "Error: Cannot find module 'dep'",
    "ModuleNotFoundError: No module named 'dep'",
]

FIXTURES = ("go-embed-setup-failed.log", "node-missing-package.log", "go-assert-fail.log", "node-assert-fail.log", "go-mixed.log")


def old_red(exit_code: int, timed_out: bool) -> bool:
    """The judgement before not-runnable runs were told apart: any non-zero
    exit that was not killed and did run."""
    return exit_code != 0 and not timed_out and not facts.not_run(exit_code)


def sample_lines() -> List[str]:
    out: List[str] = []
    for name in FIXTURES:
        out += [line for line in log(name).split("\n") if line.strip()]
    return out + EXTRA_LINES + [line for line in PY_ASSERT_FAIL.split("\n") if line.strip()]


class ReplayVerdictTest(unittest.TestCase):
    def test_setup_failures_were_red_and_are_not_runnable(self) -> None:
        cases: List[Tuple[str, str, Tuple[str, ...]]] = [
            ("go-embed-setup-failed.log", "go", ("setup-failed", "embed-pattern")),
            ("node-missing-package.log", "node", ("module-not-found",)),
        ]
        for name, toolchain, rules in cases:
            with self.subTest(name):
                self.assertTrue(old_red(1, False), "the old judgement called this run red")
                verdict, hit = facts.replay_verdict(1, False, log(name))
                self.assertEqual(verdict, "not_runnable")
                assert hit is not None
                self.assertEqual(hit["toolchain"], toolchain)
                self.assertIn(hit["rule"], rules)
                self.assertIn(hit["line"], log(name).split("\n"))

    def test_assertion_failures_stay_red(self) -> None:
        for name, output in (("go-assert-fail.log", log("go-assert-fail.log")),
                             ("node-assert-fail.log", log("node-assert-fail.log")),
                             ("python", PY_ASSERT_FAIL)):
            with self.subTest(name):
                self.assertEqual(facts.replay_verdict(1, False, output), ("red", None))

    def test_exit_zero_is_pass(self) -> None:
        for name in FIXTURES:
            with self.subTest(name):
                self.assertEqual(facts.replay_verdict(0, False, log(name)), ("pass", None))

    def test_timeouts_and_missing_commands_are_not_judged(self) -> None:
        for code, timed_out in ((-1, True), (1, True), (126, False), (127, False)):
            with self.subTest(code=code, timed_out=timed_out):
                self.assertEqual(facts.replay_verdict(code, timed_out, log("go-assert-fail.log"))[0], "not_judged")

    def test_mixed_output_is_not_runnable(self) -> None:
        verdict, hit = facts.replay_verdict(1, False, log("go-mixed.log"))
        self.assertEqual(verdict, "not_runnable")
        assert hit is not None
        self.assertEqual(hit["toolchain"], "go")

    def test_assertion_quoting_rule_strings_stays_red(self) -> None:
        # A run whose assertions failed and whose messages quote what a
        # not-runnable run prints (a test of a tool that reports such runs)
        # is red: the rules read the toolchain's own report lines, not a
        # string anywhere in a line.
        for line in (
            "AssertionError: 1 != 0 : ('unit \"u1\" not runnable — node module-not-found: "
            "`  throw new ERR_MODULE_NOT_FOUND(packageName, fileURLToPath(base), null);`', '')",
            "AssertionError: expected the summary to name \"Cannot find package 'dep'\"",
            "  AssertionError [ERR_ASSERTION]: 'Error: Cannot find module \\'dep\\'' !== ''",
            "    replay_test.go:40: got red, want cannot find package \"example.com/dep\"",
            "    replay_test.go:41: want no required module provides package example.com/dep",
            "    embed_test.go:9: want web/embed.go:5:12: pattern all:dist: no matching files found",
        ):
            with self.subTest(line=line):
                self.assertEqual(facts.replay_verdict(1, False, line), ("red", None))

    def test_every_rule_is_load_bearing(self) -> None:
        rules = facts.NOT_RUNNABLE_RULES
        self.assertTrue(rules, "the rule table is empty")
        lines = sample_lines()
        for rule in rules:
            with self.subTest(rule=rule[:2]):
                without = tuple(r for r in rules if r != rule)
                flipped = []
                for line in lines:
                    before = facts.replay_verdict(1, False, line)[0]
                    with mock.patch.object(facts, "NOT_RUNNABLE_RULES", without):
                        after = facts.replay_verdict(1, False, line)[0]
                    if (before, after) == ("not_runnable", "red"):
                        flipped.append(line)
                self.assertTrue(flipped, "no sample line needs rule %s/%s" % rule[:2])
        with mock.patch.object(facts, "NOT_RUNNABLE_RULES", ()):
            for name in ("go-embed-setup-failed.log", "node-missing-package.log"):
                self.assertEqual(facts.replay_verdict(1, False, log(name)), ("red", None), name)


if __name__ == "__main__":
    unittest.main()
