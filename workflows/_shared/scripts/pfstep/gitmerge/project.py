"""A file's bytes as the projection publishes them, computed in-process.

A ``projected`` pin (conf.py) carries the sha256 of its file as
tools/sync/project.sh publishes it: every fork-only block dropped, then
every ``## REWRITE`` row of the projection list applied in order — the
order tools/sync/projsha.sh reproduces with awk and sed.

The list is untrusted input: the lane recomputes the pin on the list as
the merge resolved it, which carries the branch's rows. So no row is ever
handed to sed or any other program. A row is parsed here into a closed
grammar of pure text transformations and interpreted; anything outside it
refuses the projection (``RowRefused``), which leaves the pin
unrecomputable and its holder a real conflict. The grammar (GNU sed -E's
meaning, the forms the live list uses):

    row   := [ '/' ERE '/' ] cmd
    cmd   := subst | 'd' | '{' subst { ';' subst } [ ';' ] '}'
    subst := 's' D ERE D replacement D [ 'g' ]

D is one ASCII punctuation character (not ``\\``); the replacement takes
literal text, ``&``, ``\\0``–``\\9`` (a group the regex has), ``\\&``,
``\\\\`` and ``\\D``. Every other command, flag or escape — ``e``, ``r``,
``R``, ``w``, ``W``, ``p``, ``y``, a numeric or ``I``/``M`` flag,
``\\L``/``\\U`` case conversion, a ``\\n`` in a replacement, a backslash
inside a bracket expression, a GNU word-boundary or backreference escape in
a regex — is refused, never approximated. So is every form on which the
interpreter and sed would part: an empty regex (sed reuses the last one), a
``{`` that is not a whole ``{n}``, ``{n,}`` or ``{n,m}`` interval, a
reference to a group the regex does not have, a ``(?`` group or a
``[:^name:]`` class, an equivalence class or collating symbol, a row or a
line that is not valid UTF-8, and a POSIX class applied to a line carrying
a non-ASCII character the class may hold in a UTF-8 locale (the classes
here are ASCII only, glibc's are not). Regexes match leftmost-longest, as
sed's (posixre).

It is bounded: a list of more than MAX_ROWS rows, a row longer than
MAX_ROW_LEN bytes, more than MAX_COMMANDS commands in all, a pattern space
growing past MAX_LINE bytes or an output past ``output_limit`` (four times
the input and 1 MiB, at most MAX_OUTPUT) is refused before the bytes are
made. The deadline is checked on every line and command.

The Go twin was internal/polyforge/gitmerge/project.go; the messages are
its texts.
"""

import hashlib
import re
import unicodedata
from typing import List, Optional, Union

from . import gostr, posixre
from .ctx import Ctx

MAX_ROWS = 1000
MAX_ROW_LEN = 4 << 10
MAX_COMMANDS = 1000
MAX_LINE = 64 << 10
MAX_OUTPUT = 64 << 20

REFUSED = "a projection row outside the grammar the lane interprets"

# awk's [[:space:]]
AWK_SPACE = b" \t\n\v\f\r"

_INTERVAL = re.compile(rb"\{[0-9]+(,[0-9]*)?\}", re.ASCII)


class RowRefused(ValueError):
    """A projection row outside the grammar, a form the interpreter would
    read otherwise than sed, or a projection past its bounds (Go's
    ErrRowRefused, its message carried whole)."""


def refused(detail: str) -> RowRefused:
    return RowRefused(REFUSED + ": " + detail)


def _wrap(prefix: str, err: RowRefused) -> RowRefused:
    return RowRefused(prefix + ": " + str(err))


def output_limit(n: int) -> int:
    """The largest projection of an input of n bytes."""
    if n > (MAX_OUTPUT - (1 << 20)) // 4:
        return MAX_OUTPUT
    return 4 * n + (1 << 20)


def awk_lines(b: bytes) -> List[bytes]:
    """b split into awk's records (a final newline ends the last one; a
    final partial line is a record too)."""
    if not b:
        return []
    ls = b.split(b"\n")
    if ls[-1] == b"":
        ls.pop()
    return ls


def rewrite_rows(lst: bytes) -> List[bytes]:
    """The list's REWRITE rows as tools/sync/projsha.sh reads them: inside
    the section starting at the ``## REWRITE`` header, every non-blank line
    outside an HTML comment (``<!--`` through the next ``-->``, awk's range,
    which may start and end on one line)."""
    rows: List[bytes] = []
    inside = comment = False
    for line in awk_lines(lst):
        if not comment and b"<!--" in line:
            comment = True
        if comment:
            if b"-->" in line:
                comment = False
            continue
        if line.startswith(b"## "):
            f = gostr.fields(line)
            inside = len(f) > 1 and f[1] == b"REWRITE"
            continue
        if inside and gostr.trim_space(line) != b"":
            rows.append(line)
    return rows


def _valid_utf8(b: bytes) -> bool:
    try:
        b.decode("utf-8")
    except UnicodeDecodeError:
        return False
    return True


def _clip_row(r: bytes) -> bytes:
    if len(r) > 80:
        return r[:80] + "…".encode("utf-8")
    return r


def _is_punct(c: int) -> bool:
    return 0x20 < c < 0x7F and not (0x30 <= c <= 0x39) and not (0x61 <= c <= 0x7A) and not (0x41 <= c <= 0x5A)


def _ch(c: int) -> str:
    """Go's string(byte): the byte as the rune of its value."""
    return chr(c)


def _class_may(name: str, r: int) -> bool:
    """Whether a non-ASCII rune may belong to a POSIX class in some locale
    (a superset of glibc's: refusing too much makes a mechanical conflict a
    real one, too little would compute a wrong pin)."""
    cat = unicodedata.category(chr(r))
    if name in ("alpha", "alnum", "upper", "lower", "digit", "xdigit"):
        return cat[0] in ("L", "N", "M")
    if name in ("space", "blank"):
        return gostr.is_space(r) or cat in ("Zs", "Zl", "Zp") or r in (0x180E, 0x200B)
    if name == "cntrl":
        return cat in ("Cc", "Cf", "Zl", "Zp")
    return True  # punct, graph, print: any character


class _ERE:
    """One compiled regex and the POSIX classes it names."""

    def __init__(self, rx: posixre.Regexp, classes: List[str]) -> None:
        self.rx = rx
        self.classes = classes

    def risk(self, ps: str) -> None:
        """Refuse a line on which the regex's POSIX classes could part from
        sed's: a non-ASCII character one of them may hold."""
        if not self.classes:
            return
        for ch in ps:
            r = ord(ch)
            if r < 0x80:
                continue
            for c in self.classes:
                if _class_may(c, r):
                    raise refused(
                        "the class [:%s:] applied to U+%04X, which a UTF-8 locale may count in it and Go does not" % (c, r)
                    )


class _Repl:
    """A replacement's literal text, or a group reference (group >= 0)."""

    def __init__(self, lit: str = "", group: int = -1) -> None:
        self.lit = lit
        self.group = group


class _Subst:
    def __init__(self, ere: _ERE, repl: List[_Repl], glob: bool) -> None:
        self.re = ere
        self.repl = repl
        self.glob = glob

    def apply(self, ps: str, room: int) -> str:
        """The substitution on the pattern space ps, refusing a result longer
        than room bytes before it is made."""
        self.re.risk(ps)
        ms = self.re.rx.find_all_submatch_index(ps, -1 if self.glob else 1)
        if not ms:
            return ps
        parts: List[str] = []
        size = 0

        def grow(x: str) -> None:
            nonlocal size
            n = len(x.encode("utf-8"))
            if size + n > room:
                raise refused("a substitution grows a line past the projection's bound (%d bytes)" % room)
            parts.append(x)
            size += n

        last = 0
        for m in ms:
            grow(ps[last:m[0]])
            for p in self.repl:
                x = p.lit
                if p.group >= 0:
                    x = ""
                    if m[2 * p.group] >= 0:
                        x = ps[m[2 * p.group]:m[2 * p.group + 1]]
                grow(x)
            last = m[1]
        grow(ps[last:])
        return "".join(parts)


class _Cmd:
    """One parsed row: an optional address, then a delete or substitutions."""

    def __init__(self) -> None:
        self.addr: Optional[_ERE] = None
        self.delete = False
        self.subs: List[_Subst] = []


def _skip_blanks(r: bytes, i: int) -> int:
    while i < len(r) and r[i] in (0x20, 0x09):
        i += 1
    return i


def _end_of_row(r: bytes, i: int) -> None:
    """Nothing but blanks and one optional ``;`` after i."""
    i = _skip_blanks(r, i)
    if i < len(r) and r[i] == 0x3B:
        i = _skip_blanks(r, i + 1)
    if i != len(r):
        raise refused("%s after the command" % gostr.quote(r[i:]))


def _scan_part(r: bytes, i: int, d: int) -> "tuple[bytes, int]":
    """r[i:] up to the next unescaped delimiter d (escapes kept, as sed's
    match_slash: bracket expressions do not hide d) and the index after."""
    out = bytearray()
    while i < len(r):
        c = r[i]
        if c == 0x5C:
            if i + 1 >= len(r):
                raise refused("a trailing backslash")
            out += r[i:i + 2]
            i += 2
            continue
        if c == d:
            return bytes(out), i + 1
        out.append(c)
        i += 1
    raise refused("an unterminated %s part" % gostr.quote(_ch(d)))


def compile_ere(rx: bytes, d: int) -> _ERE:
    """A GNU sed -E regex (delimiter d) translated into the POSIX syntax
    posixre reads (leftmost-longest, as glibc's), refusing what would not
    mean the same."""
    if not rx:
        raise refused("an empty regex (sed reuses the last regex used)")
    b = bytearray()
    classes: List[str] = []
    in_bracket = False
    i = 0
    while i < len(rx):
        c = rx[i]
        if in_bracket:
            if c == 0x5C:
                raise refused("a backslash inside a bracket expression")
            if c == 0x5B and i + 1 < len(rx) and rx[i + 1] in (0x2E, 0x3D):
                raise refused("a collating symbol or an equivalence class (%s)" % gostr.quote(rx[i:i + 2]))
            if c == 0x5B and i + 1 < len(rx) and rx[i + 1] == 0x3A:
                end = rx.find(b":]", i + 2)
                if end < 0:
                    raise refused("an unterminated character class")
                name = rx[i + 2:end]
                if name.startswith(b"^"):
                    raise refused("the Go-only negated class [:%s:]" % name.decode("utf-8", "replace"))
                classes.append(name.decode("utf-8", "replace"))
                b += rx[i:end + 2]
                i = end + 2
                continue
            if c == 0x5D:
                in_bracket = False
            b.append(c)
            i += 1
            continue
        if c == 0x5C:
            e = rx[i + 1]
            i += 2
            if e == d:
                b.append(d)  # the delimiter, unescaped (sed's own rule)
            elif e == 0x6E:
                b += b"\\n"
            elif 0x30 <= e <= 0x39:
                raise refused("a backreference in a regex")
            elif _is_punct(e) and e not in (0x3C, 0x3E, 0x60, 0x27):
                b.append(0x5C)
                b.append(e)
            else:
                raise refused("the regex escape \\%s" % _ch(e))
            continue
        if c == 0x7B:
            # a whole interval only: posixre (as Go) reads `{,n}` and any
            # other `{` as literal text, glibc as an interval or an error
            end = rx.find(b"}", i)
            if end < 0 or not _INTERVAL.fullmatch(rx[i:end + 1]):
                raise refused("a `{` that is not a whole {n}, {n,} or {n,m} interval")
            b += rx[i:end + 1]
            i = end + 1
            continue
        if c == 0x28:
            if i + 1 < len(rx) and rx[i + 1] == 0x3F:
                raise refused("the Go-only group syntax `(?`")
            b.append(c)
            i += 1
            continue
        if c == 0x5B:
            in_bracket = True
            b.append(c)
            # `]` right after `[` or `[^` is a literal member
            if i + 1 < len(rx) and rx[i + 1] == 0x5E:
                b.append(0x5E)
                i += 1
            if i + 1 < len(rx) and rx[i + 1] == 0x5D:
                b += b"\\]"
                i += 1
            i += 1
            continue
        b.append(c)
        i += 1
    if in_bracket:
        raise refused("an unterminated bracket expression")
    try:
        compiled = posixre.compile_posix(bytes(b).decode("utf-8"))
    except posixre.ReError as err:
        raise refused("the regex %s: %s" % (gostr.quote(rx), err)) from None
    return _ERE(compiled, classes)


def _parse_repl(rep: bytes, d: int) -> List[_Repl]:
    parts: List[_Repl] = []
    lit = bytearray()

    def flush() -> None:
        if lit:
            parts.append(_Repl(lit=lit.decode("utf-8")))
            lit.clear()

    i = 0
    while i < len(rep):
        c = rep[i]
        if c == 0x26:
            flush()
            parts.append(_Repl(group=0))
        elif c == 0x5C:
            e = rep[i + 1]
            i += 1
            if 0x30 <= e <= 0x39:
                flush()
                parts.append(_Repl(group=e - 0x30))
            elif e in (0x26, 0x5C, d):
                lit.append(e)
            else:
                raise refused("the replacement escape \\%s" % _ch(e))
        elif c == 0x0A:
            raise refused("a newline in a replacement")
        else:
            lit.append(c)
        i += 1
    flush()
    return parts


def _parse_subst(r: bytes, i: int) -> "tuple[_Subst, int]":
    if i >= len(r) or r[i] != 0x73:
        if i < len(r):
            raise refused("the command %s (only s, an address with d, or a block of s commands)" % gostr.quote(_ch(r[i])))
        raise refused("no command")
    if i + 1 >= len(r):
        raise refused("s without a delimiter")
    d = r[i + 1]
    if d == 0x5C or d == 0x0A or d > 0x7E or not _is_punct(d):
        raise refused("the delimiter %s" % gostr.quote(_ch(d)))
    rx, nxt = _scan_part(r, i + 2, d)
    rep, nxt = _scan_part(r, nxt, d)
    ere = compile_ere(rx, d)
    repl = _parse_repl(rep, d)
    for p in repl:
        if p.group > ere.rx.num_subexp():
            raise refused("the reference \\%d to a group the regex does not have" % p.group)
    glob = False
    while nxt < len(r) and r[nxt] not in (0x3B, 0x7D, 0x20, 0x09):
        if r[nxt] != 0x67 or glob:
            raise refused("the s flag %s (only g)" % gostr.quote(_ch(r[nxt])))
        glob = True
        nxt += 1
    return _Subst(ere, repl, glob), nxt


def parse_row(row: bytes) -> _Cmd:
    """One row (see the module's grammar)."""
    c = _Cmd()
    if len(row) > MAX_ROW_LEN:
        raise refused("a row of %d bytes, longer than %d" % (len(row), MAX_ROW_LEN))
    if not _valid_utf8(row):
        raise refused("a row that is not valid UTF-8")
    r = row.strip(b" \t")
    i = 0
    if r.startswith(b"/"):
        rx, nxt = _scan_part(r, 1, 0x2F)
        c.addr = compile_ere(rx, 0x2F)
        i = _skip_blanks(r, nxt)
        if i >= len(r):
            raise refused("an address without a command")
        if r[i] == 0x64:
            c.delete = True
            _end_of_row(r, i + 1)
            return c
        if r[i] == 0x7B:
            i += 1
            while True:
                i = _skip_blanks(r, i)
                if i < len(r) and r[i] == 0x7D:
                    if not c.subs:
                        raise refused("an empty block")
                    _end_of_row(r, i + 1)
                    return c
                s, nxt = _parse_subst(r, i)
                c.subs.append(s)
                i = _skip_blanks(r, nxt)
                if i < len(r) and r[i] == 0x3B:
                    i += 1
                    continue
                if i >= len(r) or r[i] != 0x7D:
                    raise refused("a block command must be `s` commands separated by `;` and closed by `}`")
    s, nxt = _parse_subst(r, i)
    c.subs = [s]
    _end_of_row(r, nxt)
    return c


def project(ctx: Ctx, lst: bytes, content: bytes) -> bytes:
    """content as the projection publishes it: every fork-only block dropped
    (a line that is ``<!-- fork-only:begin -->`` or ``<!-- fork-only:end
    -->`` once trimmed, and everything between), then the list's REWRITE
    rows applied to every line in order. Each kept line ends with a
    newline, as awk prints it. RowRefused for a row outside the grammar or a
    bound exceeded; ctx's error when it is done."""
    rows = rewrite_rows(lst)
    if len(rows) > MAX_ROWS:
        raise refused("%d REWRITE rows, more than %d" % (len(rows), MAX_ROWS))
    prog: List[_Cmd] = []
    commands = 0
    for i, row in enumerate(rows):
        try:
            c = parse_row(row)
        except RowRefused as err:
            raise _wrap("REWRITE row %d %s" % (i + 1, gostr.quote(_clip_row(row))), err) from None
        commands += len(c.subs)
        if c.delete:
            commands += 1
        if commands > MAX_COMMANDS:
            raise refused("more than %d commands in the REWRITE rows" % MAX_COMMANDS)
        prog.append(c)
    out = bytearray()
    limit = output_limit(len(content))
    fork = False
    for n, line in enumerate(awk_lines(content)):
        ctx.check()
        t = line.strip(AWK_SPACE)
        if t == b"<!-- fork-only:begin -->":
            fork = True
            continue
        if t == b"<!-- fork-only:end -->":
            fork = False
            continue
        if fork:
            continue
        if not prog:
            out += line + b"\n"
            continue
        if not _valid_utf8(line):
            raise refused("line %d is not valid UTF-8" % (n + 1))
        room = min(MAX_LINE, limit - len(out) - 1)
        if len(line) > room:
            raise refused("line %d is longer than the projection's bound (%d bytes)" % (n + 1, room))
        ps: str = line.decode("utf-8")
        keep = True
        for c in prog:
            ctx.check()
            try:
                if c.addr is not None:
                    c.addr.risk(ps)
                    if not c.addr.rx.match_string(ps):
                        continue
                if c.delete:
                    keep = False
                    break
                for s in c.subs:
                    ps = s.apply(ps, room)
            except RowRefused as err:
                raise _wrap("line %d" % (n + 1), err) from None
        if keep:
            out += ps.encode("utf-8") + b"\n"
    return bytes(out)


def projected_sha(ctx: Ctx, lst: bytes, content: Union[bytes, bytearray]) -> str:
    """The sha256 of content as the projection publishes it under lst."""
    return hashlib.sha256(project(ctx, lst, bytes(content))).hexdigest()
