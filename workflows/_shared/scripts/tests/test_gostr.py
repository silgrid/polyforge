"""pfstep.gostr: Go's string helpers. The expectations are Go's own answers
(go1.26.10: strconv.Quote, strings.Fields / strings.TrimSpace, path.Clean /
path.Dir, verifyrun's cleanRel, regexp.QuoteMeta, and verifyrun's capText
body run at a given limit — a throwaway program over the same inputs),
hard-coded here."""

import unittest
from typing import List, Tuple

from pfstep import gostr, units

# (s, strconv.Quote(s))
QUOTE: List[Tuple[str, str]] = [
    ('', '""'),
    ('abc', '"abc"'),
    ('a"b', '"a\\"b"'),
    ('a\\b', '"a\\\\b"'),
    ('a\nb\tc', '"a\\nb\\tc"'),
    ('\x00\x01\x1f', '"\\x00\\x01\\x1f"'),
    ('\x7f', '"\\x7f"'),
    ('\x07\x08\x0c\r\x0b', '"\\a\\b\\f\\r\\v"'),
    ('é', '"é"'),
    ('\xa0', '"\\u00a0"'),
    ('\u200b', '"\\u200b"'),
    ('\u2028', '"\\u2028"'),
    ('😀', '"😀"'),
    ('\U000e0001', '"\\U000e0001"'),
    ('\u0301', '"\u0301"'),
    ('\ufeff', '"\\ufeff"'),
    ('\U0010ffff', '"\\U0010ffff"'),
    ('\u0378', '"\\u0378"'),
    ("it's", '"it\'s"'),
    ('日本', '"日本"'),
    ('\x85', '"\\u0085"'),
    ('\x1b[0m', '"\\x1b[0m"'),
    ('tab\there', '"tab\\there"'),
]

# (s, strings.Fields(s), strings.TrimSpace(s))
SPACE: List[Tuple[str, List[str], str]] = [
    ('', [], ''),
    ('  ', [], ''),
    ('a b', ['a', 'b'], 'a b'),
    (' \ta\n b\r\n', ['a', 'b'], 'a\n b'),
    ('a\x1cb', ['a\x1cb'], 'a\x1cb'),
    ('a\x85b', ['a', 'b'], 'a\x85b'),
    ('a\xa0b', ['a', 'b'], 'a\xa0b'),
    ('a\u3000b', ['a', 'b'], 'a\u3000b'),
    ('a\u2000b', ['a', 'b'], 'a\u2000b'),
    ('\x0ba\x0b', ['a'], 'a'),
    ('a\u200bb', ['a\u200bb'], 'a\u200bb'),
    ('\u2028x\u2029', ['x'], 'x'),
    ('a\x1f', ['a\x1f'], 'a\x1f'),
    ('\ufeffa', ['\ufeffa'], '\ufeffa'),
]

# (p, path.Clean(p), path.Dir(p), cleanRel(p) of verifyrun/capsulefacts.go)
PATHS: List[Tuple[str, str, str, str]] = [
    ('', '.', '.', ''),
    ('.', '.', '.', ''),
    ('..', '..', '.', ''),
    ('/', '/', '/', ''),
    ('//', '/', '/', ''),
    ('///a', '/a', '/', ''),
    ('a//b', 'a/b', 'a', 'a/b'),
    ('a/./b', 'a/b', 'a', 'a/b'),
    ('a/../b', 'b', '.', 'b'),
    ('../a', '../a', '..', ''),
    ('a/..', '.', 'a', ''),
    ('/../a', '/a', '/', ''),
    ('./a/', 'a', 'a', 'a'),
    ('//a//b/', '/a/b', '/a/b', ''),
    ('a/b/../../..', '..', '.', ''),
    ('a/b', 'a/b', 'a', 'a/b'),
    ('a', 'a', '.', 'a'),
    ('/a', '/a', '/', ''),
    ('a/', 'a', 'a', 'a'),
    ('a//b//', 'a/b', 'a/b', 'a/b'),
    ('//a', '/a', '/', ''),
    (' a/b ', ' a/b ', ' a', 'a/b'),
    ('a/../../b', '../b', '..', ''),
    ('./', '.', '.', ''),
    ('..a/b', '..a/b', '..a', '..a/b'),
    ('a/b/..', 'a', 'a/b', 'a'),
]

# (s, limit, capText's body (verifyrun/units.go) with maxTextBytes = limit)
CAP: List[Tuple[str, int, str]] = [
    ('éééééééééé', 12, 'é …[cut]'),
    ('éééééééééé', 20, 'éééééééééé'),
    ('éééééééééé', 19, 'ééééé …[cut]'),
    ('abcdefghijklmnop', 12, 'abc …[cut]'),
    ('😀😀😀😀😀', 15, '😀 …[cut]'),
    ('😀😀😀😀😀', 14, '😀 …[cut]'),
    ('😀😀😀😀😀', 13, '😀 …[cut]'),
    ('short', 12, 'short'),
    ('abéééééééééé', 14, 'abé …[cut]'),
]

# (s, regexp.QuoteMeta(s))
QUOTE_META: List[Tuple[str, str]] = [
    ('a.b*c+d?e(f)g|h[i]j{k}l^m$n\\o', 'a\\.b\\*c\\+d\\?e\\(f\\)g\\|h\\[i\\]j\\{k\\}l\\^m\\$n\\\\o'),
    ('-/#:=_ ', '-/#:=_ '),
    ('TestX', 'TestX'),
    ('plain', 'plain'),
    ('é.', 'é\\.'),
    ('^Test$', '\\^Test\\$'),
]


class QuoteTest(unittest.TestCase):
    def test_quote_is_strconv_quote(self) -> None:
        """Mirrors Go's strconv.Quote, its answers derived from Go itself."""
        for s, want in QUOTE:
            with self.subTest(s=s):
                self.assertEqual(gostr.quote(s), want)


class SpaceTest(unittest.TestCase):
    def test_fields_and_trim_space_are_go_white_space(self) -> None:
        """Mirrors Go's strings.Fields and strings.TrimSpace (unicode.IsSpace:
        U+001C..U+001F and U+200B are no space, U+0085, U+00A0, U+2000 and
        U+3000 are), derived from Go itself."""
        for s, fields, trimmed in SPACE:
            with self.subTest(s=s):
                self.assertEqual(gostr.fields(s), fields)
                self.assertEqual(gostr.trim_space(s), trimmed)


class PathTest(unittest.TestCase):
    def test_path_clean_dir_and_clean_rel(self) -> None:
        """Mirrors Go's path.Clean and path.Dir and verifyrun's cleanRel,
        derived from Go itself."""
        for p, clean, directory, rel in PATHS:
            with self.subTest(p=p):
                self.assertEqual(gostr.path_clean(p), clean, "path.Clean")
                self.assertEqual(gostr.path_dir(p), directory, "path.Dir")
                self.assertEqual(gostr.clean_rel(p), rel, "cleanRel")


class CapTextTest(unittest.TestCase):
    def test_cap_text_cuts_on_a_rune_boundary(self) -> None:
        """Mirrors verifyrun's capText (units.go) at several limits: the cut
        on a UTF-8 rune boundary, the " …[cut]" mark counted in the limit."""
        self.assertEqual(gostr.CUT_MARK, " …[cut]")
        for s, limit, want in CAP:
            with self.subTest(s=s, limit=limit):
                got = gostr.cap_text(s, limit)
                self.assertEqual(got, want)
                self.assertLessEqual(len(got.encode("utf-8")), limit)

    def test_units_cap_text_at_max_text_bytes(self) -> None:
        """Mirrors capText's boundary at maxTextBytes (units.go): a text of
        exactly the limit is kept, one byte more is cut."""
        exact = "a" * units.MAX_TEXT_BYTES
        self.assertEqual(units.cap_text(exact), exact)
        over = exact + "b"
        want = "a" * (units.MAX_TEXT_BYTES - len(gostr.CUT_MARK.encode("utf-8"))) + gostr.CUT_MARK
        self.assertEqual(units.cap_text(over), want)
        self.assertEqual(len(units.cap_text(over).encode("utf-8")), units.MAX_TEXT_BYTES)


class QuoteMetaTest(unittest.TestCase):
    def test_quote_meta_is_regexp_quote_meta(self) -> None:
        """Mirrors Go's regexp.QuoteMeta, derived from Go itself."""
        for s, want in QUOTE_META:
            with self.subTest(s=s):
                self.assertEqual(gostr.quote_meta(s), want)


if __name__ == "__main__":
    unittest.main()
