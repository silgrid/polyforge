"""The conflicts with a mechanical answer. In a merge of the default branch
into a published branch:

- an append-only file both sides appended to resolves as the default
  branch's file followed by the bytes the branch appended past their merge
  base — the sections land in merge order. A side that rewrote a byte of
  the base is no append: a real conflict;
- a pin or list holder settles its conflicting hunks line by line, and only
  two kinds (conf.py): a hunk whose sides differ only in the declared pins'
  hashes (the pin's line) takes the default branch's side; a hunk inside a
  declared list's region (between its begin and end marker lines) where
  both sides only inserted whole lines carrying the list's comment prefix
  at the same point of the base takes the default branch's lines then the
  branch's. Nothing is spliced inside a line. Then, for every pin of a
  resolved file, each side's hash of it is replaced throughout the holder
  by the resolved file's hash.

A pin's hash is computed in-process (``pin_hash``): the sha256, or the
projection of the pinned file under a list whose rows — the branch's among
them — are interpreted, never run (project.py). A pin that cannot be
recomputed leaves its holder a real conflict. The conf is read from the
default branch: a change cannot widen its own resolution. Everything else
is a real conflict, left in progress.

A file's content and lines are bytes; paths are text.
"""

import hashlib
import os
import re
import shutil
import tempfile
from dataclasses import dataclass, field
from typing import Callable, Dict, List, Optional, Set, Tuple

from . import gostr
from .conf import Conf, ListDecl, Pin
from .ctx import GoError
from .git import CmdError, Git
from .project import RowRefused, projected_sha

_HEX64 = re.compile(rb"[0-9a-f]{64}", re.ASCII)


@dataclass
class Scope:
    """What one resolution reads besides the conflicted index: main, the
    default branch's commit, and sides, the revisions whose versions of a
    pinned file a holder's pin line may carry."""

    main: str
    sides: List[str] = field(default_factory=list)


@dataclass
class Hunk:
    """One diff3 conflict hunk: the branch's, the base's and main's lines."""

    br: List[bytes]
    base: List[bytes]
    mn: List[bytes]


@dataclass
class Seg:
    """A merge-file output's run of merged lines, or one conflict hunk."""

    lines: List[bytes] = field(default_factory=list)
    h: Optional[Hunk] = None


Settle = Callable[[List[Seg], int], Optional[List[bytes]]]


def read_file(path: str) -> bytes:
    try:
        with open(path, "rb") as f:
            return f.read()
    except OSError as err:
        raise GoError(gostr.os_error("open", path, err)) from None


def write_file(path: str, data: bytes) -> None:
    try:
        with open(path, "wb") as f:
            f.write(data)
    except OSError as err:
        raise GoError(gostr.os_error("open", path, err)) from None


def stage(g: Git, cwd: str, n: int, p: str) -> Optional[bytes]:
    """The index stage n of a conflicted path (None = absent)."""
    try:
        return g.cmd(cwd, "git", "show", ":%d:%s" % (n, p))
    except GoError:
        return None


def show(g: Git, cwd: str, rev: str, p: str) -> Optional[bytes]:
    """A file at a revision (None = absent)."""
    try:
        return g.cmd(cwd, "git", "show", rev + ":" + p)
    except GoError:
        return None


def conflict_marked(b: bytes) -> bool:
    """Whether a file still carries a conflict marker line."""
    return b.startswith(b"<<<<<<< ") or b"\n<<<<<<< " in b


def pin_hash(g: Git, cwd: str, conf: Conf, main: str, pin: Pin, content: bytes) -> str:
    """A pin's hash of content: the sha256, or — a projected pin — the
    sha256 of content as the projection publishes it under the pin's list,
    the list as the merge resolved it so far when it is a settled pin
    holder, else the default branch's (main's). RowRefused (wrapped) for a
    row outside the grammar."""
    if pin.list == "":
        return hashlib.sha256(content).hexdigest()
    lst: Optional[bytes] = None
    if main != "":
        lst = show(g, cwd, main, pin.list)
    if conf.holder(pin.list):
        try:
            with open(os.path.join(cwd, pin.list), "rb") as f:
                wt = f.read()
            if not conflict_marked(wt):
                lst = wt
        except OSError:
            pass
    if lst is None:
        raise GoError("pin %s in %s: no projection list %s to project with" % (pin.file, pin.holder, pin.list))
    try:
        return projected_sha(g.ctx, lst, content)
    except GoError as err:
        raise err.wrap("pin %s in %s (projected by %s)" % (pin.file, pin.holder, pin.list)) from None


def known_pins(g: Git, cwd: str, conf: Conf, holder: str, rs: Scope) -> Set[str]:
    """The hashes holder's pin lines may carry: every pin's hash of its
    file's version at each of the scope's sides."""
    known: Set[str] = set()
    for pin in conf.pins:
        if pin.holder != holder:
            continue
        for rev in rs.sides:
            content = show(g, cwd, rev, pin.file)
            if content is None:
                continue
            try:
                known.add(pin_hash(g, cwd, conf, rs.main, pin, content))
            except RowRefused:
                continue  # no hash this side's pin line could carry
    return known


def lines(s: bytes) -> List[bytes]:
    """s split into its lines, each keeping its newline."""
    if not s:
        return []
    out = s.split(b"\n")
    res = [x + b"\n" for x in out[:-1]]
    if out[-1]:
        res.append(out[-1])
    return res


_OPEN = b"<<<<<<< branch\n"
_MID = b"||||||| base\n"
_SEP = b"=======\n"
_CLOSE = b">>>>>>> main\n"


def parse_merge(out: bytes) -> Optional[List[Seg]]:
    """A diff3 merge-file output (branch / base / main) split into merged
    runs and conflict hunks (None = unreadable markers)."""
    segs: List[Seg] = []
    rest = out
    while True:
        i = rest.find(_OPEN)
        if i < 0:
            if _CLOSE in rest:
                return None
            segs.append(Seg(lines=lines(rest)))
            return segs
        segs.append(Seg(lines=lines(rest[:i])))
        rest = rest[i + len(_OPEN):]
        j, k, m = rest.find(_MID), rest.find(_SEP), rest.find(_CLOSE)
        if j < 0 or k < j or m < k:
            return None
        segs.append(Seg(h=Hunk(br=lines(rest[:j]), base=lines(rest[j + len(_MID):k]), mn=lines(rest[k + len(_SEP):m]))))
        rest = rest[m + len(_CLOSE):]


def merge_hunks(g: Git, br: bytes, base: bytes, mn: bytes, settle: Settle) -> Optional[bytes]:
    """A diff3 merge of the three versions (branch, base, main) with every
    conflict hunk rewritten by settle (None = one is not settled)."""
    tmp = tempfile.mkdtemp(prefix="pf-integrate-merge-")
    try:
        names = []
        for name, b in (("b", br), ("o", base), ("m", mn)):
            p = os.path.join(tmp, name)
            with open(p, "wb") as f:
                f.write(b)
            names.append(p)
        # an exit status n > 0 is n conflicts: the markers are the output
        try:
            out = g.cmd(tmp, "git", "merge-file", "-p", "--diff3", "-L", "branch", "-L", "base", "-L", "main", *names)
        except CmdError as err:
            out = err.stdout
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    segs = parse_merge(out)
    if segs is None:
        return None
    parts: List[bytes] = []
    for i, sg in enumerate(segs):
        if sg.h is None:
            parts += sg.lines
            continue
        res = settle(segs, i)
        if res is None:
            return None
        parts += res
    return b"".join(parts)


def _hunk_marked(lst: ListDecl, h: Hunk) -> bool:
    return lst.markers(h.br) or lst.markers(h.base) or lst.markers(h.mn)


def holds(lst: ListDecl, segs: List[Seg], i: int) -> bool:
    """Whether lst's region holds hunk i of segs: the nearest marker line
    before it among the merged runs is the begin marker and the nearest
    after it the end marker, and neither the hunk nor any hunk between
    carries a marker of the list on any side."""
    h = segs[i].h
    if h is None or _hunk_marked(lst, h):
        return False

    def look(step: int) -> bytes:
        j = i + step
        while 0 <= j < len(segs):
            hj = segs[j].h
            if hj is not None:
                if _hunk_marked(lst, hj):
                    return b""
                j += step
                continue
            ls = segs[j].lines
            for k in range(len(ls)):
                x = ls[k] if step > 0 else ls[len(ls) - 1 - k]
                if lst.marker(x, lst.begin):
                    return lst.begin
                if lst.marker(x, lst.end):
                    return lst.end
            j += step
        return b""

    return look(-1) == lst.begin and look(1) == lst.end


def regions_of(lists: List[ListDecl], segs: List[Seg], i: int) -> List[ListDecl]:
    return [lst for lst in lists if holds(lst, segs, i)]


def all_entries(lst: ListDecl, ls: List[bytes]) -> bool:
    return all(lst.entry(x) for x in ls)


def mask_pins(ls: List[bytes], known: Set[str]) -> List[bytes]:
    """ls with every known pin hash masked."""

    def sub(m: "re.Match[bytes]") -> bytes:
        return b"<pf-pin>" if m.group().decode("ascii") in known else m.group()

    return [_HEX64.sub(sub, x) for x in ls]


def masked_equal(a: List[bytes], b: List[bytes], known: Set[str]) -> bool:
    return b"".join(mask_pins(a, known)) == b"".join(mask_pins(b, known))


def settle_hunk(h: Hunk, known: Set[str], lists: List[ListDecl]) -> Optional[List[bytes]]:
    """One holder hunk, line by line, the known pin hashes masked: the sides
    equal but for those hashes → main's side; both sides only inserting
    whole lines at the same point of the base, every inserted line one of a
    list whose region holds the hunk → the base with main's lines then the
    branch's inserted there. Anything else is a real conflict (None)."""
    br, base, mn = h.br, h.base, h.mn
    mb, mo, mm = mask_pins(br, known), mask_pins(base, known), mask_pins(mn, known)
    if b"".join(mb) == b"".join(mm):
        return list(mn)
    p = 0
    while p < len(mo) and p < len(mb) and p < len(mm) and mo[p] == mb[p] and mo[p] == mm[p]:
        p += 1
    s = 0
    while (s < len(mo) - p and s < len(mb) - p and s < len(mm) - p
           and mo[len(mo) - 1 - s] == mb[len(mb) - 1 - s] and mo[len(mo) - 1 - s] == mm[len(mm) - 1 - s]):
        s += 1
    if p + s != len(mo):
        return None  # a side changed a line of the base
    ins_b, ins_m = br[p:len(br) - s], mn[p:len(mn) - s]
    for lst in lists:
        if all_entries(lst, ins_b) and all_entries(lst, ins_m):
            return mn[:p] + ins_m + ins_b + mn[len(mn) - s:]
    return None


def holder_merge(g: Git, cwd: str, p: str, known: Set[str], lists: List[ListDecl]) -> bool:
    """A conflicted holder settled hunk by hunk (settle_hunk) on a diff3
    merge of its index stages, written; False = a real conflict."""
    base, br, mn = stage(g, cwd, 1, p), stage(g, cwd, 2, p), stage(g, cwd, 3, p)
    if base is None or br is None or mn is None:
        return False

    def settle(segs: List[Seg], i: int) -> Optional[List[bytes]]:
        h = segs[i].h
        assert h is not None
        return settle_hunk(h, known, regions_of(lists, segs, i))

    merged = merge_hunks(g, br, base, mn, settle)
    if merged is None:
        return False
    write_file(os.path.join(cwd, p), merged)
    return True


def replace_in(g: Git, cwd: str, rel: str, old: List[str], new: List[str]) -> None:
    """Every old[i] replaced by new[i] in the working tree's file rel, staged."""
    f = os.path.join(cwd, rel)
    text = read_file(f)
    for o, n in zip(old, new):
        if o:
            text = text.replace(o.encode("ascii"), n.encode("ascii"))
    write_file(f, text)
    g.git(cwd, "add", "--", rel)


def resolve(g: Git, cwd: str, conf: Optional[Conf], conflicted: List[str], rs: Scope) -> Tuple[List[str], List[str], List[str]]:
    """The conflicted paths conf makes mechanical resolved and staged: the
    resolved paths, the remaining (real) conflicts — a holder whose pin
    cannot be recomputed among them, conflicted by git or not — and why such
    a holder could not be settled."""
    if conf is None:
        conf = Conf()
    appended: Dict[str, Tuple[bytes, bytes]] = {}
    done: Dict[str, bool] = {}
    notes: List[str] = []
    for p in conflicted:
        if not conf.is_append_only(p):
            continue
        base, br, mn = stage(g, cwd, 1, p), stage(g, cwd, 2, p), stage(g, cwd, 3, p)
        if base is None or br is None or mn is None or not br.startswith(base) or not mn.startswith(base):
            continue
        write_file(os.path.join(cwd, p), mn + br[len(base):])
        appended[p] = (br, mn)
        done[p] = True
    # a pin holder's pin lines and comment list
    for p in conflicted:
        if done.get(p) or not conf.holder(p):
            continue
        known = known_pins(g, cwd, conf, p, rs)
        if holder_merge(g, cwd, p, known, conf.lists_of(p)):
            done[p] = True
    # the pins of every resolved file: each side's hash becomes the resolved
    # file's, in every holder (conflicted or not); every value computed
    # before any holder is rewritten
    swaps: List[Tuple[str, List[str], List[str]]] = []
    unpinned: Dict[str, bool] = {}
    for pin in conf.pins:
        sides = appended.get(pin.file)
        if sides is None or unpinned.get(pin.holder):
            continue
        if not done.get(pin.holder) and pin.holder in conflicted:
            continue  # its conflict is a real one: the pins cannot be had
        branch, main = sides
        cur = read_file(os.path.join(cwd, pin.file))
        hs: List[str] = []
        try:
            for content in (main, branch, cur):
                hs.append(pin_hash(g, cwd, conf, rs.main, pin, content))
        except RowRefused as err:
            unpinned[pin.holder], done[pin.holder] = True, False
            notes.append("%s: its pin of %s cannot be recomputed: %s" % (pin.holder, pin.file, err))
            continue
        swaps.append((pin.holder, [hs[0], hs[1]], [hs[2], hs[2]]))
    for holder, old, new in swaps:
        if unpinned.get(holder):
            continue
        replace_in(g, cwd, holder, old, new)
    resolved: List[str] = []
    remaining: List[str] = []
    for p in conflicted:
        if not done.get(p):
            remaining.append(p)
            continue
        g.git(cwd, "add", "--", p)
        resolved.append(p)
    for pin in conf.pins:
        if unpinned.get(pin.holder) and pin.holder not in remaining:
            remaining.append(pin.holder)
    return resolved, remaining, notes
