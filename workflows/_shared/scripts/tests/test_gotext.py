"""pfstep.gotext and pfstep.godecode: text and typed JSON reads with the
semantics of the Go step commands the scripts took over."""

import errno
import unittest

from pfstep import godecode as gd
from pfstep import gotext


class GoTextTest(unittest.TestCase):
    def test_space_is_gos(self) -> None:
        # Python's strip() also takes U+001C..U+001F; Go's TrimSpace does not
        self.assertEqual(gotext.trim_space("\x1c a\u2028 \x85"), "\x1c a")
        self.assertEqual(gotext.fields(" a\tb\x1fc  \u3000d "), ["a", "b\x1fc", "d"])
        self.assertEqual(gotext.fields(""), [])

    def test_quote_is_strconv_quote(self) -> None:
        self.assertEqual(gotext.quote('a"b\\c'), '"a\\"b\\\\c"')
        self.assertEqual(gotext.quote("\x01\n\t\x7f"), '"\\x01\\n\\t\\x7f"')
        self.assertEqual(gotext.quote("é☺"), '"é☺"')
        self.assertEqual(gotext.quote("\u2028\U000e0001"), '"\\u2028\\U000e0001"')
        self.assertEqual(gotext.quote_list(["docs/**", "*.md"]), '["docs/**" "*.md"]')

    def test_clip_text_counts_bytes_on_a_character_boundary(self) -> None:
        self.assertEqual(gotext.clip_text("a" * 256), "a" * 256)
        self.assertEqual(gotext.clip_text("a" * 257), "a" * 256 + "...(truncated)")
        # é is two bytes: 128 of them are exactly 256
        self.assertEqual(gotext.clip_text("é" * 128), "é" * 128)
        self.assertEqual(gotext.clip_text("a" + "é" * 128), "a" + "é" * 127 + "...(truncated)")

    def test_clip_runes_and_lists(self) -> None:
        self.assertEqual(gotext.clip_runes("éééé", 4), "éééé")
        self.assertEqual(gotext.clip_runes("ééééé", 4), "éééé" + gotext.TRUNCATED_RUNES)
        self.assertEqual(gotext.clip_list(["a", "b", "c"], 2), ["a", "b", "(+1 more)"])
        self.assertEqual(gotext.short_sha("0123456789abcdef"), "0123456789ab")

    def test_path_is_gos(self) -> None:
        self.assertEqual(gotext.path_clean("//a/../b/./c/"), "/b/c")
        self.assertEqual(gotext.path_clean(""), ".")
        self.assertEqual(gotext.path_clean("../a/.."), "..")
        self.assertEqual(gotext.path_base("a/b/"), "b")
        self.assertEqual(gotext.path_base(""), ".")
        self.assertEqual(gotext.path_dir("a"), ".")
        self.assertEqual(gotext.path_dir("a/b/c"), "a/b")
        self.assertEqual(gotext.path_ext("a.b/c"), "")
        self.assertEqual(gotext.path_ext("a/b.tar.gz"), ".gz")

    def test_case(self) -> None:
        self.assertEqual(gotext.to_lower("TOUCHED FİLES"), "touched files")
        self.assertTrue(gotext.equal_fold("READ", "read"))
        self.assertTrue(gotext.equal_fold("ſ", "s"))  # Go's simple folding
        self.assertFalse(gotext.equal_fold("ﬁ", "fi"))  # casefold() would expand it

    def test_shell_join_and_os_error(self) -> None:
        self.assertEqual(gotext.shell_join(["go", "test", "./...", "-run", "^Test(A|B)$", "it's"]),
                         "go test ./... -run '^Test(A|B)$' 'it'\\''s'")
        err = FileNotFoundError(errno.ENOENT, "No such file or directory", "/x/y")
        self.assertEqual(gotext.os_error("open", err), "open /x/y: no such file or directory")


class GoDecodeTest(unittest.TestCase):
    SPEC = gd.Struct({"id": gd.STR, "n": gd.INT, "ok": gd.BOOL, "tags": gd.List(gd.STR), "run": gd.Ptr(gd.Struct({"argv": gd.List(gd.STR)}))})

    def test_zero_values_and_null(self) -> None:
        v, err = gd.decode({"id": None, "tags": None}, self.SPEC)
        self.assertIsNone(err)
        self.assertEqual(v, {"id": "", "n": 0, "ok": False, "tags": None, "run": None})

    def test_keys_match_ignoring_ascii_case(self) -> None:
        v, err = gd.decode({"ID": "a", "Run": {"ARGV": ["x"]}, "other": 1}, self.SPEC)
        self.assertIsNone(err)
        self.assertEqual((v["id"], v["run"]), ("a", {"argv": ["x"]}))

    def test_a_wrong_type_is_an_error_and_the_rest_is_read(self) -> None:
        v, err = gd.decode({"id": 7, "n": 2, "tags": ["a", 3, "b"]}, self.SPEC)
        self.assertIsNotNone(err)
        self.assertEqual((v["id"], v["n"], v["tags"]), ("", 2, ["a", "", "b"]))
        for bad in (1.0, 1e3, True, "1"):
            _, err = gd.decode({"n": bad}, self.SPEC)
            self.assertIsNotNone(err, bad)
        _, err = gd.decode([1], self.SPEC)
        self.assertIsNotNone(err)


if __name__ == "__main__":
    unittest.main()
