"""Reverting a source item's merges on the default branch (the owner's
``revert`` ruling), mechanically and resumably.

- ``revert_merge`` reverts one merge (``git revert -m 1``) with the conf's
  mechanical rules turned to a revert: an append-only file keeps the default
  branch's bytes (a revert never rewrites the log), a holder's conflicting
  pin lines and declared list lines keep the default branch's side, and
  every pin of a kept file is set to that file's hash. Any other conflict is
  a real one: the revert is abandoned.
- ``revert`` opens and merges one PR reverting several merges, and picks up
  an earlier attempt: an existing PR of the revert branch is merged (or
  read back merged), an existing branch is built on, and a merge already
  reverted (on the branch or on the default branch) is skipped.
"""

import os
from dataclasses import dataclass, field
from typing import Dict, List, Optional, Set, Tuple

from .conf import Conf, ListDecl
from .ctx import GoError
from .git import Git
from .gostr import quote, trim_text
from .merge import conflicted
from .resolve import (Scope, Seg, all_entries, known_pins, masked_equal, merge_hunks, pin_hash, read_file,
                      regions_of, replace_in, show, stage, write_file)


def revert_branch(name: str) -> str:
    """The revert branch of the source item name (its slug, else its id):
    integrate/revert-<name>, a slug's ``#`` made a hyphen."""
    return "integrate/revert-" + name.replace("#", "-")


def reverted(g: Git, cwd: str, m: str, rev: str) -> bool:
    """Whether the merge m is already reverted in rev's history (a commit
    since m saying so)."""
    try:
        out = g.git(cwd, "log", "--format=%H", "-F", "--grep", "This reverts commit " + m, m + ".." + rev)
    except GoError:
        return False
    return out.strip() != ""


def newest_first(g: Git, cwd: str, base: str, merges: List[str]) -> List[str]:
    """merges — merge commits on origin's base — newest first over base's
    first-parent history since the oldest of them: the order revert reverts
    them in. The default branch is as fetched."""
    if not merges:
        return []
    oldest = merges[0]
    for m in merges[1:]:
        if g.is_ancestor(cwd, m, oldest):
            oldest = m
    fp = g.first_parents(cwd, oldest + "~1", "refs/remotes/origin/" + base)
    pos = {c: i for i, c in enumerate(fp)}
    return sorted(merges, key=lambda c: -pos.get(c, 0))


def holder_revert(g: Git, cwd: str, p: str, known: Set[str], lists: List[ListDecl]) -> bool:
    """A holder conflicted by a revert settled hunk by hunk: a hunk that
    differs only in the declared pins' hashes, or lies inside a declared
    list's region with every line of every side one of the list's, keeps
    HEAD's side; anything else is a real conflict (False)."""
    base, head, target = stage(g, cwd, 1, p), stage(g, cwd, 2, p), stage(g, cwd, 3, p)
    if base is None or head is None or target is None:
        return False

    def settle(segs: List[Seg], i: int) -> Optional[List[bytes]]:
        h = segs[i].h
        assert h is not None
        if masked_equal(h.br, h.mn, known):
            return list(h.br)
        for lst in regions_of(lists, segs, i):
            if all_entries(lst, h.br) and all_entries(lst, h.base) and all_entries(lst, h.mn):
                return list(h.br)
        return None

    merged = merge_hunks(g, head, base, target, settle)
    if merged is None:
        return False
    write_file(os.path.join(cwd, p), merged)
    return True


@dataclass
class _Kept:
    file: str
    olds: List[bytes] = field(default_factory=list)
    now: bytes = b""


def keep_log(g: Git, cwd: str, conf: Conf, main: str, m: str, files: List[str]) -> List[str]:
    """The revert's mechanical rules applied to a revert in progress; the
    conflicted paths left (real conflicts)."""
    rs = Scope(main=main, sides=["HEAD", m, m + "^1"])
    done: Dict[str, bool] = {}
    keeps: List[_Kept] = []
    for p in conf.append_only:
        head = show(g, cwd, "HEAD", p)
        if head is None:
            continue
        olds: List[bytes] = []
        if p in files:
            for n in (1, 2, 3):
                b = stage(g, cwd, n, p)
                if b is not None:
                    olds.append(b)
        else:
            try:
                with open(os.path.join(cwd, p), "rb") as f:
                    wt = f.read()
            except OSError:
                continue
            if wt == head:
                continue  # the revert left the log alone
            olds.append(wt)
        for rev in (m, m + "^1"):
            b = show(g, cwd, rev, p)
            if b is not None:
                olds.append(b)
        write_file(os.path.join(cwd, p), head)
        g.git(cwd, "add", "--", p)
        done[p] = True
        keeps.append(_Kept(file=p, olds=olds, now=head))
    for p in files:
        if done.get(p) or not conf.holder(p):
            continue
        known = known_pins(g, cwd, conf, p, rs)
        if holder_revert(g, cwd, p, known, conf.lists_of(p)):
            done[p] = True
            g.git(cwd, "add", "--", p)
    for k in keeps:
        for pin in conf.pins:
            if pin.file != k.file or (pin.holder in files and not done.get(pin.holder)):
                continue
            nv = pin_hash(g, cwd, conf, main, pin, k.now)
            old: List[str] = []
            new: List[str] = []
            for o in k.olds:
                old.append(pin_hash(g, cwd, conf, main, pin, o))
                new.append(nv)
            replace_in(g, cwd, pin.holder, old, new)
    return [p for p in files if not done.get(p)]


def revert_merge(g: Git, cwd: str, conf: Optional[Conf], main: str, m: str) -> None:
    """The merge commit m reverted onto HEAD and committed (the module's
    rules), every in-tree merge attribute overridden; main is the default
    branch's commit."""
    if conf is None:
        conf = Conf()
    g.pin_attributes(cwd)
    try:
        subject = g.git(cwd, "log", "-1", "--format=%s", m)
    except GoError:
        subject = ""
    msg = "Revert %s\n\nThis reverts commit %s.\n" % (quote(trim_text(subject)), m)
    rerr: Optional[GoError] = None
    try:
        g.git(cwd, "revert", "--no-commit", "-m", "1", m)
    except GoError as err:
        rerr = err
    files = conflicted(g, cwd)
    if rerr is not None and not files:
        _abort_revert(g, cwd)
        raise rerr
    try:
        remaining = keep_log(g, cwd, conf, main, m, files)
    except Exception:
        _abort_revert(g, cwd)
        raise
    if remaining:
        _abort_revert(g, cwd)
        raise GoError("reverting %s conflicts in code: %s" % (m, ", ".join(remaining)))
    g.git(cwd, "commit", "-q", "--allow-empty", "-m", msg)
    try:
        g.git(cwd, "revert", "--quit")
    except GoError:
        pass


def _abort_revert(g: Git, cwd: str) -> None:
    try:
        g.git(cwd, "revert", "--abort")
    except GoError:
        pass


def revert(g: Git, cwd: str, base: str, conf: Optional[Conf], merges: List[str], branch: str, title: str,
           body: str) -> Tuple[str, str, Optional[GoError]]:
    """Open and merge a PR reverting merges (merge commits on base, newest
    first): the revert branch (origin's, else from origin's base), one
    revert_merge per merge not yet reverted, a push, the PR and its merge
    (matching the pushed head). An earlier attempt is picked up. Answers the
    PR's URL, the revert's merge commit (both "" when the default branch
    already reverts every merge) and the error that stopped it (None = it
    landed); the URL is set whenever a PR was had."""
    try:
        pr = g.find_pr(cwd, branch)
    except GoError as err:
        return "", "", err
    if pr is not None:
        if pr.state == "MERGED":
            if pr.merge_commit is not None:
                return pr.url, pr.merge_commit, None
        elif pr.state == "OPEN":
            try:
                g.merge_pr(cwd, pr.url, pr.head_ref_oid)
                after = g.view_pr(cwd, pr.url)
            except GoError as err:
                return pr.url, "", err
            return pr.url, after.merge_commit, None
        else:
            return pr.url, "", GoError("the revert PR %s of %s is %s: the owner's to reopen or delete" % (pr.url, branch, pr.state.lower()))
    try:
        g.fetch(cwd, base)
        start = "refs/remotes/origin/" + base
        try:
            g.fetch(cwd, branch)
            start = "refs/remotes/origin/" + branch
        except GoError:
            pass
        g.checkout(cwd, start)
        main = g.rev(cwd, "refs/remotes/origin/" + base)
        for m in merges:
            if reverted(g, cwd, m, "HEAD"):
                continue
            revert_merge(g, cwd, conf, main, m)
        head = g.rev(cwd, "HEAD")
        if head == main:
            return "", "", None  # every merge is reverted on the default branch already
        g.push(cwd, branch)
        url = g.create_pr(cwd, base, branch, title, body)
    except GoError as err:
        return "", "", err
    try:
        g.merge_pr(cwd, url, head)
        after = g.view_pr(cwd, url)
    except GoError as err:
        return url, "", err
    return url, after.merge_commit, None
