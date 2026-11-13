"""Go's string helpers, where Python's differ and a message or a parse
depends on the difference.

- ``fields`` / ``trim_space``: Go's unicode.IsSpace set (Python's str.split
  and str.strip also take U+001C..U+001F);
- ``cap_text``: a cut at a byte length on a UTF-8 rune boundary, marked;
- ``quote``: strconv.Quote (fmt's %q) — a printable rune as is, the C
  escapes, any other control as \\xNN, \\uNNNN or \\UNNNNNNNN;
- ``SPACE``: RE2's ``\\s`` as a character class (Python's ``\\s`` also
  takes a vertical tab).
"""

import posixpath
import re
import unicodedata
from typing import List

# RE2's \s: [\t\n\f\r ] (no vertical tab).
SPACE = r"[\t\n\f\r ]"

# Go's unicode.IsSpace.
_GO_SPACE = "\t\n\v\f\r \x85\xa0                　"
_FIELDS = re.compile("[" + re.escape(_GO_SPACE) + "]+")

# The marker a cut text ends with.
CUT_MARK = " …[cut]"


def fields(s: str) -> List[str]:
    """strings.Fields: s split around runs of Go white space."""
    return [f for f in _FIELDS.split(s) if f]


def trim_space(s: str) -> str:
    """strings.TrimSpace."""
    return s.strip(_GO_SPACE)


def trim_left_space(s: str) -> str:
    """strings.TrimLeft(s, " \\t") — the indentation helper's cutset."""
    return s.lstrip(" \t")


def cap_text(s: str, limit: int) -> str:
    """s cut to limit UTF-8 bytes on a rune boundary, marking the cut."""
    raw = s.encode("utf-8", "surrogatepass")
    if len(raw) <= limit:
        return s
    cut = limit - len(CUT_MARK.encode("utf-8"))
    while cut > 0 and (raw[cut] & 0xC0) == 0x80:
        cut -= 1
    return raw[:cut].decode("utf-8", "replace") + CUT_MARK


_ESC = {"\a": "\\a", "\b": "\\b", "\f": "\\f", "\n": "\\n", "\r": "\\r", "\t": "\\t", "\v": "\\v", "\\": "\\\\", '"': '\\"'}


def _printable(c: str) -> bool:
    if c == " ":
        return True
    return unicodedata.category(c)[0] in "LMNPS"


def quote(s: str) -> str:
    """strconv.Quote(s)."""
    out = ['"']
    for c in s:
        if c in _ESC:
            out.append(_ESC[c])
        elif _printable(c):
            out.append(c)
        else:
            o = ord(c)
            if o < 0x80:
                out.append("\\x%02x" % o)
            elif o < 0x10000:
                out.append("\\u%04x" % o)
            else:
                out.append("\\U%08x" % o)
    out.append('"')
    return "".join(out)


# ---- Go's path package (slash paths) ------------------------------------


def path_clean(p: str) -> str:
    """path.Clean."""
    if p == "":
        return "."
    out = posixpath.normpath(p)
    if out.startswith("//"):
        out = "/" + out.lstrip("/")
    return out


def path_dir(p: str) -> str:
    """path.Dir: all but the last element, cleaned ("." for none)."""
    return path_clean(p[: p.rfind("/") + 1])


def path_join(*elems: str) -> str:
    """path.Join: the non-empty elements joined by / and cleaned ("" for none)."""
    parts = [e for e in elems if e != ""]
    if not parts:
        return ""
    return path_clean("/".join(parts))


def clean_rel(p: str) -> str:
    """One repo-relative path normalized ("" when it is not one)."""
    p = trim_space(p)
    if p == "":
        return ""
    c = path_clean(p)
    if c.startswith("/") or c in (".", "..") or c.startswith("../"):
        return ""
    return c


_META = "\\.+*?()|[]{}^$"


def quote_meta(s: str) -> str:
    """regexp.QuoteMeta: every RE2 metacharacter escaped."""
    return "".join("\\" + c if c in _META else c for c in s)
