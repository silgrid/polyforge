"""pfstep.gitmerge.posixre: POSIX regexes with Go's CompilePOSIX semantics —
leftmost-longest (where Python's re is leftmost-first), Go's choice of
submatches among the longest, Go's refusals word for word, and a corpus of
Go's own answers (tests/testdata/posixre_go.json, frozen from Go's regexp
before the Go merge mechanics were deleted)."""

import json
import os
import unittest
from typing import List, Optional

from pfstep.gitmerge import posixre

TESTDATA = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata")


def byte_offsets(s: str, m: List[int]) -> List[int]:
    return [-1 if v < 0 else len(s[:v].encode("utf-8")) for v in m]


def find(expr: str, s: str, n: int = -1) -> Optional[List[List[int]]]:
    rx = posixre.compile_posix(expr)
    return rx.find_all_submatch_index(s, n)


class LeftmostLongestTest(unittest.TestCase):
    def test_the_longest_alternative_wins(self) -> None:
        # leftmost-first (Python's re) would take "a"
        self.assertEqual(find("a|ab", "xab"), [[1, 3]])
        self.assertEqual(find("(a|ab)(c|bcd)(d*)", "abcd"), [[0, 4, 0, 1, 1, 4, 4, 4]])

    def test_empty_matches_as_go_steps_over_them(self) -> None:
        self.assertEqual(find("a*", "baaa"), [[0, 0], [1, 4]])
        self.assertEqual(find("x*", "abc"), [[0, 0], [1, 1], [2, 2], [3, 3]])

    def test_groups_in_repetitions_and_classes(self) -> None:
        self.assertEqual(find("(a|b)*c", "abac"), [[0, 4, 2, 3]])
        self.assertEqual(find("[[:alpha:]]+", "ab1cd"), [[0, 2], [3, 5]])
        self.assertEqual(find("[^a]", "ab\nc"), [[1, 2], [3, 4]], "a negated class never takes a newline")
        self.assertEqual(find("[]a]", "]"), [[0, 1]])
        self.assertEqual(find("a{01}", "a{01}"), [[0, 5]], "a leading zero makes the brace a literal, as Go reads it")
        self.assertEqual(find("a**", "aaa"), [[0, 3]], "stacked repetitions are POSIX's")

    def test_anchors_are_the_line_s(self) -> None:
        self.assertEqual(find("^a", "aa"), [[0, 1]])
        self.assertEqual(find("a$", "aa"), [[1, 2]])
        self.assertEqual(find("^a", "aa", 1), [[0, 1]])

    def test_go_refusals_word_for_word(self) -> None:
        for expr, msg in [
            ("*a", "error parsing regexp: missing argument to repetition operator: `*`"),
            ("(", "error parsing regexp: missing closing ): `(`"),
            (")", "error parsing regexp: unexpected ): `)`"),
            ("[a", "error parsing regexp: missing closing ]: `[a`"),
            ("[z-a]", "error parsing regexp: invalid character class range: `z-a`"),
            ("[[:foo:]]", "error parsing regexp: invalid character class range: `[:foo:]`"),
            ("[a-b-c]", "error parsing regexp: invalid character class range: `-c`"),
            ("a{1001}", "error parsing regexp: invalid repeat count: `{1001}`"),
            ("(a{500}){3}", "error parsing regexp: invalid repeat count: `{3}`"),
        ]:
            with self.assertRaises(posixre.ReError, msg=expr) as cm:
                posixre.compile_posix(expr)
            self.assertEqual(str(cm.exception), msg)

    def test_nesting_is_bounded_and_linear(self) -> None:
        with self.assertRaises(posixre.ReError) as cm:
            posixre.compile_posix("(" * 1001 + "a" + ")" * 1001)
        self.assertIn("expression nests too deeply", str(cm.exception))
        deep = posixre.compile_posix("(" * 300 + "a" + ")" * 300)
        self.assertEqual(deep.num_subexp(), 300)
        # what a backtracking engine takes exponential time over
        rx = posixre.compile_posix("(a*)*$")
        self.assertEqual(rx.find_all_submatch_index("a" * 3000 + "b", 1), [[3001, 3001, 3001, 3001]])


class GoCorpusTest(unittest.TestCase):
    def test_every_answer_is_go_s(self) -> None:
        with open(os.path.join(TESTDATA, "posixre_go.json"), encoding="utf-8") as f:
            cases = json.load(f)["cases"]
        self.assertGreater(len(cases), 400)
        for c in cases:
            with self.subTest(re=c["re"], s=c["s"], n=c["n"]):
                try:
                    rx = posixre.compile_posix(c["re"])
                except posixre.ReError as err:
                    self.assertEqual(str(err), c.get("err"))
                    continue
                self.assertNotIn("err", c)
                got = [byte_offsets(c["s"], m) for m in rx.find_all_submatch_index(c["s"], c["n"])]
                self.assertEqual(got, c["m"])
                self.assertEqual(rx.num_subexp(), c["numsub"])
                self.assertEqual(rx.match_string(c["s"]), bool(c["m"]))


if __name__ == "__main__":
    unittest.main()
