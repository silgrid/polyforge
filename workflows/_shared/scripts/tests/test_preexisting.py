"""pfstep.preexisting: the pre-existing judgment — the positive shape and
each false-positive shape that must stay a new failure (the Go table of
verifyrun/rules_test.go, asserting the Go behavior)."""

import re
import unittest
from typing import Any, List
from unittest import mock

from pfstep import git, preexisting, proc, rerun
from pfstep.preexisting import BaseObservation, classify_base
from pfstep.units import KIND_GO, KIND_PYTEST, KIND_UNITTEST


class ClassifyBaseTest(unittest.TestCase):
    def test_classify_base(self) -> None:
        """Port of TestClassifyBase (rules_test.go)."""
        same = BaseObservation(exit=1, found=True, failed=True, signature="x_test.go:N: boom")
        # an order-dependent failure: the bar fails one way, the isolated
        # re-runs at HEAD and base another — the base proves nothing
        got, why = classify_base("x_test.go:N: in-suite M1", "x_test.go:N: boom", True, "", [same, same], 2)
        self.assertFalse(got, why)
        self.assertIn("differs from its isolated re-run", why)
        for name, head_sig, exists, runs, want, want_why in (
            ("same failure on every base run", "x_test.go:N: boom", True, [same, same], True, "same way on all 2"),
            ("absent at base", "x_test.go:N: boom", False, [], False, "base does not have this test"),
            ("environmental at base", "x_test.go:N: boom", True, [BaseObservation(exit=2), same], False, "environmental"),
            ("different signature", "x_test.go:N: boom", True,
             [same, BaseObservation(exit=1, found=True, failed=True, signature="x_test.go:N: other")], False, "fails differently"),
            ("passes at base", "x_test.go:N: boom", True, [BaseObservation(exit=0, found=True), same], False, "passes on the base"),
            ("no HEAD signature", "", True, [same, same], False, "no targeted re-run at HEAD"),
            ("base failure without a signature", "x_test.go:N: boom", True,
             [same, BaseObservation(exit=1, found=True, failed=True)], False, "environmental"),
            ("budget cut the runs", "x_test.go:N: boom", True, [same], False, "only 1 of 2"),
        ):
            with self.subTest(name):
                got, why = classify_base(head_sig, head_sig, exists, "base does not have this test", runs, 2)
                self.assertEqual(got, want, why)
                self.assertIn(want_why, why)


if __name__ == "__main__":
    unittest.main()


# A backslash before a letter (\s, \d, \w, \b): a GNU extension of POSIX
# ERE that macOS's regex library does not know (git grep -E is the
# platform's regcomp).
_GNU_ESCAPE = re.compile(r"\\[A-Za-z]", re.ASCII)


class PortableGrepTest(unittest.TestCase):
    def test_git_grep_patterns_are_posix_ere(self) -> None:
        """Every pattern the existence proof and the test-file lookup hand
        `git grep -E` is plain POSIX ERE (a class like [[:space:]], never
        a GNU \\s), so the macOS lane reads it as Linux does."""
        calls: List[List[str]] = []

        def fake(cwd: str, *args: str, **kw: Any) -> proc.Completed:
            calls.append(list(args))
            return proc.Completed(argv=list(args), exit_code=0)

        with mock.patch.object(git, "run", fake):
            for kind, name in ((KIND_GO, "TestX"), (KIND_PYTEST, "tests/t.py::test_x[a.b]"),
                               (KIND_UNITTEST, "pkg.mod.Case.test_x")):
                preexisting.unit_presence("/nowhere", "HEAD", kind, name, "t.py")
            rerun.go_test_file("/nowhere", "TestX", "")
        patterns = [a[a.index("-E") + 1] for a in calls if "-E" in a]
        self.assertEqual(len(patterns), 4, calls)
        for p in patterns:
            self.assertIsNone(_GNU_ESCAPE.search(p), "a GNU-only escape in %r" % p)
        self.assertIn("def[[:space:]]+test_x\\(", patterns)

