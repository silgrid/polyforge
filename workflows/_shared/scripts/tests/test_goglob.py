"""pfstep.goglob: Go's path.Match (its own test table), the micro_path=
grammar built on it (``*`` never crosses ``/``, ``**`` whole segments), the
scope engine's shell-case glob read the way RE2 reads the expression the
Go side built, and the test-file classifier."""

import unittest
from typing import List, Optional, Tuple

from pfstep import goglob

# Go's src/path/match_test.go matchTests: (pattern, name, match, bad pattern)
PATH_MATCH: List[Tuple[str, str, bool, bool]] = [
    ("abc", "abc", True, False),
    ("*", "abc", True, False),
    ("*c", "abc", True, False),
    ("a*", "a", True, False),
    ("a*", "abc", True, False),
    ("a*", "ab/c", False, False),
    ("a*/b", "abc/b", True, False),
    ("a*/b", "a/c/b", False, False),
    ("a*b*c*d*e*/f", "axbxcxdxe/f", True, False),
    ("a*b*c*d*e*/f", "axbxcxdxexxx/f", True, False),
    ("a*b*c*d*e*/f", "axbxcxdxe/xxx/f", False, False),
    ("a*b*c*d*e*/f", "axbxcxdxexxx/fff", False, False),
    ("a*b?c*x", "abxbbxdbxebxczzx", True, False),
    ("a*b?c*x", "abxbbxdbxebxczzy", False, False),
    ("ab[c]", "abc", True, False),
    ("ab[b-d]", "abc", True, False),
    ("ab[e-g]", "abc", False, False),
    ("ab[^c]", "abc", False, False),
    ("ab[^b-d]", "abc", False, False),
    ("ab[^e-g]", "abc", True, False),
    ("a\\*b", "a*b", True, False),
    ("a\\*b", "ab", False, False),
    ("a?b", "a☺b", True, False),
    ("a[^a]b", "a☺b", True, False),
    ("a???b", "a☺b", False, False),
    ("a[^a][^a][^a]b", "a☺b", False, False),
    ("[a-ζ]*", "α", True, False),
    ("*[a-ζ]", "A", False, False),
    ("a?b", "a/b", False, False),
    ("a*b", "a/b", False, False),
    ("[\\]a]", "]", True, False),
    ("[\\-]", "-", True, False),
    ("[x\\-]", "x", True, False),
    ("[x\\-]", "-", True, False),
    ("[x\\-]", "z", False, False),
    ("[\\-x]", "x", True, False),
    ("[\\-x]", "-", True, False),
    ("[\\-x]", "a", False, False),
    ("[]a]", "]", False, True),
    ("[-]", "-", False, True),
    ("[x-]", "x", False, True),
    ("[x-]", "-", False, True),
    ("[x-]", "z", False, True),
    ("[-x]", "x", False, True),
    ("[-x]", "-", False, True),
    ("[-x]", "a", False, True),
    ("\\", "a", False, True),
    ("[a-b-c]", "a", False, True),
    ("[", "a", False, True),
    ("[^", "a", False, True),
    ("[^bc", "a", False, True),
    ("a[", "a", False, True),
    ("a[", "ab", False, True),
    ("a[", "x", False, True),
    ("a/b[", "x", False, True),
    ("*x", "xxx", True, False),
]


def _path_match(pattern: str, name: str) -> Tuple[bool, bool]:
    try:
        return goglob.path_match(pattern, name), False
    except goglob.BadPattern:
        return False, True


class PathMatchTest(unittest.TestCase):
    def test_go_table(self) -> None:
        for pattern, name, match, bad in PATH_MATCH:
            self.assertEqual(_path_match(pattern, name), (match, bad), (pattern, name))


class MicroGlobTest(unittest.TestCase):
    # the micro lane's table (driver/fast_lane_test.go's TestMicroGlobMatch)
    CASES = [
        ("docs/**", "docs/a.md", True),
        ("docs/**", "docs/x/y/z.md", True),
        ("docs/*", "docs/a.md", True),
        ("docs/*", "docs/x/a.md", False),  # * never crosses /
        ("docs/*.md", "docs/a.txt", False),
        ("**/*.md", "a.md", True),  # ** is zero or more segments
        ("**/*.md", "x/y/a.md", True),
        ("lib/**/en.json", "lib/en.json", True),
        ("lib/**/en.json", "lib/i18n/v2/en.json", True),
        ("README.md", "README.md", True),  # no wildcard = exact
        ("README.md", "docs/README.md", False),
        ("Docs/**", "docs/a.md", False),  # case-sensitive
        ("docs/**", "docsx/a.md", False),
        ("docs/[", "docs/[", False),  # a malformed pattern matches nothing
        ("docs/?.md", "docs/a.md", True),
        ("docs/?", "docs/ab", False),
        ("**", "a/b/c", True),
        ("a/**/b", "a/b", True),
    ]

    def test_table(self) -> None:
        for pattern, path, want in self.CASES:
            self.assertEqual(goglob.micro_glob_match(pattern, path), want, (pattern, path))

    def test_not_fnmatch(self) -> None:
        # fnmatch's * would cross / and its [!x] negates; path.Match's does neither
        self.assertFalse(goglob.micro_glob_match("docs/*", "docs/a/b.md"))
        self.assertFalse(goglob.micro_glob_match("[!a]", "b"))
        self.assertTrue(goglob.micro_glob_match("[!a]", "!"))
        self.assertTrue(goglob.micro_glob_match("[^a]", "b"))


class CaseMatchTest(unittest.TestCase):
    # facts' CarriesGlobMatch table (driver/w403_develop_e2e_carries_test.go)
    CASES = [
        ("lib/panel*", "lib/panel.ts", True),
        ("lib/*", "lib/a/b.ts", True),  # * crosses /
        ("lib/?.ts", "lib/a.ts", True),
        ("lib/[ab].ts", "lib/b.ts", True),
        ("lib/[!ab].ts", "lib/a.ts", False),
        ("lib/panel*", "web/lib/panel.ts", False),
        ("app.go", "app.go", True),
        ("app.go", "xapp.go", False),
        ("a\\*b", "a*b", True),
        ("a\\*b", "axb", False),
        ("[", "[", True),  # no closing ]: a literal
        ("a.b", "axb", False),
    ]

    def test_table(self) -> None:
        for glob, path, want in self.CASES:
            self.assertEqual(goglob.case_match(glob, path), want, (glob, path))

    def test_end_of_text_is_not_before_a_final_newline(self) -> None:
        # RE2's $ is the end of the text; Python's $ also matches before a final \n
        self.assertFalse(goglob.case_match("a", "a\n"))
        self.assertFalse(goglob.case_match("*.md", "x.md\n"))

    def test_a_glob_byte_above_ascii_stands_for_its_code_point(self) -> None:
        # the Go side quoted each byte as string(byte): a UTF-8 glob never
        # matches the same UTF-8 path outside a class
        self.assertFalse(goglob.case_match("é", "é"))
        self.assertTrue(goglob.case_match("é", "Ã©"))
        self.assertTrue(goglob.case_match("[é]", "é"))

    def test_classes_as_re2_reads_them(self) -> None:
        # a POSIX class eats the class's closing ]: what follows is still
        # the class, and without a later ] RE2 refuses the expression
        self.assertFalse(goglob.case_match("x[[:digit:]]", "x7]"))
        self.assertFalse(goglob.case_match("x[[:digit:]]", "x7"))
        self.assertTrue(goglob.case_match("[[:alpha:]][ab]", "]"))
        self.assertTrue(goglob.case_match("[[:alpha:]][ab]", "q"))
        self.assertFalse(goglob.case_match("[[:nope:]]", "a"))  # an invalid class: RE2 refuses
        self.assertFalse(goglob.case_match("[b-a]", "a"))  # an invalid range
        self.assertTrue(goglob.test_glob_match("[\\d]", "7"))  # the classifier passes \d through
        self.assertFalse(goglob.test_glob_match("[\\s]", "\v"))  # RE2's \s has no vertical tab
        self.assertTrue(goglob.case_match("[!a]", "\n"))  # a negated class takes a newline


class TestFileTest(unittest.TestCase):
    def test_classifier(self) -> None:
        cases: List[Tuple[str, Optional[List[str]], bool]] = [
            ("a/b_test.go", None, True),
            ("src/x.test.ts", None, True),
            ("src/x.spec.js", None, True),
            ("tests/test_x.py", None, True),
            ("pkg/y_test.py", None, True),
            ("test/fixture.json", None, True),
            ("a/tests/b.txt", None, True),
            ("a/b.go", None, False),
            ("src/x.ts", None, False),
            ("README.md", None, False),
            ("spec/a.js", ["spec/**"], True),
            ("deep/spec/a.js", ["spec/*"], False),
            ("x/a.snap", ["*.snap"], True),  # against the base name too
            ("./a_test.go", None, True),
        ]
        for p, globs, want in cases:
            self.assertEqual(goglob.is_test_file(p, globs or []), want, p)


if __name__ == "__main__":
    unittest.main()
