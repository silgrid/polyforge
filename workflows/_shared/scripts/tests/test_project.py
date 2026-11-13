"""pfstep.gitmerge.project: a file as the projection publishes it, the
list's rows interpreted in-process — never run — inside a closed grammar,
bounded, refusing every form sed and the interpreter would read apart; and
the Go interpreter's answers (tests/testdata/project_go.json, frozen
before the Go merge mechanics were deleted), byte for byte, refusals word
for word."""

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import unittest

from pfstep.gitmerge import project
from pfstep.gitmerge.ctx import Ctx, CtxError

TESTDATA = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata")
ROOT = os.path.abspath(os.path.join(os.path.dirname(TESTDATA), "..", "..", "..", ".."))

GRAMMAR_LIST = (
    "# list\n<!-- s/never/read/g\n## REWRITE\n-->\n## REWRITE\n\n"
    "<!-- a comment row: s/a/X/g -->\n"
    "s/ticket#([0-9]+)/task#\\1/g\n"
    "s|ORG/x|pub/x|g\n"
    "/@[Tt]eam/{s/@team/@acme/g;s/@Team/@Acme/g}\n"
    "/^drop me/d\n"
    "s/(^|[^A-Za-z0-9])tk([0-9]+)/\\1t\\2/g\n"
    "s/one/&-&/\n"
    "s/\\(paren\\)/[p]/g\n"
    "s/[[:space:]]+$//\n"
    "s/x\\/y/x|y/g\n"
    "\n## RENAME\ns/never/applied/\n"
)
GRAMMAR_INPUT = (
    "ticket#12 and ticket#3\nORG/x ORG/x\n@team @Team\ndrop me now\ntk7 xtk8 (tk9\none one\n(paren)  \nx/y\n"
    "<!-- fork-only:begin -->\nprivate\n  <!-- fork-only:end -->\t\n  last line without newline"
)


def proj(lst: bytes, content: bytes, ctx: Ctx = Ctx()) -> bytes:
    return project.project(ctx, lst, content)


class GrammarTest(unittest.TestCase):
    def test_everything_but_text_rows_is_refused(self) -> None:
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, True)
        marker = os.path.join(tmp, "pwned")
        for row in [
            "s|.*|touch " + marker + "|e", "s/a/b/ge", "e touch " + marker, "1e touch " + marker, "r /etc/passwd",
            "/x/r /etc/passwd", "R /etc/passwd", "s/a/b/w " + marker, "w " + marker, "/x/W " + marker, "s/a/b/p",
            "s/a/b/2", "s/a/b/I", "y/abc/xyz/", "/x/{s/a/b/;e touch " + marker + "}", "/x/{s/a/b/;w " + marker + "}",
            "s/(a)/\\U\\1/", "s/a/x\\ny/", "s/(a)\\1/b/", "s/\\<a\\>/b/", "s/[\\.]/b/", "s/a\\w/b/", "# a comment", "p", "s/a/b",
        ]:
            with self.subTest(row=row), self.assertRaises(project.RowRefused):
                proj(("## REWRITE\n\n" + row + "\n").encode(), b"a x\n")
        self.assertFalse(os.path.exists(marker), "a refused row ran")
        want = "task#12 and task#3\npub/x pub/x\n@acme @Acme\nt7 xtk8 (t9\none-one one\n[p]\nx|y\n  last line without newline\n"
        self.assertEqual(proj(GRAMMAR_LIST.encode(), GRAMMAR_INPUT.encode()).decode(), want)

    def test_forms_go_and_sed_read_apart_are_refused(self) -> None:
        for row, line in [
            ("/foo/s//X/", b"foo"), ("s//X/", b"foo"), ("//d", b"foo"), ("s/a{,2}/X/", b"aa"), ("s/a{2/X/", b"aa"),
            ("s/a{x}/X/", b"a{x}"), ("s/(a)/\\2/", b"a"), ("s/a/\\1/", b"a"), ("s/(?i)a/X/", b"A"),
            ("s/[[:^alpha:]]/X/", b"1"), ("s/[[=a=]]/X/", b"a"), ("s/[[.a.]]/X/", b"a"),
            ("s/[[:space:]]+$//", "a　".encode()), ("s/[^[:space:]]/X/", "　".encode()), ("/[[:space:]]/d", "a\u00a0b".encode()),
            ("s/[[:alpha:]]+/X/", "é".encode()), ("s/[[:punct:]]/X/", "a—b".encode()), ("s/a/b/", b"a\xffb"),
        ]:
            with self.subTest(row=row, line=line), self.assertRaises(project.RowRefused):
                proj(("## REWRITE\n" + row + "\n").encode(), line + b"\n")
        with self.assertRaises(project.RowRefused):
            proj(b"## REWRITE\ns/\xff/b/\n", b"a\n")
        for row, text, want in [
            ("s/a{2}/X/", "aaa", "Xa"), ("s/a{1,}/X/", "aaa", "X"), ("s/(a)(b)/\\2\\1/", "ab", "ba"),
            ("s/[[:space:]]+$//", "é —  ", "é —"), ("s/[[:alpha:]]+/X/", "ab—", "X—"), ("s/\\{x\\}/y/", "{x}", "y"),
        ]:
            with self.subTest(row=row, line=text):
                self.assertEqual(proj(("## REWRITE\n" + row + "\n").encode(), (text + "\n").encode()).decode(), want + "\n")
        self.assertEqual(proj(b"## REWRITE\n", b"a\xffb\n"), b"a\xffb\n", "no row: invalid UTF-8 is only copied")

    def test_the_projection_is_bounded(self) -> None:
        def amp(n: int) -> bytes:
            return ("## REWRITE\n" + "s/.*/&&&&&&&&/\n" * n).encode()

        self.assertEqual(len(proj(amp(4), b"abcdef\n")), 6 * 8 ** 4 + 1)
        for name, lst, content in [
            ("12-row amplification", amp(12), b"abcdef\n"),
            ("6-row amplification", amp(6), b"abcdef\n"),
            ("100k rows", ("## REWRITE\n" + "s/a/b/\n" * 100000).encode(), b"a\n"),
            ("a long row", ("## REWRITE\ns/a/" + "b" * 5000 + "/\n").encode(), b"a\n"),
            ("1001 commands", ("## REWRITE\n" + "/x/{s/a/b/;s/a/b/;s/a/b/;s/a/b/;s/a/b/;s/a/b/;s/a/b/}\n" * 143).encode(), b"x\n"),
            ("empty matches, global", ("## REWRITE\ns/q*/" + "X" * 4000 + "/g\n").encode(), b"a" * 1000 + b"\n"),
            ("a long line", b"## REWRITE\ns/a/b/\n", b"a" * 70000 + b"\n"),
        ]:
            with self.subTest(name), self.assertRaises(project.RowRefused):
                proj(lst, content)
        self.assertEqual(len(proj(b"## REWRITE\n", b"a" * 70000 + b"\n")), 70001)
        done = Ctx()
        done.cancel()
        with self.assertRaises(CtxError) as cm:
            proj(amp(1), b"a\n", done)
        self.assertNotIsInstance(cm.exception, project.RowRefused)

    def test_rewrite_rows_skip_comments_and_other_sections(self) -> None:
        self.assertEqual(project.rewrite_rows(GRAMMAR_LIST.encode())[0], b"s/ticket#([0-9]+)/task#\\1/g")
        self.assertNotIn(b"s/never/applied/", project.rewrite_rows(GRAMMAR_LIST.encode()))


class GoCorpusTest(unittest.TestCase):
    def test_every_projection_is_go_s(self) -> None:
        with open(os.path.join(TESTDATA, "project_go.json"), encoding="utf-8") as f:
            cases = json.load(f)["cases"]
        self.assertGreater(len(cases), 200)
        for i, c in enumerate(cases):
            with self.subTest(case=i, list=c["list"]):
                try:
                    out = proj(c["list"].encode(), c["content"].encode())
                except project.RowRefused as err:
                    self.assertEqual(str(err), c.get("err"))
                    continue
                self.assertEqual(out.decode(), c.get("out"))


@unittest.skipUnless(shutil.which("bash") and subprocess.run(["sh", "-c", "echo a | sed --sandbox -E s/a/b/"],
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0, "no GNU sed with --sandbox")
class ProjshaTest(unittest.TestCase):
    """The in-process projection is byte for byte what the sync tooling's
    projsha.sh (awk and sed) publishes, where the tree carries it."""

    def projsha(self, script: str, lst: str, f: str) -> str:
        out = subprocess.run(["bash", script, lst, f], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=True)
        return out.stdout.decode().strip()

    def test_matches_projsha(self) -> None:
        script = os.path.join(ROOT, "tools", "sync", "projsha.sh")
        list_path = os.path.join(ROOT, "docs", "sync", "desensitize.md")
        if not (os.path.isfile(script) and os.path.isfile(list_path)):
            self.skipTest("no projection tooling in this tree")
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, True)
        sl, si = os.path.join(tmp, "list.md"), os.path.join(tmp, "in.md")
        with open(sl, "w", encoding="utf-8") as f:
            f.write(GRAMMAR_LIST)
        with open(si, "w", encoding="utf-8") as f:
            f.write(GRAMMAR_INPUT)
        self.assertEqual(project.projected_sha(Ctx(), GRAMMAR_LIST.encode(), GRAMMAR_INPUT.encode()), self.projsha(script, sl, si))
        with open(list_path, "rb") as f:
            lst = f.read()
        files = [os.path.join(ROOT, "docs", "upgrade.md"), os.path.join(ROOT, "AGENTS.md")]
        for sub in ("docs", "skills"):
            for d, _, names in os.walk(os.path.join(ROOT, sub)):
                files += [os.path.join(d, n) for n in sorted(names) if n.endswith(".md")]
        self.assertGreater(len(files), 20)
        for path in files[:60]:
            with self.subTest(path=path):
                with open(path, "rb") as f:
                    raw = f.read()
                self.assertEqual(hashlib.sha256(proj(lst, raw)).hexdigest(), self.projsha(script, list_path, path))


if __name__ == "__main__":
    unittest.main()
