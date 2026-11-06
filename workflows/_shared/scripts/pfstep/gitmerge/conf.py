"""A repository's integration conf, ``.polyforge/integrate.conf``: the
files whose merge conflicts the lane resolves mechanically.

::

    append_only=<path>                 a file every change only appends to
                                       (docs/upgrade.md): a conflict is
                                       resolved as the default branch's file
                                       followed by the branch's appended bytes
    pin=<pinned path> <holder path> [projected <list path>]
                                       <holder> carries a hash of <pinned>:
                                       once <pinned> was resolved, every
                                       occurrence of either side's hash in
                                       <holder> becomes the resolved file's.
                                       The hash is the file's sha256, or —
                                       projected — the sha256 of the file as
                                       the projection publishes it under
                                       <list>'s REWRITE rows (project.py),
                                       computed in-process: no pin runs a
                                       command, and its inputs are untrusted
    list=<holder path> <comment prefix> <begin marker> <end marker>
                                       <holder> keeps a list of whole comment
                                       lines between the line `<comment
                                       prefix> <begin marker>` and the line
                                       `<comment prefix> <end marker>`, each
                                       change appending one line at its end:
                                       a hunk inside that region where both
                                       sides only inserted whole `<comment
                                       prefix> ` lines at the same point takes
                                       the default branch's lines then the
                                       branch's

A pin or list holder's conflict is settled hunk by hunk and only by those
two rules (resolve.py); every other hunk is a real conflict. ``#`` lines,
blank lines and HTML comment lines (``<!-- … -->``) are ignored. A missing
conf declares nothing: every conflict is a real one.

Paths are text (a byte that is not UTF-8 kept as a surrogate escape, so
it reaches git and the file system unchanged); a list's prefix and markers
are bytes, compared with a file's lines.
"""

from dataclasses import dataclass, field
from typing import List

from . import gostr
from .ctx import GoError

CONF_REL = ".polyforge/integrate.conf"

# bufio.Scanner's token limit: a longer line ends the parse with its error
_MAX_TOKEN = 64 * 1024


@dataclass(frozen=True)
class ListDecl:
    """One list= line: the region of a holder whose lines are a list every
    change appends one line to."""

    holder: str
    prefix: bytes
    begin: bytes
    end: bytes

    def marker(self, line: bytes, m: bytes) -> bool:
        """Whether line is the list's marker line m."""
        return gostr.trim_space(line) == self.prefix + b" " + m

    def markers(self, lines: List[bytes]) -> bool:
        """Whether any of lines is one of the list's markers."""
        return any(self.marker(x, self.begin) or self.marker(x, self.end) for x in lines)

    def entry(self, line: bytes) -> bool:
        """Whether line is one of the list's lines: the comment prefix and a
        space, then text, and no marker."""
        t = gostr.trim_space(line)
        return t.startswith(self.prefix + b" ") and not self.marker(line, self.begin) and not self.marker(line, self.end)


@dataclass(frozen=True)
class Pin:
    """One pin= line; list is a projected pin's projection list ("" = the
    sha256 of the pinned file's bytes)."""

    file: str
    holder: str
    list: str = ""


@dataclass
class Conf:
    append_only: List[str] = field(default_factory=list)
    pins: List[Pin] = field(default_factory=list)
    lists: List[ListDecl] = field(default_factory=list)

    def is_append_only(self, p: str) -> bool:
        return p in self.append_only

    def holder(self, p: str) -> bool:
        """Whether p carries a pin or a list."""
        return any(pin.holder == p for pin in self.pins) or bool(self.lists_of(p))

    def lists_of(self, p: str) -> List[ListDecl]:
        return [lst for lst in self.lists if lst.holder == p]


def go_clean(p: str) -> str:
    """Go's filepath.Clean (slash paths)."""
    if p == "":
        return "."
    rooted = p.startswith("/")
    out: List[str] = []
    for e in p.split("/"):
        if e in ("", "."):
            continue
        if e == "..":
            if out and out[-1] != "..":
                out.pop()
            elif not rooted:
                out.append("..")
            continue
        out.append(e)
    s = "/".join(out)
    if rooted:
        s = "/" + s
    return s or "."


def _text(b: bytes) -> str:
    return b.decode("utf-8", "surrogateescape")


def clean_path(p: bytes) -> str:
    s = _text(p)
    c = go_clean(s)
    if s == "" or s.startswith("/") or c == "." or c.startswith("../"):
        raise GoError("%s is not a repository-relative path" % gostr.quote(p))
    return c


def parse_conf(raw: bytes) -> Conf:
    """The conf from the bytes a tree holds (b"" = none declared)."""
    c = Conf()
    lines = raw.split(b"\n")
    if lines and lines[-1] == b"":
        lines.pop()
    for n, line in enumerate(lines, 1):
        if len(line) >= _MAX_TOKEN:
            raise GoError("bufio.Scanner: token too long")
        if line.endswith(b"\r"):
            line = line[:-1]
        line = gostr.trim_space(line)
        if line == b"" or line.startswith(b"#") or line.startswith(b"<!--"):
            continue
        key, sep, val = line.partition(b"=")
        if not sep:
            raise GoError("%s:%d: want key=value, got %s" % (CONF_REL, n, gostr.quote(line)))
        val = gostr.trim_space(val)
        k = gostr.trim_space(key)
        if k == b"append_only":
            try:
                c.append_only.append(clean_path(val))
            except GoError as err:
                raise err.wrap("%s:%d" % (CONF_REL, n)) from None
        elif k == b"pin":
            f = gostr.fields(val)
            if (len(f) != 2 and len(f) != 4) or (len(f) == 4 and f[2] != b"projected"):
                raise GoError(
                    "%s:%d: pin= wants <pinned path> <holder path> [projected <list path>] (a pin's hash is computed"
                    " in-process; no pin runs a command)" % (CONF_REL, n)
                )
            words = f[:2] + ([f[3]] if len(f) == 4 else [])
            paths = []
            for w in words:
                try:
                    paths.append(clean_path(w))
                except GoError as err:
                    raise err.wrap("%s:%d" % (CONF_REL, n)) from None
            c.pins.append(Pin(file=paths[0], holder=paths[1], list=paths[2] if len(paths) == 3 else ""))
        elif k == b"list":
            f = gostr.fields(val)
            if len(f) != 4 or f[2] == f[3]:
                raise GoError(
                    "%s:%d: list= wants <holder path> <comment prefix> <begin marker> <end marker> (two different"
                    " markers)" % (CONF_REL, n)
                )
            try:
                holder = clean_path(f[0])
            except GoError as err:
                raise err.wrap("%s:%d" % (CONF_REL, n)) from None
            c.lists.append(ListDecl(holder=holder, prefix=f[1], begin=f[2], end=f[3]))
        else:
            raise GoError("%s:%d: unknown key %s" % (CONF_REL, n, gostr.quote(key)))
    return c
