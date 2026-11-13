"""The glob syntaxes a step script matches paths with, as the Go side did.

Three of them, and none is ``fnmatch``:

- ``path_match`` is Go's ``path.Match`` (written out, byte for byte):
  ``*`` and ``?`` never match ``/``, ``[...]`` a class negated by ``^``,
  ``\\`` escapes, and a malformed pattern matches nothing (``BadPattern``).
  ``micro_glob_match`` builds the micro lane's ``micro_path=`` grammar on
  it: segment by segment, ``**`` zero or more whole segments, any other
  segment a ``path.Match`` pattern, so ``*`` never crosses ``/``.
- ``case_match`` is the scope engine's glob (``carries:``, ``suite_glob=``,
  ``exclude=``, ``forced_full=``): shell ``case`` matching, ``*`` crossing
  ``/``, ``?`` one character, ``[...]`` a class (``[!...]`` negated), ``\\``
  escaping the next character. ``test_glob_match`` is its twin without the
  escape, the test-file classifier's (``is_test_file``).

The Go side built a regular expression of the glob and compiled it with
RE2; the two shell-case matchers here build the same expression text and
read it the way RE2 does (``_Re2``): a literal byte of the glob above
U+007F stands for the character of that code point (Go's
``string(byte)``), a class may hold a POSIX class (``[:alpha:]``), a Perl
class (``\\d \\s \\w``, ASCII) or an escape, ``$`` is the end of the text
(never before a final newline), and an expression RE2 refuses matches
nothing — as does a Unicode class (``\\p{..}``) in a glob's class, which
this reading does not carry (the Go side's classifier would have taken it).
"""

import re
import warnings
from typing import Dict, List, Optional, Pattern, Sequence, Tuple

from . import gotext


class BadPattern(ValueError):
    """path.ErrBadPattern."""


# ---------------------------------------------------------------------------
# path.Match


def _decode_rune(b: bytes, i: int) -> Tuple[int, int]:
    """utf8.DecodeRune of b[i:]: (rune, size); (0xFFFD, 1) for a bad byte."""
    c = b[i]
    if c < 0x80:
        return c, 1
    for n in (2, 3, 4):
        if i + n <= len(b):
            try:
                s = b[i : i + n].decode("utf-8")
            except UnicodeDecodeError:
                continue
            if len(s) == 1:
                return ord(s), n
    return 0xFFFD, 1


def _scan_chunk(p: bytes) -> Tuple[bool, bytes, bytes]:
    star = False
    while p and p[0:1] == b"*":
        p = p[1:]
        star = True
    inrange = False
    i = 0
    while i < len(p):
        c = p[i : i + 1]
        if c == b"\\":
            if i + 1 < len(p):
                i += 1
        elif c == b"[":
            inrange = True
        elif c == b"]":
            inrange = False
        elif c == b"*" and not inrange:
            return star, p[:i], p[i:]
        i += 1
    return star, p, b""


def _get_esc(chunk: bytes) -> Tuple[int, bytes]:
    if not chunk or chunk[0:1] in (b"-", b"]"):
        raise BadPattern("bad pattern")
    if chunk[0:1] == b"\\":
        chunk = chunk[1:]
        if not chunk:
            raise BadPattern("bad pattern")
    r, n = _decode_rune(chunk, 0)
    bad = r == 0xFFFD and n == 1 and chunk[0] >= 0x80
    rest = chunk[n:]
    if bad or not rest:
        raise BadPattern("bad pattern")
    return r, rest


def _match_chunk(chunk: bytes, s: bytes) -> Tuple[bytes, bool]:
    """(rest, ok); BadPattern for a malformed chunk."""
    failed = False
    while chunk:
        failed = failed or not s
        c = chunk[0:1]
        if c == b"[":
            r = 0
            if not failed:
                r, n = _decode_rune(s, 0)
                s = s[n:]
            chunk = chunk[1:]
            negated = False
            if chunk[0:1] == b"^":
                negated = True
                chunk = chunk[1:]
            match = False
            nrange = 0
            while True:
                if chunk[0:1] == b"]" and nrange > 0:
                    chunk = chunk[1:]
                    break
                lo, chunk = _get_esc(chunk)
                hi = lo
                if chunk[0:1] == b"-":
                    hi, chunk = _get_esc(chunk[1:])
                match = match or lo <= r <= hi
                nrange += 1
            failed = failed or match == negated
        elif c == b"?":
            if not failed:
                failed = s[0:1] == b"/"
                _, n = _decode_rune(s, 0)
                s = s[n:]
            chunk = chunk[1:]
        else:
            if c == b"\\":
                chunk = chunk[1:]
                if not chunk:
                    raise BadPattern("bad pattern")
            if not failed:
                failed = chunk[0] != s[0]
                s = s[1:]
            chunk = chunk[1:]
    if failed:
        return b"", False
    return s, True


def path_match(pattern: str, name: str) -> bool:
    """path.Match(pattern, name); BadPattern when the pattern is malformed."""
    pat = pattern.encode("utf-8", "surrogateescape")
    nm = name.encode("utf-8", "surrogateescape")
    while pat:
        star, chunk, pat = _scan_chunk(pat)
        if star and not chunk:
            return b"/" not in nm
        t, ok = b"", False
        err: Optional[BadPattern] = None
        try:
            t, ok = _match_chunk(chunk, nm)
        except BadPattern as e:
            err = e
        if ok and (not t or pat):
            nm = t
            continue
        if err is not None:
            raise err
        if star:
            matched = False
            i = 0
            while i < len(nm) and nm[i : i + 1] != b"/":
                t, ok = _match_chunk(chunk, nm[i + 1 :])
                if ok:
                    if not pat and t:
                        i += 1
                        continue
                    nm = t
                    matched = True
                    break
                i += 1
            if matched:
                continue
        while pat:
            _, chunk, pat = _scan_chunk(pat)
            _match_chunk(chunk, b"")
        return False
    return not nm


def micro_glob_match(pattern: str, p: str) -> bool:
    """A micro_path= pattern against a repo-relative path (the module doc)."""
    return _segments(pattern.split("/"), p.split("/"))


def _segments(ps: Sequence[str], xs: Sequence[str]) -> bool:
    if not ps:
        return not xs
    if ps[0] == "**":
        if _segments(ps[1:], xs):
            return True
        return len(xs) > 0 and _segments(ps, xs[1:])
    if not xs:
        return False
    try:
        ok = path_match(ps[0], xs[0])
    except BadPattern:
        return False
    return ok and _segments(ps[1:], xs[1:])


# ---------------------------------------------------------------------------
# The shell-case globs, through the regular expression RE2 would compile.

_QUOTE_META = set("\\.+*?()|[]{}^$")

# POSIX classes RE2 knows (ASCII).
_POSIX: Dict[str, List[Tuple[int, int]]] = {
    "alnum": [(0x30, 0x39), (0x41, 0x5A), (0x61, 0x7A)],
    "alpha": [(0x41, 0x5A), (0x61, 0x7A)],
    "ascii": [(0x00, 0x7F)],
    "blank": [(0x09, 0x09), (0x20, 0x20)],
    "cntrl": [(0x00, 0x1F), (0x7F, 0x7F)],
    "digit": [(0x30, 0x39)],
    "graph": [(0x21, 0x7E)],
    "lower": [(0x61, 0x7A)],
    "print": [(0x20, 0x7E)],
    "punct": [(0x21, 0x2F), (0x3A, 0x40), (0x5B, 0x60), (0x7B, 0x7E)],
    "space": [(0x09, 0x0D), (0x20, 0x20)],
    "upper": [(0x41, 0x5A)],
    "word": [(0x30, 0x39), (0x41, 0x5A), (0x5F, 0x5F), (0x61, 0x7A)],
    "xdigit": [(0x30, 0x39), (0x41, 0x46), (0x61, 0x66)],
}

# RE2's Perl classes (ASCII; \s has no vertical tab).
_PERL: Dict[str, List[Tuple[int, int]]] = {
    "d": [(0x30, 0x39)],
    "s": [(0x09, 0x0A), (0x0C, 0x0D), (0x20, 0x20)],
    "w": [(0x30, 0x39), (0x41, 0x5A), (0x5F, 0x5F), (0x61, 0x7A)],
}

_ESCAPES = {"a": 7, "f": 12, "t": 9, "n": 10, "r": 13, "v": 11}

_MAX_RUNE = 0x10FFFF


class _Refused(Exception):
    """An expression RE2 would not compile."""


def _complement(ranges: List[Tuple[int, int]]) -> List[Tuple[int, int]]:
    out = []
    nxt = 0
    for lo, hi in sorted(ranges):
        if lo > nxt:
            out.append((nxt, lo - 1))
        nxt = max(nxt, hi + 1)
    if nxt <= _MAX_RUNE:
        out.append((nxt, _MAX_RUNE))
    return out


def _class_text(ranges: List[Tuple[int, int]], negated: bool) -> str:
    if negated:
        ranges = _complement(ranges)
    if not ranges:
        return "(?!)"
    parts = []
    for lo, hi in ranges:
        if lo == hi:
            parts.append("\\U%08x" % lo)
        else:
            parts.append("\\U%08x-\\U%08x" % (lo, hi))
    return "[" + "".join(parts) + "]"


class _Re2:
    """The subset of RE2 syntax the glob translations produce, read into a
    Python expression of the same meaning."""

    def __init__(self, src: str) -> None:
        self.s = src
        self.i = 0

    def translate(self) -> str:
        out: List[str] = []
        s = self.s
        while self.i < len(s):
            c = s[self.i]
            if c == "^":
                out.append("\\A")
                self.i += 1
            elif c == "$":
                out.append("\\Z")
                self.i += 1
            elif c in ".*":
                out.append(c)
                self.i += 1
            elif c == "\\":
                if self.i + 1 >= len(s):
                    raise _Refused("trailing backslash")
                d = s[self.i + 1]
                if not (d.isascii() and not d.isalnum()):
                    raise _Refused("escape \\" + d)
                out.append(re.escape(d))
                self.i += 2
            elif c == "[":
                self.i += 1
                out.append(self._class())
            else:
                out.append(re.escape(c))
                self.i += 1
        return "".join(out)

    def _char(self) -> int:
        s = self.s
        if self.i >= len(s):
            raise _Refused("missing ]")
        c = s[self.i]
        if c != "\\":
            self.i += 1
            return ord(c)
        if self.i + 1 >= len(s):
            raise _Refused("trailing backslash")
        d = s[self.i + 1]
        self.i += 2
        if d in _ESCAPES:
            return _ESCAPES[d]
        if d.isascii() and not d.isalnum():
            return ord(d)
        if d == "x":
            # \xHH or \x{H...}
            if s.startswith("{", self.i):
                end = s.find("}", self.i)
                digits = s[self.i + 1 : end] if end > self.i else ""
                if end < 0 or not digits or any(c not in "0123456789abcdefABCDEF" for c in digits) or int(digits, 16) > _MAX_RUNE:
                    raise _Refused("bad \\x escape")
                self.i = end + 1
                return int(digits, 16)
            digits = s[self.i : self.i + 2]
            if len(digits) != 2 or any(c not in "0123456789abcdefABCDEF" for c in digits):
                raise _Refused("bad \\x escape")
            self.i += 2
            return int(digits, 16)
        if d in "01234567":
            # an octal escape: up to three digits
            j = self.i
            while j < len(s) and j < self.i + 2 and s[j] in "01234567":
                j += 1
            v = int(d + s[self.i : j], 8)
            self.i = j
            return v
        raise _Refused("escape \\" + d)

    def _class(self) -> str:
        s = self.s
        ranges: List[Tuple[int, int]] = []
        negated = False
        if self.i < len(s) and s[self.i] == "^":
            negated = True
            self.i += 1
        first = True
        while True:
            if self.i >= len(s):
                raise _Refused("missing ]")
            if s[self.i] == "]" and not first:
                self.i += 1
                break
            first = False
            if s.startswith("[:", self.i) and len(s) - self.i > 2:
                end = s.find(":]", self.i + 2)
                if end >= 0:
                    name = s[self.i + 2 : end]
                    neg = name.startswith("^")
                    group = _POSIX.get(name[1:] if neg else name)
                    if group is None:
                        raise _Refused("invalid POSIX class")
                    ranges.extend(_complement(group) if neg else group)
                    self.i = end + 2
                    continue
            if s[self.i] == "\\" and self.i + 1 < len(s):
                d = s[self.i + 1]
                if d.lower() in _PERL:
                    group = _PERL[d.lower()]
                    ranges.extend(_complement(group) if d.isupper() else group)
                    self.i += 2
                    continue
                if d in "pP":
                    raise _Refused("Unicode class")
            lo = self._char()
            hi = lo
            if self.i + 1 < len(s) and s[self.i] == "-" and s[self.i + 1] != "]":
                self.i += 1
                hi = self._char()
                if hi < lo:
                    raise _Refused("invalid range")
            ranges.append((lo, hi))
        return _class_text(ranges, negated)


_CACHE: Dict[str, Optional[Pattern[str]]] = {}


def _compile(go_re: str) -> Optional[Pattern[str]]:
    if go_re in _CACHE:
        return _CACHE[go_re]
    pat: Optional[Pattern[str]]
    try:
        text = _Re2(go_re).translate()
        with warnings.catch_warnings():
            warnings.simplefilter("ignore")
            pat = re.compile(text, re.ASCII)
    except (_Refused, re.error):
        pat = None
    _CACHE[go_re] = pat
    return pat


def _byte_literal(b: int) -> str:
    """regexp.QuoteMeta(string(b)) of one glob byte: the character of code
    point b, its meta escaped."""
    c = chr(b)
    return "\\" + c if c in _QUOTE_META else c


def _go_regex(glob: str, escapes: bool) -> str:
    raw = glob.encode("utf-8", "surrogateescape")
    out = ["^"]
    i = 0
    while i < len(raw):
        ch = raw[i]
        if ch == 0x2A:  # *
            out.append(".*")
        elif ch == 0x3F:  # ?
            out.append(".")
        elif ch == 0x5C and escapes:  # \
            if i + 1 < len(raw):
                i += 1
                out.append(_byte_literal(raw[i]))
        elif ch == 0x5B:  # [
            end = raw.find(b"]", i + 1)
            if end < 0:
                out.append("\\[")
                i += 1
                continue
            cls = raw[i + 1 : end].decode("utf-8", "surrogateescape")
            if cls.startswith("!"):
                cls = "^" + cls[1:]
            if escapes:
                cls = cls.replace("\\", "\\\\")
            out.append("[" + cls + "]")
            i = end
        else:
            out.append(_byte_literal(ch))
        i += 1
    out.append("$")
    return "".join(out)


def case_match(glob: str, p: str) -> bool:
    """The scope engine's glob (the module doc): facts' CarriesGlobMatch."""
    pat = _compile(_go_regex(glob, True))
    return pat is not None and pat.search(p) is not None


def test_glob_match(glob: str, p: str) -> bool:
    """The test-file classifier's glob: case_match without the backslash
    escape (a class's backslash reaches the expression as written)."""
    pat = _compile(_go_regex(glob, False))
    return pat is not None and pat.search(p) is not None


# ---------------------------------------------------------------------------
# The test-file classifier.

GENERIC_TEST_BASE = ("*_test.go", "*.test.*", "*.spec.*", "test_*.py", "*_test.py")


def is_test_file(p: str, globs: Sequence[str]) -> bool:
    """One repo-relative path a test file: a generic name pattern, a path
    under a test/ or tests/ directory, or a conf test_glob (against the
    path and its base name, ``*`` crossing ``/``)."""
    p = gotext.path_clean(p)
    if p.startswith("./"):
        p = p[2:]
    base = gotext.path_base(p)
    for g in GENERIC_TEST_BASE:
        if test_glob_match(g, base):
            return True
    parts = p.split("/")
    for d in parts[:-1]:
        if d in ("test", "tests"):
            return True
    for g in globs:
        if test_glob_match(g, p) or test_glob_match(g, base):
            return True
    return False
