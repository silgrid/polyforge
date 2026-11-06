"""sync.py — origin's default branch merged into every task repo of the
capsule, in place (the successor of the Go step command pf-sync: the same
merge, the same record, the same result file; a real conflict is the sync
agent's to resolve, then ``sync.py --continue``).

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" [--capsule DIR] [--deadline D] [--json] [--out PATH]
    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --continue [...]
    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/sync.py" --abort [...]

One pass, per task repo (repos/<name>) in name order: a linked worktree is
refused (its merge attribute override would land in the shared common
dir); a merge a crash left in progress (MERGE_HEAD) is aborted; then
origin's default branch is fetched and merged into the branch checked out —
fast-forwarded when the branch has no commit of its own, else a ``--no-ff``
merge with every in-tree merge attribute overridden for it only and the
conflicts the DEFAULT BRANCH's .polyforge/integrate.conf makes mechanical
resolved and committed as the lane's identity. A real conflict is left in
progress and its files listed: the agent resolves them in place and runs
``--continue``, which refuses a residual conflict marker, an unmerged or
unstaged path, a mechanically resolved file changed and a resolved text
file that drops a line both parents (HEAD and MERGE_HEAD) carry, commits
each merge,
and rewrites the SAME result file — ``conflicts`` emptied, ``resolved``
filled, ``after`` the merge commit.

``--abort`` abandons the run: every merge the record left in progress is
aborted (the repo back at its own HEAD, nothing reset), a merge the run
already committed is kept, the record is removed and the result is written
marked ``aborted`` (a pending repo keeps its ``conflicts``, its ``after``
empty: nothing downstream can read it as synced); exit 0, also when no sync
is in progress.

Crash re-entry: before anything merges, the run's record (``syncing.json``
beside --out, else the capsule's artifacts/sync/) holds the step round it
ran in (PF_ROUND), each repo's branch, origin's head of it and ``before``;
every merge made is recorded (``after``) once committed, and the record is
removed when the run settles with nothing left to resolve. A plain run that
finds a record of ANOTHER round supersedes it: a leftover MERGE_HEAD is
aborted and the sync starts over from HEAD, nothing reset. One that finds a
record of its own round (or one with no round) is the same run re-entered:
a leftover MERGE_HEAD is aborted, a repo with another branch checked out or
origin's branch moved is refused (exit 2), a HEAD that moved past
``before`` with commits that are not the sync's own merge is refused (exit
2: ``--abort`` drops the record, the work stays), and any other HEAD that
is neither ``before`` nor the recorded ``after`` is reset to ``before`` (the
commit kept under refs/polyforge/discarded/pf-sync/<UTC time to the
nanosecond>) before the merge is made again. ``--continue`` with no sync in
progress is refused before anything is written — the settled result at
--out stays as it is — and one whose merge is no longer in progress is exit
2 with the result rewritten. A ``--continue`` round that leaves some files
unresolved moves the files resolved so far to ``resolved`` and keeps the
rest under ``conflicts``.

The result's details::

    { "record": "<syncing.json>",
      "repos": [{name, branch, before, after, default_branch, default_head, fast_forwarded,
                 mechanical[], conflicts[], resolved[], notes[], incoming[], discarded[]}] }

``heads`` are each repo's HEAD when the script ends. Exit 0: every repo is
synced. Exit 1: a real conflict is left in progress (the files under
``conflicts``), or ``--continue`` found one still unresolved. Exit 2: not
judged — no task repo, a linked worktree, a fetch that failed, a repo whose
branch or origin moved under the run, work committed on top of the run's
record, ``--continue`` with nothing to continue — or the invocation
unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import datetime  # noqa: E402
import json  # noqa: E402
import time  # noqa: E402
import traceback  # noqa: E402
from dataclasses import dataclass, field  # noqa: E402
from typing import Any, Dict, List, Mapping, Optional, Tuple  # noqa: E402

from pfstep import atomic, jsonout, runner  # noqa: E402
from pfstep.invoke import Invocation, duration_flag, go_list  # noqa: E402
from pfstep.gitmerge import gostr  # noqa: E402
from pfstep.gitmerge import merge as gm  # noqa: E402
from pfstep.gitmerge.ctx import Ctx, GoError  # noqa: E402
from pfstep.gitmerge.git import Git, paths  # noqa: E402
from pfstep.gitmerge.resolve import show  # noqa: E402

NAME = "sync.py"
RECORD_NAME = "syncing.json"
DISCARDED_PREFIX = "refs/polyforge/discarded/pf-sync/"


class Refused(GoError):
    """A sync that cannot run on this capsule as it stands (exit 2: the
    environment, never the change's verdict)."""


def append_new(lst: Optional[List[str]], *xs: str) -> Optional[List[str]]:
    """lst with each of xs it does not hold yet appended (None stays None
    when nothing is appended, as a nil Go slice)."""
    for x in xs:
        if lst is None or x not in lst:
            lst = (lst or []) + [x]
    return lst


def or_empty(lst: Optional[List[str]]) -> List[str]:
    return [] if lst is None else lst


@dataclass
class Discarded:
    commit: str
    ref: str


@dataclass
class SyncRepo:
    """One task repo's sync: the run's record of it and the details entry.
    A list that is None is Go's nil slice (null in the record)."""

    name: str
    branch: str = ""
    origin: str = ""
    before: str = ""
    after: str = ""
    default_branch: str = ""
    default_head: str = ""
    fast_forwarded: bool = False
    mechanical: Optional[List[str]] = None
    conflicts: Optional[List[str]] = None
    resolved: Optional[List[str]] = None
    notes: Optional[List[str]] = None
    incoming: Optional[List[str]] = None
    discarded: List[Discarded] = field(default_factory=list)
    self_healed: bool = False
    mechanical_blobs: Optional[Dict[str, str]] = None
    aborted: bool = False

    def pending(self) -> bool:
        return bool(self.conflicts)

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"name": self.name}
        if self.branch:
            d["branch"] = self.branch
        if self.origin:
            d["origin"] = self.origin
        d["before"] = self.before
        if self.after:
            d["after"] = self.after
        if self.default_branch:
            d["default_branch"] = self.default_branch
        if self.default_head:
            d["default_head"] = self.default_head
        if self.fast_forwarded:
            d["fast_forwarded"] = True
        d["mechanical"] = self.mechanical
        d["conflicts"] = self.conflicts
        d["resolved"] = self.resolved
        if self.notes:
            d["notes"] = list(self.notes)
        if self.incoming:
            d["incoming"] = list(self.incoming)
        if self.discarded:
            d["discarded"] = [{"commit": x.commit, "ref": x.ref} for x in self.discarded]
        if self.self_healed:
            d["self_healed"] = True
        if self.mechanical_blobs:
            d["mechanical_blobs"] = {k: self.mechanical_blobs[k] for k in sorted(self.mechanical_blobs)}
        if self.aborted:
            d["aborted"] = True
        return d


@dataclass
class SyncRecord:
    record: str
    round: str = ""
    aborted: bool = False
    repos: List[SyncRepo] = field(default_factory=list)

    def repo(self, name: str) -> Optional[SyncRepo]:
        for x in self.repos:
            if x.name == name:
                return x
        return None

    def pending(self) -> List[SyncRepo]:
        return [x for x in self.repos if x.pending()]

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"record": self.record}
        if self.round:
            d["round"] = self.round
        if self.aborted:
            d["aborted"] = True
        d["repos"] = [r.to_dict() for r in self.repos]
        return d

    def save(self) -> None:
        atomic.write(self.record, jsonout.dumps(self.to_dict()).encode("utf-8"), 0o644)


def _typed(v: Any, want: type, what: str) -> Any:
    if v is not None and not isinstance(v, want):
        raise ValueError("json: cannot unmarshal %s into %s" % (type(v).__name__, what))
    return v


def _str_list(v: Any, what: str) -> Optional[List[str]]:
    _typed(v, list, what)
    if v is None:
        return None
    for x in v:
        _typed(x, str, what)
    return list(v)


def _repo_of(d: Any) -> SyncRepo:
    _typed(d, dict, "syncRepo")
    if d is None:
        d = {}
    blobs = _typed(d.get("mechanical_blobs"), dict, "mechanical_blobs")
    disc = []
    for x in _typed(d.get("discarded"), list, "discarded") or []:
        _typed(x, dict, "syncDiscarded")
        x = x or {}
        disc.append(Discarded(commit=_typed(x.get("commit"), str, "commit") or "", ref=_typed(x.get("ref"), str, "ref") or ""))
    return SyncRepo(
        name=_typed(d.get("name"), str, "name") or "",
        branch=_typed(d.get("branch"), str, "branch") or "",
        origin=_typed(d.get("origin"), str, "origin") or "",
        before=_typed(d.get("before"), str, "before") or "",
        after=_typed(d.get("after"), str, "after") or "",
        default_branch=_typed(d.get("default_branch"), str, "default_branch") or "",
        default_head=_typed(d.get("default_head"), str, "default_head") or "",
        fast_forwarded=bool(_typed(d.get("fast_forwarded"), bool, "fast_forwarded")),
        mechanical=_str_list(d.get("mechanical"), "mechanical"),
        conflicts=_str_list(d.get("conflicts"), "conflicts"),
        resolved=_str_list(d.get("resolved"), "resolved"),
        notes=_str_list(d.get("notes"), "notes"),
        incoming=_str_list(d.get("incoming"), "incoming"),
        discarded=disc,
        self_healed=bool(_typed(d.get("self_healed"), bool, "self_healed")),
        mechanical_blobs={str(k): str(v) for k, v in blobs.items()} if blobs is not None else None,
        aborted=bool(_typed(d.get("aborted"), bool, "aborted")),
    )


def read_record(path: str) -> Optional[SyncRecord]:
    """The record at path (None = none)."""
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except FileNotFoundError:
        return None
    except OSError as err:
        raise GoError(gostr.os_error("open", path, err)) from None
    try:
        d = json.loads(raw.decode("utf-8", "replace"))
        _typed(d, dict, "syncRecord")
        rec = SyncRecord(record=path, round=_typed(d.get("round"), str, "round") or "",
                         aborted=bool(_typed(d.get("aborted"), bool, "aborted")),
                         repos=[_repo_of(x) for x in _typed(d.get("repos"), list, "repos") or []])
    except ValueError as err:
        raise GoError("%s is not a sync record: %s" % (path, err)) from None
    return rec


def capsule_repos(capsule_dir: str) -> Tuple[str, List[str]]:
    """The capsule root and its task repos (repos/<name> holding .git), by name."""
    root = os.path.abspath(capsule_dir)
    d = os.path.join(root, "repos")
    try:
        entries = list(os.scandir(d))
    except OSError as err:
        raise GoError("--capsule %s: the task repos: %s" % (root, gostr.os_error("open", d, err))) from None
    out = sorted(e.name for e in entries if e.is_dir(follow_symlinks=False) and os.path.exists(os.path.join(d, e.name, ".git")))
    return root, out


def non_empty_lines(s: str) -> List[str]:
    return [x.strip() for x in s.split("\n") if x.strip()]


def checked_out(g: Git, cwd: str) -> str:
    """The branch checked out ("" = detached)."""
    try:
        return g.git(cwd, "symbolic-ref", "-q", "--short", "HEAD").strip()
    except GoError:
        return ""


def has_conflict_marker(content: bytes) -> bool:
    """Whether content carries a conflict marker line: seven ``<``, ``|`` or
    ``>`` at the line's start, then a space or the line's end."""
    for line in content.split(b"\n"):
        if line.endswith(b"\r"):
            line = line.rstrip(b"\r")
        for m in (b"<<<<<<<", b"|||||||", b">>>>>>>"):
            if line == m or line.startswith(m + b" "):
                return True
    return False


def dropped_lines(ours: bytes, theirs: bytes, merged: bytes) -> Optional[List[bytes]]:
    """The distinct lines both parents carry that the merge result lacks, in
    ours' order (None = binary: a NUL in the first 8000 bytes of any of the
    three, git's own test). Lines compare with trailing whitespace stripped,
    blank lines ignored, order and repetition not counted."""
    if any(b"\0" in b[:8000] for b in (ours, theirs, merged)):
        return None

    def lines(b: bytes) -> List[bytes]:
        return [x for x in (ln.rstrip() for ln in b.split(b"\n")) if x]

    kept, have = set(lines(theirs)), set(lines(merged))
    out: List[bytes] = []
    for x in lines(ours):
        if x in kept and x not in have:
            out.append(x)
            have.add(x)
    return out


class Sync:
    """One run of the script."""

    def __init__(self, inv: Invocation, g: Git, capsule: str, record: str) -> None:
        self.inv = inv
        self.g = g
        self.capsule = capsule
        self.record = record

    def dir(self, name: str) -> str:
        return os.path.join(self.capsule, "repos", name)

    def heads(self, rec: SyncRecord) -> Dict[str, str]:
        out: Dict[str, str] = {}
        for r in rec.repos:
            try:
                out[r.name] = self.g.rev(self.dir(r.name), "HEAD")
            except GoError:
                pass
        return out

    def settle(self, rec: SyncRecord) -> int:
        """The exit code of a run whose merges are done: 1 with the record
        kept while a real conflict is pending, else 0 with it removed."""
        inv = self.inv
        pend = rec.pending()
        if pend:
            lines = ["repos/%s: %s" % (r.name, ", ".join(or_empty(r.conflicts))) for r in pend]
            inv.summary = (
                "%d repo(s) left with a real conflict, the merge in progress — resolve each file in place, `git add`"
                " it, then run `sync.py --continue`: %s" % (len(pend), "; ".join(lines))
            )
            return 1
        try:
            os.remove(rec.record)
        except FileNotFoundError:
            pass
        except OSError as err:
            inv.summary = "the sync record %s could not be removed: %s" % (rec.record, gostr.os_error("remove", rec.record, err))
            return 2
        merged = ["repos/" + r.name for r in rec.repos if r.after != r.before]
        same = ["repos/" + r.name for r in rec.repos if r.after == r.before]
        if not merged:
            inv.summary = "every task repo already contains its default branch: %s" % ", ".join(same)
        elif not same:
            inv.summary = "the default branch merged into %s" % ", ".join(merged)
        else:
            inv.summary = "the default branch merged into %s; already contained in %s" % (", ".join(merged), ", ".join(same))
        return 0

    def run_repos(self, names: List[str], prior: Optional[SyncRecord]) -> Tuple[Optional[SyncRecord], Optional[GoError]]:
        """The pass: every repo held to the record, the record written before
        anything merges, then each repo fetched and merged. The record as far
        as it got (None when the run could not start), and the error."""
        g, inv = self.g, self.inv
        rnd = inv.getenv("PF_ROUND")
        rec = SyncRecord(record=self.record, round=rnd)
        superseded = ""
        if prior is not None and prior.round != "" and rnd != "" and prior.round != rnd:
            # a record of a round that never settled (a rewind or a cancel
            # left it): its merges in progress are aborted below and nothing
            # is held to it — never a reset over what was committed since
            superseded, prior = prior.round, None
        try:
            for name in names:
                d = self.dir(name)
                try:
                    is_linked = gm.linked(g, d)
                except GoError as err:
                    raise err.wrap("repos/" + name) from None
                if is_linked:
                    raise Refused(
                        "sync refused: repos/%s is a linked git worktree (its git dir is not its common dir): the merge"
                        " attribute override every merge writes to $GIT_DIR/info/attributes would be the common dir's,"
                        " shared with every other worktree — the capsule's task repos must be standalone clones" % name
                    )
                healed = False
                if gm.in_progress(g, d) != "":
                    try:
                        gm.abort(g, d)
                    except GoError as err:
                        raise err.wrap("repos/%s: aborting the merge a crash left in progress" % name) from None
                    healed = True
                branch = checked_out(g, d)
                try:
                    head = g.rev(d, "HEAD")
                except GoError as err:
                    raise err.wrap("repos/%s: reading HEAD" % name) from None
                r = prior.repo(name) if prior is not None else None
                if r is not None:
                    self.hold_to_run(d, r, branch, head)
                else:
                    r = SyncRepo(name=name, branch=branch, before=head)
                    if branch != "":
                        try:
                            r.origin = gm.remote_head(g, d, branch)
                        except GoError as err:
                            raise err.wrap("repos/%s: reading origin's %s" % (name, branch)) from None
                r.self_healed = r.self_healed or healed
                if superseded != "":
                    note = ("the sync record of round %s (this is round %s) was superseded: its merge in progress aborted,"
                            " the sync started over from HEAD, nothing reset" % (superseded, rnd))
                    r.notes = append_new(r.notes, note)
                    inv.say("repos/%s: %s" % (name, note))
                r.mechanical, r.conflicts, r.resolved = or_empty(r.mechanical), or_empty(r.conflicts), or_empty(r.resolved)
                rec.repos.append(r)
        except GoError as err:
            return None, err
        try:
            rec.save()
        except OSError as err:
            return None, GoError("%s not written: %s" % (self.record, err))
        for r in rec.repos:
            try:
                self.sync_one(self.dir(r.name), r)
            except GoError as err:
                return rec, err
            try:
                rec.save()
            except OSError as err:
                return rec, GoError("%s not written: %s" % (self.record, err))
        return rec, None

    def sync_one(self, d: str, r: SyncRepo) -> None:
        """The default branch fetched and merged into one repo (r.before is
        HEAD): nothing when HEAD already contains it, a fast-forward when the
        branch has no commit of its own, else gitmerge's sync."""
        g, inv, name = self.g, self.inv, r.name
        r.default_branch = g.default_branch(d)
        try:
            g.fetch(d, r.default_branch)
        except GoError as err:
            raise err.wrap("repos/%s: fetching %s" % (name, r.default_branch)) from None
        try:
            r.default_head = g.rev(d, "refs/remotes/origin/" + r.default_branch)
        except GoError as err:
            raise err.wrap("repos/%s: origin has no %s" % (name, r.default_branch)) from None
        try:
            conf = gm.conf_at(g, d, "refs/remotes/origin/" + r.default_branch)
        except GoError as err:
            raise err.wrap("repos/" + name) from None
        try:
            head = g.rev(d, "HEAD")
        except GoError as err:
            raise err.wrap("repos/%s: reading HEAD" % name) from None
        try:
            out = g.git(d, "log", "--format=%h %s", "--max-count=50", head + ".." + r.default_head)
            lines = non_empty_lines(out)
            if lines:
                r.incoming = lines
        except GoError:
            pass
        if head == r.default_head or g.is_ancestor(d, r.default_head, head):
            r.after = head
            inv.say("repos/%s: HEAD already contains %s (%s)" % (name, r.default_branch, gostr.short(r.default_head)))
            return
        if g.is_ancestor(d, head, r.default_head):
            # a repo with no commit of the item's: fast-forwarded, never given
            # a merge commit it would then have to publish
            try:
                g.git(d, "merge", "-q", "--ff-only", r.default_head)
            except GoError as err:
                raise err.wrap("repos/%s: fast-forwarding to %s" % (name, r.default_branch)) from None
            r.after, r.fast_forwarded = r.default_head, True
            inv.say("repos/%s: fast-forwarded to %s (%s)" % (name, r.default_branch, gostr.short(r.default_head)))
            return
        # the merge attribute override lives for this merge only: the clone
        # is the work item's working clone
        msg = "Merge %s into %s (sync)" % (r.default_branch, gostr.or_text(r.branch, "HEAD"))
        try:
            synced = g.keep_attributes(d, lambda: gm.sync(g, d, r.default_branch, r.default_head, conf, msg))
        except GoError as err:
            raise err.wrap("repos/%s: merging %s" % (name, r.default_branch)) from None
        r.mechanical = append_new(r.mechanical, *synced.resolved)
        r.notes = append_new(r.notes, *synced.notes)
        if not synced.conflicts:
            r.after = synced.updated
            inv.say("repos/%s: merged %s (%s) as %s; resolved mechanically: %s" % (
                name, r.default_branch, gostr.short(r.default_head), gostr.short(r.after),
                gostr.or_text(", ".join(synced.resolved), "nothing")))
            return
        r.conflicts, r.mechanical_blobs = list(synced.conflicts), {}
        for f in synced.resolved:
            try:
                blob = g.git(d, "rev-parse", ":" + f)
            except GoError as err:
                raise err.wrap("repos/%s: reading the staged resolution of %s" % (name, f)) from None
            r.mechanical_blobs[f] = blob.strip()
        inv.say("repos/%s: merging %s (%s) conflicts in %s — the merge is left in progress" % (
            name, r.default_branch, gostr.short(r.default_head), ", ".join(synced.conflicts)))

    def hold_to_run(self, d: str, r: SyncRepo, branch: str, head: str) -> None:
        """One repo, on a re-entry of the run, held to its record r: the
        branch checked out and origin's head of it as the run found them, else
        refused; HEAD at before or at the recorded after, else reset to before
        — the commit found kept under refs/polyforge/discarded/pf-sync/<t> and
        named on r. A merge recorded (HEAD at after) keeps its record; any
        other HEAD starts the repo over."""
        g, inv = self.g, self.inv
        rp = "repos/" + r.name
        if branch != r.branch:
            raise Refused(
                "sync refused: %s has %s checked out, not %s, where this sync started — check %s out again in %s, then"
                " run sync.py again" % (rp, gostr.or_text(branch, "a detached HEAD"), gostr.or_text(r.branch, "a detached HEAD"),
                                        gostr.or_text(r.branch, r.before), rp))
        if r.branch != "":
            try:
                remote = gm.remote_head(g, d, r.branch)
            except GoError as err:
                raise err.wrap("%s: reading origin's %s" % (rp, r.branch)) from None
            if remote != r.origin:
                raise Refused(
                    "sync refused: origin's %s (%s) is at %s, not at %s where this sync found it — something pushed to it"
                    " while the sync ran; put it back (delete it when that was none), then run sync.py again" % (
                        r.branch, rp, gostr.or_text(gostr.short(remote), "none"), gostr.or_text(gostr.short(r.origin), "none")))
        if head == r.after and r.after != "":
            return
        if head != r.before:
            try:
                work = self.work_since(d, r, head)
            except GoError as err:
                raise Refused(
                    "sync refused: %s: HEAD %s is neither where the sync started (%s) nor a merge it recorded, and the"
                    " commits since cannot be read (%s) — nothing is reset; run `sync.py --abort` to drop the sync's"
                    " record, then sync.py again" % (rp, gostr.short(head), gostr.short(r.before), err)) from None
            if work:
                raise Refused(
                    "sync refused: %s: HEAD %s moved past where the sync started (%s) with commits that are not its merge"
                    " (%s) — work committed on top of the sync's record; sync.py never discards it: run `sync.py"
                    " --abort` to drop the record (the commits stay), then sync.py again" % (
                        rp, gostr.short(head), gostr.short(r.before), ", ".join(work)))
            ref = DISCARDED_PREFIX + _nano_stamp()
            try:
                g.git(d, "update-ref", ref, head)
            except GoError as err:
                raise err.wrap("%s: keeping %s under %s" % (rp, gostr.short(head), ref)) from None
            try:
                g.git(d, "reset", "-q", "--hard", r.before)
            except GoError as err:
                raise err.wrap("%s: resetting the branch to %s" % (rp, gostr.short(r.before))) from None
            r.discarded.append(Discarded(commit=head, ref=ref))
            inv.say("%s: HEAD %s is neither where the sync started (%s) nor a merge it recorded — reset to %s, the commit"
                    " kept under %s" % (rp, gostr.short(head), gostr.short(r.before), gostr.short(r.before), ref))
        # the repo starts over from before
        r.after, r.fast_forwarded = "", False
        r.mechanical = r.conflicts = r.resolved = r.notes = None
        r.mechanical_blobs = None

    def work_since(self, d: str, r: SyncRepo, head: str) -> List[str]:
        """The commits HEAD carries past r.before that are neither merges nor
        the default branch's (short names, newest first)."""
        default = r.default_branch or self.g.default_branch(d)
        out = self.g.git(d, "rev-list", "--no-merges", r.before + ".." + head, "--not", "refs/remotes/origin/" + default)
        return [gostr.short(c) for c in non_empty_lines(out)]

    def abort(self, prior: Optional[SyncRecord]) -> int:
        """``--abort``: every merge the record left in progress aborted, the
        record removed, the result written marked aborted."""
        g, inv = self.g, self.inv
        rec = prior if prior is not None else SyncRecord(record=self.record, round=inv.getenv("PF_ROUND"))
        rec.aborted = True
        inv.details = rec.to_dict
        try:
            aborted: List[str] = []
            for r in rec.repos:
                d = self.dir(r.name)
                if gm.in_progress(g, d) != "":
                    try:
                        gm.abort(g, d)
                    except GoError as err:
                        inv.summary = "repos/%s: aborting the merge in progress: %s (the record %s is kept)" % (r.name, err, self.record)
                        return 2
                if r.pending():
                    r.aborted, r.after = True, ""
                    aborted.append("repos/%s (%s)" % (r.name, ", ".join(or_empty(r.conflicts))))
                r.mechanical_blobs = None
            try:
                os.remove(self.record)
            except FileNotFoundError:
                pass
            except OSError as err:
                inv.summary = "the sync record %s could not be removed: %s" % (self.record, gostr.os_error("remove", self.record, err))
                return 2
            if prior is None:
                inv.summary = "aborted: no sync in progress, nothing to abort"
            elif not aborted:
                inv.summary = "aborted: no merge was pending; the record is removed and nothing is synced by this result — run sync.py again"
            else:
                inv.summary = ("aborted: the pending merge abandoned in %s, the record removed, nothing reset — nothing is"
                               " synced by this result; run sync.py again" % "; ".join(aborted))
            inv.say(inv.summary)
            return 0
        finally:
            inv.heads = self.heads(rec)

    def resume(self, rec: SyncRecord) -> int:
        """``--continue``: each pending repo's merge, still in progress,
        checked and committed; the record rewritten and, nothing left,
        removed."""
        g, inv = self.g, self.inv
        inv.details = rec.to_dict
        try:
            for r in rec.pending():
                d = self.dir(r.name)
                mh = gm.in_progress(g, d)
                if mh != r.default_head:
                    inv.summary = ("repos/%s: the merge of %s (%s) is no longer in progress (MERGE_HEAD %s) — run `sync.py`"
                                   " again: it starts the sync over" % (r.name, r.default_branch, gostr.short(r.default_head),
                                                                         gostr.or_text(gostr.short(mh), "absent")))
                    return 2
                try:
                    left, faults = self.tree_faults(d, r)
                except GoError as err:
                    inv.summary = "repos/%s: %s" % (r.name, err)
                    return 2
                if faults:
                    # the files resolved so far move to resolved now, so a
                    # later round's commit records every file the agent
                    # resolved; a resolved file marked again comes back
                    resolved: Optional[List[str]] = None
                    for f in or_empty(r.resolved) + or_empty(r.conflicts):
                        if f not in left:
                            resolved = append_new(resolved, f)
                    r.resolved, r.conflicts, r.notes = or_empty(resolved), left, append_new(r.notes, *faults)
                    try:
                        rec.save()
                    except OSError as err:
                        inv.summary = "%s not written: %s" % (self.record, err)
                        return 2
                    continue
                try:
                    after = gm.commit(g, d)
                except GoError as err:
                    inv.summary = "repos/%s: committing the resolved merge: %s" % (r.name, err)
                    return 2
                r.after, r.resolved = after, append_new(r.resolved, *or_empty(r.conflicts))
                r.conflicts, r.mechanical_blobs = [], None
                try:
                    rec.save()
                except OSError as err:
                    inv.summary = "%s not written: %s" % (self.record, err)
                    return 2
                inv.say("repos/%s: the resolved merge of %s committed as %s (resolved: %s)" % (
                    r.name, r.default_branch, gostr.short(after), ", ".join(or_empty(r.resolved))))
            pend = rec.pending()
            if pend:
                lines = ["repos/%s: %s" % (r.name, "; ".join(or_empty(r.notes))) for r in pend]
                inv.summary = ("%d repo(s) still unresolved — fix what each note names, then run `sync.py --continue` again:"
                               " %s" % (len(pend), " | ".join(lines)))
                return 1
            return self.settle(rec)
        finally:
            inv.heads = self.heads(rec)

    def tree_faults(self, d: str, r: SyncRepo) -> Tuple[List[str], List[str]]:
        """What keeps one repo's resolved merge from being committed — an
        unmerged path, an unstaged change, a conflict marker left in a
        conflicted file, a conflicted text file whose staged result drops a
        line both parents carry (a file only one parent has is not checked;
        one removed from the index reads as empty), a mechanically resolved
        file changed — and the conflicted files still unresolved."""
        g = self.g
        left: Optional[List[str]] = None
        faults: List[str] = []
        try:
            unmerged = gm.conflicted(g, d)
        except GoError as err:
            raise err.wrap("reading the unmerged paths") from None
        if unmerged:
            faults.append("unmerged paths remain: %s — resolve each one and `git add` it" % ", ".join(unmerged))
            left = append_new(left, *unmerged)
        try:
            out = g.git(d, "diff", "--name-only", "-z")
        except GoError as err:
            raise err.wrap("reading the unstaged changes") from None
        if out != "":
            faults.append("changes not staged for the merge commit: %s — `git add` your resolution, or restore what you"
                          " did not mean to change" % ", ".join(paths(out)))
        # the files the agent resolves: the pending ones and those an earlier
        # --continue round accepted (one marked again comes back)
        for f in or_empty(r.conflicts) + or_empty(r.resolved):
            try:
                with open(os.path.join(d, f), "rb") as fh:
                    raw = fh.read()
            except OSError:
                continue
            if has_conflict_marker(raw):
                faults.append("%s still carries a conflict marker line (<<<<<<<, ||||||| or >>>>>>>)" % f)
                left = append_new(left, f)
        for f in or_empty(r.conflicts) + or_empty(r.resolved):
            if f in unmerged:
                continue
            ours, theirs = show(g, d, "HEAD", f), show(g, d, "MERGE_HEAD", f)
            if ours is None or theirs is None:
                continue
            gone = dropped_lines(ours, theirs, show(g, d, "", f) or b"")
            if gone:
                eg = ", ".join('"%s"' % x.decode("utf-8", "replace")[:120] for x in gone[:3])
                faults.append("%s drops %d line(s) both parents carry (HEAD and MERGE_HEAD), e.g. %s — put them back, or"
                              " ask the owner when dropping them is the resolution" % (f, len(gone), eg))
                left = append_new(left, f)
        for f in sorted(r.mechanical_blobs or {}):
            blob = (r.mechanical_blobs or {})[f]
            try:
                staged = g.git(d, "rev-parse", ":" + f)
            except GoError as err:
                staged = _stdout_of(err)
            try:
                work = g.git(d, "hash-object", "--", f)
            except GoError as err:
                work = _stdout_of(err)
            if staged.strip() != blob or work.strip() != blob:
                faults.append("%s was resolved mechanically (the default branch's integrate.conf) and must stay"
                              " byte-identical to that resolution — restore it: `git cat-file blob %s > %s && git add %s`" % (f, blob, f, f))
        if faults and not left:
            left = list(or_empty(r.conflicts))
        return or_empty(left), faults


def _stdout_of(err: GoError) -> str:
    out = getattr(err, "stdout", b"")
    return out.decode("utf-8", "surrogateescape") if isinstance(out, bytes) else ""


def _nano_stamp() -> str:
    """Now in UTC to the nanosecond: 20060102T150405.000000000Z."""
    ns = time.time_ns()
    t = datetime.datetime.fromtimestamp(ns // 1_000_000_000, datetime.timezone.utc)
    return t.strftime("%Y%m%dT%H%M%S") + ".%09dZ" % (ns % 1_000_000_000)


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Merge origin's default branch into every task repo of the capsule, in place.")
    ap.add_argument("--capsule", default=".", help="the capsule root (its repos/<name> are the clones)")
    ap.add_argument("--continue", dest="cont", action="store_true",
                    help="commit the merges whose real conflicts were resolved in place, and rewrite the result; with no sync"
                         " in progress it refuses (exit 2) and leaves the result at --out as it is — run sync.py again to sync again")
    ap.add_argument("--abort", action="store_true",
                    help="abandon the sync: abort every merge its record left in progress, keep the merges it committed, remove"
                         " the record and write the result marked aborted (nothing reset); with no sync in progress it aborts nothing")
    ap.add_argument("--deadline", type=duration_flag, default=0,
                    help="the whole run's deadline, a Go duration (default: derived from the step's tool window, two minutes under it)")
    ap.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    ap.add_argument("--out", default="", help="write the result file there (atomically)")
    ap.add_argument("rest", nargs="*", help=argparse.SUPPRESS)
    return ap


def run(inv: Invocation) -> int:
    a = parser().parse_args(inv.argv)
    # out and json are set once the invocation is one that may write or print
    # the result: a --continue with no sync in progress is refused BEFORE
    # it, so the settled result at --out (the file the next step reads) is
    # left as it is and stdout stays empty
    if not a.cont:
        inv.out, inv.json = a.out, a.json
    if a.rest:
        return inv.usage("unexpected arguments %s" % go_list(a.rest))
    if a.cont and a.abort:
        return inv.usage("--continue and --abort exclude each other")
    try:
        capsule, names = capsule_repos(a.capsule)
    except GoError as err:
        return inv.usage(str(err))
    if not names:
        return inv.usage("no task repo under %s/repos (run from the capsule root, or name it with --capsule)" % capsule)
    record = os.path.join(capsule, "artifacts", "sync", RECORD_NAME)
    if a.out:
        record = os.path.abspath(os.path.join(os.path.dirname(a.out), RECORD_NAME))
    ctx = Ctx(inv.deadline(a.deadline) / 1e9)
    s = Sync(inv, Git(ctx, inv.env), capsule, record)
    try:
        prior = read_record(record)
    except GoError as err:
        return inv.usage(str(err))
    if a.cont:
        if prior is None:
            return inv.usage("no sync in progress (no record at %s): the result at --out is left as it is; run `sync.py` to sync again" % record)
        if not prior.pending():
            return inv.usage("nothing to continue: the record %s holds no pending conflict (the sync settled; the result at --out"
                             " is left as it is; run `sync.py` to sync again)" % record)
        inv.out, inv.json = a.out, a.json
        return s.resume(prior)
    if a.abort:
        return s.abort(prior)
    rec, fail = s.run_repos(names, prior)
    if rec is not None:
        inv.details = rec.to_dict
        inv.heads = s.heads(rec)
    if ctx.err() is not None:
        inv.summary = "interrupted (%s): the sync is not complete; run sync.py again" % ctx.err()
        return 2
    if isinstance(fail, Refused):
        inv.summary = str(fail)
        return 2
    if fail is not None:
        inv.summary = "the sync could not run (an infrastructure failure, never this change's fault): %s" % fail
        return 2
    assert rec is not None
    return s.settle(rec)


def main(argv: Optional[List[str]] = None, environ: Optional[Mapping[str, str]] = None) -> int:
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    runner.raise_on_termination()
    try:
        code = run(inv)
    except (KeyboardInterrupt, runner.Interrupted):
        inv.summary = "interrupted (context canceled): the sync is not complete; run sync.py again"
        code = 2
    except Exception as err:  # a bug, never a verdict: said, and not judged
        traceback.print_exc()
        inv.summary = "the sync could not run (an infrastructure failure, never this change's fault): %s" % err
        code = 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
