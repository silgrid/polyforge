"""pfstep.preexisting: the pre-existing judgment — the positive shape and
each false-positive shape that must stay a new failure (the Go table of
verifyrun/rules_test.go, asserting the Go behavior), and the judgment's
three-way class beside the why (polyforge#212: build's close and the fast
checks triage a still-red unit on the base with the same code, so the
class lives where the judgment does, one entry for both callers)."""

import re
import unittest
from typing import Any, List
from unittest import mock

from pfstep import git, preexisting, proc, rerun
from pfstep.preexisting import (
    ABSENT,
    PRESENT,
    UNKNOWN,
    BaseObservation,
    CLASS_PRE_EXISTING,
    CLASS_THIS_CHANGE,
    CLASS_UNDETERMINED,
    classify_base,
)
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


class ClassifyBaseClassTest(unittest.TestCase):
    def test_classify_base_class_table(self) -> None:
        """The three-way judgment's table (polyforge#212): every row of the
        ticket's conditions — same order, same why wording as classify_base —
        with its class; classify_base answers the same rows exactly as
        before (a thin wrapper, its regression table above untouched)."""
        same = BaseObservation(exit=1, found=True, failed=True, signature="x_test.go:N: boom")
        hs = "x_test.go:N: boom"
        # (name, bar_sig, head_sig, presence, presence_why, runs, want, class, why substring)
        rows = (
            ("absent at the base", hs, hs, ABSENT, "tests/t.py does not exist at (sha)", [same, same], 2,
             CLASS_THIS_CHANGE, "base does not have this test"),
            ("presence unknown", hs, hs, UNKNOWN, "its kind has no existence proof", [same, same], 2,
             CLASS_UNDETERMINED, "base does not have this test"),
            ("no targeted re-run at HEAD", hs, "", PRESENT, "", [same, same], 2,
             CLASS_UNDETERMINED, "no targeted re-run at HEAD"),
            ("an order-dependent failure", "x_test.go:N: in-suite M1", hs, PRESENT, "", [same, same], 2,
             CLASS_UNDETERMINED, "differs from its isolated re-run at HEAD"),
            ("budget cut the runs", hs, hs, PRESENT, "", [same], 2,
             CLASS_UNDETERMINED, "only 1 of 2"),
            ("base failure without a signature", hs, hs, PRESENT, "",
             [BaseObservation(exit=1, found=True, failed=True), same], 2,
             CLASS_UNDETERMINED, "without an assertion line"),
            ("base run fails differently", hs, hs, PRESENT, "",
             [BaseObservation(exit=1, found=True, failed=True, signature="x_test.go:N: other"), same], 2,
             CLASS_UNDETERMINED, "fails differently"),
            ("it passes on the base", hs, hs, PRESENT, "",
             [BaseObservation(exit=0, found=True), same], 2,
             CLASS_THIS_CHANGE, "it passes on the base"),
            ("base run never reached the failure", hs, hs, PRESENT, "",
             [BaseObservation(exit=4), same], 2,
             CLASS_UNDETERMINED, "did not reach the test's own failure"),
            ("the same failure on every base run", hs, hs, PRESENT, "", [same, same], 2,
             CLASS_PRE_EXISTING, "fails the same way on all 2 base runs"),
        )
        fn = preexisting.classify_base_class
        for name, bar, head, presence, why_not, runs, want, want_cls, want_why in rows:
            with self.subTest(name):
                cls, why = fn(bar, head, presence, why_not, runs, want)
                self.assertEqual(cls, want_cls, why)
                self.assertIn(want_why, why)
                got, why2 = classify_base(bar, head, presence == PRESENT, why_not, runs, want)
                self.assertEqual(got, want_cls == CLASS_PRE_EXISTING, why2)
                self.assertEqual(why2, why, "the wrapper keeps the judgment's wording")


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

