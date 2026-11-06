"""pfstep.shellwords: word splitting, the simple-command judgment and the
raw words a derived command re-emits — the Go tables of
verifyrun/rules_test.go and units_test.go, asserting the Go behavior."""

import unittest

import verify
from pfstep import duration, rerun
from pfstep.rerun import UnitState
from pfstep.shellwords import leading_assignments, shell_words, shell_words_raw
from pfstep.units import KIND_GO, Unit


class ShellWordsTest(unittest.TestCase):
    def test_shell_words(self) -> None:
        """Port of TestShellWords (rules_test.go)."""
        for cmd, words, simple in (
            ("GOWORK=off go test ./... -run 'A|B'", ["GOWORK=off", "go", "test", "./...", "-run", "A|B"], True),
            ("cd webui && npm ci", ["cd", "webui"], False),
            ('test -z "$(gofmt -l .)"', ["test", "-z"], False),
            ('echo "a b" c\\ d', ["echo", "a b", "c d"], True),
            ("go test | tee log", ["go", "test"], False),
        ):
            with self.subTest(cmd):
                self.assertEqual(shell_words(cmd), (words, simple))
        self.assertEqual(" ".join(leading_assignments("env GOWORK=off FOO='a b' go test")), "GOWORK=off FOO='a b'")

    def test_shell_comment_is_not_simple(self) -> None:
        """Port of TestShellCommentIsNotSimple (units_test.go)."""
        _, simple = shell_words("go test ./... # all")
        self.assertFalse(simple, "a command with a comment must not count as simple")
        w, simple = shell_words("echo a#b")
        self.assertTrue(simple, "a # inside a word is literal")
        self.assertEqual(w[1], "a#b")

    def test_substitution_keeps_the_skip_form(self) -> None:
        """Port of TestSubstitutionKeepsTheSkipForm (rules_test.go)."""
        cmd = "GOWORK=off go test $(go list ./... | grep -v '/test$') -count=1"
        w, simple = shell_words(cmd)
        self.assertTrue(simple, w)
        self.assertEqual(len(w), 5, w)
        self.assertEqual(w[3], "$(go list ./... | grep -v '/test$')")
        pre = UnitState(unit=Unit(kind=KIND_GO, name="TestOld", failed=True), pre_existing=True)
        cr = verify.CmdRun(repo="app", check="app:x", name="x", phase="full", cmd=cmd, origin=verify.ORIGIN_PLAN,
                           timeout=0, units=[pre])
        self.assertEqual(verify.skip_form(cr), (cmd + " -skip '^(TestOld)$'", ""))
        r, _ = rerun.go_rerun(cmd, "^TestOld$", "./a/", duration.MINUTE)
        self.assertEqual(r, "GOWORK=off go test -count=1 -v -timeout 1m0s -run '^TestOld$' ./a/")
        for c in ('echo "$(date)"', "echo $(unclosed", "echo 'unclosed"):
            with self.subTest(c):
                self.assertFalse(shell_words(c)[1], "%r must not count as simple" % c)
        # a literal dollar stays literal: the raw text is what gets re-emitted
        w, raws, simple = shell_words_raw("X='a$b' go test \\$y")
        self.assertTrue(simple)
        self.assertEqual((w[0], raws[0], raws[3]), ("X=a$b", "X='a$b'", "\\$y"))
        self.assertEqual(" ".join(leading_assignments("X='a$b' go test")), "X='a$b'", "assignments re-emit raw")
        w, simple = shell_words("FOO=${BAR} go test ./... -tags=$TAGS")
        self.assertTrue(simple, w)
        self.assertEqual((w[0], w[4]), ("FOO=${BAR}", "-tags=$TAGS"), "expansions in words")
        self.assertEqual(" ".join(leading_assignments("FOO=$(pwd)/x go test")), "FOO=$(pwd)/x",
                         "an assignment with an expansion is re-emitted raw")


if __name__ == "__main__":
    unittest.main()
