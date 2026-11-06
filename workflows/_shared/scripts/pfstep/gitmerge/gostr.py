"""Go's string helpers, as the merge mechanics' messages and parsers use
them: ``strings.Fields`` and ``strings.TrimSpace`` (Unicode white space, a
byte that is not UTF-8 never one), ``strconv.Quote`` (``%q``), and the
short commit name of a message.

They take bytes where Go's input is a byte string that need not be UTF-8
(a conf line, a projection row), and text otherwise.
"""

from typing import List, Tuple, Union

# Go's unicode.IsSpace: the Latin-1 spaces and the White_Space property.
_SPACE = frozenset(
    [0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0x85, 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000]
    + list(range(0x2000, 0x200B))
)

RUNE_ERROR = 0xFFFD


def is_space(r: int) -> bool:
    """unicode.IsSpace."""
    return r in _SPACE


def decode_rune(b: bytes, i: int) -> Tuple[int, int]:
    """utf8.DecodeRune of b[i:]: the rune and its width; an invalid byte is
    (RUNE_ERROR, 1)."""
    c = b[i]
    if c < 0x80:
        return c, 1
    if 0xC2 <= c <= 0xDF:
        n = 2
    elif 0xE0 <= c <= 0xEF:
        n = 3
    elif 0xF0 <= c <= 0xF4:
        n = 4
    else:
        return RUNE_ERROR, 1
    chunk = b[i:i + n]
    if len(chunk) < n:
        return RUNE_ERROR, 1
    try:
        s = chunk.decode("utf-8")
    except UnicodeDecodeError:
        return RUNE_ERROR, 1
    return ord(s), n


def runes(b: bytes) -> List[Tuple[int, int, int]]:
    """Every rune of b as (rune, start, width), invalid bytes as RUNE_ERROR."""
    out = []
    i = 0
    while i < len(b):
        r, w = decode_rune(b, i)
        out.append((r, i, w))
        i += w
    return out


def _is_space_at(b: bytes, r: int, w: int) -> bool:
    return not (r == RUNE_ERROR and w == 1) and is_space(r)


def fields(b: bytes) -> List[bytes]:
    """strings.Fields: b split around runs of white space."""
    out = []
    start = -1
    for r, i, w in runes(b):
        if _is_space_at(b, r, w):
            if start >= 0:
                out.append(b[start:i])
                start = -1
        elif start < 0:
            start = i
    if start >= 0:
        out.append(b[start:])
    return out


def trim_space(b: bytes) -> bytes:
    """strings.TrimSpace: b without its leading and trailing white space."""
    rs = runes(b)
    lo = 0
    while lo < len(rs) and _is_space_at(b, rs[lo][0], rs[lo][2]):
        lo += 1
    hi = len(rs)
    while hi > lo and _is_space_at(b, rs[hi - 1][0], rs[hi - 1][2]):
        hi -= 1
    if lo >= hi:
        return b""
    return b[rs[lo][1]:rs[hi - 1][1] + rs[hi - 1][2]]


def _printable(r: int) -> bool:
    # strconv.IsPrint: letters, marks, numbers, punctuation, symbols and the
    # ASCII space — Python's str.isprintable() rule
    return chr(r).isprintable()


_HEX = "0123456789abcdef"


def quote(s: Union[str, bytes]) -> str:
    """strconv.Quote (Go's %q of a string)."""
    b = s.encode("utf-8", "surrogateescape") if isinstance(s, str) else s
    out = ['"']
    for r, i, w in runes(b):
        if w == 1 and r == RUNE_ERROR:
            out.append("\\x" + _HEX[b[i] >> 4] + _HEX[b[i] & 0xF])
            continue
        if r == 0x22 or r == 0x5C:
            out.append("\\" + chr(r))
        elif _printable(r):
            out.append(chr(r))
        elif r == 0x07:
            out.append("\\a")
        elif r == 0x08:
            out.append("\\b")
        elif r == 0x0C:
            out.append("\\f")
        elif r == 0x0A:
            out.append("\\n")
        elif r == 0x0D:
            out.append("\\r")
        elif r == 0x09:
            out.append("\\t")
        elif r == 0x0B:
            out.append("\\v")
        elif r < 0x20 or r == 0x7F:
            out.append("\\x" + _HEX[r >> 4] + _HEX[r & 0xF])
        elif r < 0x10000:
            out.append("\\u%04x" % r)
        else:
            out.append("\\U%08x" % r)
    out.append('"')
    return "".join(out)


def short(s: str) -> str:
    """A commit name shortened for a message (its first 12 characters)."""
    return s[:12] if len(s) > 12 else s


def or_text(s: str, alt: str) -> str:
    """s, or alt when s is blank."""
    return alt if trim_space(s.encode("utf-8", "surrogateescape")) == b"" else s


def trim_text(s: str) -> str:
    """strings.TrimSpace of a text."""
    return trim_space(s.encode("utf-8", "surrogateescape")).decode("utf-8", "surrogateescape")


def os_error(op: str, path: str, err: OSError) -> str:
    """An OS error as Go's *PathError words it: ``<op> <path>: <reason>``."""
    why = err.strerror or str(err)
    return "%s %s: %s" % (op, path, why[:1].lower() + why[1:])
