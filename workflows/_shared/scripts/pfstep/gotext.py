"""Text the way the step commands' Go side handled it.

The scripts took over commands written in Go, and their results, messages
and file reads keep that behaviour where Python's own differs:

- whitespace is Go's ``unicode.IsSpace`` (``trim_space``, ``fields``):
  Python's ``str.strip()`` / ``str.split()`` also take the ASCII
  separators U+001C..U+001F, which Go does not;
- ``quote`` is ``strconv.Quote`` (``%q``), ``quote_list`` a ``%q`` of a
  string slice (``["a" "b"]``);
- ``clip_text`` cuts at a byte bound of the UTF-8 encoding, on a character
  boundary (Go strings are bytes); ``clip_runes`` at a character count;
- ``path_clean`` / ``path_base`` / ``path_dir`` / ``path_ext`` are Go's
  ``path`` package (``posixpath`` keeps a leading ``//``, answers ``""`` for
  the base of ``a/``, and so on);
- ``shell_join`` renders an argv as one bash line, a word that is not bare
  single-quoted.
"""

import re
from typing import List, Sequence

# Go's unicode.IsSpace: the Latin-1 spaces and the White_Space property.
SPACE = (
    "\t\n\v\f\r \x85\xa0\u1680"
    "\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a"
    "\u2028\u2029\u202f\u205f\u3000"
)
_SPACE_SET = frozenset(SPACE)


def is_space(c: str) -> bool:
    return c in _SPACE_SET


def trim_space(s: str) -> str:
    """strings.TrimSpace."""
    return s.strip(SPACE)


def fields(s: str) -> List[str]:
    """strings.Fields: the runs of non-space characters."""
    out: List[str] = []
    cur: List[str] = []
    for c in s:
        if c in _SPACE_SET:
            if cur:
                out.append("".join(cur))
                cur = []
        else:
            cur.append(c)
    if cur:
        out.append("".join(cur))
    return out


def _printable(c: str) -> bool:
    # Go's strconv.IsPrint: letters, marks, numbers, punctuation, symbols
    # and the ASCII space; Python's isprintable() is the same set.
    return c == " " or c.isprintable()


def quote(s: str) -> str:
    """strconv.Quote: a double-quoted Go string literal."""
    out = ['"']
    for c in s:
        o = ord(c)
        if c == '"':
            out.append('\\"')
        elif c == "\\":
            out.append("\\\\")
        elif 0xD800 <= o <= 0xDFFF:
            out.append("\\ufffd")
        elif _printable(c):
            out.append(c)
        elif c == "\a":
            out.append("\\a")
        elif c == "\b":
            out.append("\\b")
        elif c == "\f":
            out.append("\\f")
        elif c == "\n":
            out.append("\\n")
        elif c == "\r":
            out.append("\\r")
        elif c == "\t":
            out.append("\\t")
        elif c == "\v":
            out.append("\\v")
        elif o < 0x20 or o == 0x7F:
            out.append("\\x%02x" % o)
        elif o < 0x10000:
            out.append("\\u%04x" % o)
        else:
            out.append("\\U%08x" % o)
    out.append('"')
    return "".join(out)


def quote_list(xs: Sequence[str]) -> str:
    """fmt's %q of a []string: ``["a" "b"]``."""
    return "[" + " ".join(quote(x) for x in xs) + "]"


def _lower1(c: str) -> str:
    # unicode.ToLower of one character: always one character (Python's
    # lower() of U+0130 is two)
    if c == "\u0130":
        return "i"
    lo = c.lower()
    return lo if len(lo) == 1 else c


def _upper1(c: str) -> str:
    up = c.upper()
    return up if len(up) == 1 else c


def to_lower(s: str) -> str:
    """strings.ToLower."""
    return "".join(_lower1(c) for c in s)


def equal_fold(a: str, b: str) -> bool:
    """strings.EqualFold: equal under simple case folding, character for
    character (Python's casefold() expands, Go's does not)."""
    if len(a) != len(b):
        return False
    for x, y in zip(a, b):
        if x == y:
            continue
        if _lower1(x) == _lower1(y) or _upper1(x) == _upper1(y):
            continue
        return False
    return True


def os_error(op: str, err: OSError) -> str:
    """An OSError as Go words a *PathError: "<op> <path>: <the errno's
    text>" (Go's errno texts are Python's with a lower-case first letter)."""
    msg = err.strerror or str(err)
    msg = msg[:1].lower() + msg[1:]
    if err.filename is None:
        return msg
    return "%s %s: %s" % (op, err.filename, msg)


# MAX_QUOTED_TEXT bounds agent-written text quoted into a message (bytes).
MAX_QUOTED_TEXT = 256

TRUNCATED_RUNES = "… (truncated; the full output is in the step's archived attempt)"


def clip_text(s: str, limit: int = MAX_QUOTED_TEXT) -> str:
    """s cut to limit bytes of its UTF-8 encoding, on a character boundary,
    the cut marked."""
    raw = s.encode("utf-8", "surrogatepass")
    if len(raw) <= limit:
        return s
    cut = limit
    while cut > 0 and (raw[cut] & 0xC0) == 0x80:
        cut -= 1
    return raw[:cut].decode("utf-8", "replace") + "...(truncated)"


def clip_runes(s: str, limit: int) -> str:
    """s cut to limit characters, the cut marked."""
    if len(s) > limit:
        return s[:limit] + TRUNCATED_RUNES
    return s


def clip_list(xs: Sequence[str], n: int) -> List[str]:
    """The first n entries, the rest counted."""
    if len(xs) <= n:
        return list(xs)
    return list(xs[:n]) + ["(+%d more)" % (len(xs) - n)]


def short_sha(s: str) -> str:
    """A commit name shortened for a message (12 characters)."""
    return s[:12] if len(s) > 12 else s


def path_clean(p: str) -> str:
    """path.Clean."""
    if p == "":
        return "."
    rooted = p.startswith("/")
    out: List[str] = []
    for seg in p.split("/"):
        if seg == "" or seg == ".":
            continue
        if seg == "..":
            if out and out[-1] != "..":
                out.pop()
            elif not rooted:
                out.append("..")
            continue
        out.append(seg)
    s = "/".join(out)
    if rooted:
        return "/" + s
    return s or "."


def path_base(p: str) -> str:
    """path.Base."""
    if p == "":
        return "."
    p = p.rstrip("/")
    if p == "":
        return "/"
    i = p.rfind("/")
    return p[i + 1 :] if i >= 0 else p


def path_dir(p: str) -> str:
    """path.Dir."""
    i = p.rfind("/")
    return path_clean(p[: i + 1])


def path_ext(p: str) -> str:
    """path.Ext: the suffix from the final element's last dot."""
    i = len(p) - 1
    while i >= 0 and p[i] != "/":
        if p[i] == ".":
            return p[i:]
        i -= 1
    return ""


_BARE_WORD = re.compile(r"[A-Za-z0-9_@%+=:,./-]+", re.ASCII)


def shell_join(argv: Sequence[str]) -> str:
    """An argv as one bash line, each word that is not bare single-quoted."""
    words = []
    for w in argv:
        if _BARE_WORD.fullmatch(w):
            words.append(w)
        else:
            words.append("'" + w.replace("'", "'\\''") + "'")
    return " ".join(words)
