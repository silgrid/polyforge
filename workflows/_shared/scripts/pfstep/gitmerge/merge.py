"""Merging a default branch's head into the branch checked out in a clone,
in place (``sync``), and reading a merge in progress."""

from dataclasses import dataclass, field
from typing import List, Optional

from .conf import CONF_REL, Conf, go_clean, parse_conf
from .ctx import GoError
from .git import Git, paths
from .gostr import quote
from .resolve import Scope, resolve


@dataclass
class Synced:
    """One merge of a default branch's head into the branch checked out.

    base is the default branch, main the commit merged in, frm the HEAD the
    merge started from; updated the merge commit ("" = HEAD already
    contained main, or a real conflict left the merge in progress);
    resolved the files resolved mechanically; conflicts the files with a
    real conflict (the merge left in progress); notes why a declared file
    among conflicts could not be settled mechanically."""

    base: str
    main: str
    frm: str = ""
    updated: str = ""
    resolved: List[str] = field(default_factory=list)
    conflicts: List[str] = field(default_factory=list)
    notes: List[str] = field(default_factory=list)


def conflicted(g: Git, cwd: str) -> List[str]:
    """The unmerged paths of a merge in progress (none = no conflict left,
    or no merge in progress)."""
    return paths(g.git(cwd, "diff", "--name-only", "-z", "--diff-filter=U"))


def commit(g: Git, cwd: str) -> str:
    """A merge in progress whose conflicts are all resolved and staged,
    concluded; the merge commit."""
    left = conflicted(g, cwd)
    if left:
        raise GoError("unresolved conflicts remain: %s" % ", ".join(left))
    g.git(cwd, "commit", "-q", "--no-edit")
    return g.rev(cwd, "HEAD")


def abort(g: Git, cwd: str) -> None:
    """Abandon a merge in progress."""
    g.git(cwd, "merge", "--abort")


def in_progress(g: Git, cwd: str) -> str:
    """The commit a merge in progress merges (MERGE_HEAD; "" = none)."""
    try:
        return g.git(cwd, "rev-parse", "-q", "--verify", "MERGE_HEAD").strip()
    except GoError:
        return ""


def linked(g: Git, cwd: str) -> bool:
    """Whether the clone is a linked worktree — its git dir is not its
    common dir — whose $GIT_DIR/info/attributes (the override every merge
    writes) would be the common dir's, shared with every other worktree."""
    out = g.git(cwd, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
    f = out.split()
    if len(f) != 2:
        raise GoError("git rev-parse --git-dir --git-common-dir answered %s" % quote(out.strip()))
    return go_clean(f[0]) != go_clean(f[1])


def remote_head(g: Git, cwd: str, branch: str) -> str:
    """The commit origin's branch is at, asking origin itself (``git
    ls-remote``; "" = no such branch)."""
    out = g.git(cwd, "ls-remote", "origin", "refs/heads/" + branch)
    for line in out.split("\n"):
        sha, sep, ref = line.strip().partition("\t")
        if sep and ref == "refs/heads/" + branch:
            return sha
    return ""


def conf_at(g: Git, cwd: str, rev: str) -> Conf:
    """The conf a revision's tree carries (a tree without one = an empty
    conf): the default branch's, so a change never widens its own
    mechanical resolution."""
    try:
        g.git(cwd, "cat-file", "-e", rev + ":" + CONF_REL)
    except GoError:
        try:
            g.rev(cwd, rev)
        except GoError as err:
            raise err.wrap("reading %s at %s" % (CONF_REL, rev)) from None
        return Conf()
    return parse_conf(g.cmd(cwd, "git", "show", rev + ":" + CONF_REL))


def sync(g: Git, cwd: str, base: str, main: str, conf: Optional[Conf], msg: str) -> Synced:
    """main — the head of the default branch base, fetched — merged into the
    branch checked out in the clone, in place: nothing when HEAD already
    contains main; else a ``--no-ff`` merge with message msg, every in-tree
    merge attribute overridden (pin_attributes), the conflicts conf declares
    mechanical resolved (resolve.py) and the merge committed as the lane's
    identity. A real conflict leaves the merge in progress and names its
    files (conflicts): the caller resolves and commits it (commit) or
    abandons it (abort). It fetches nothing and pushes nothing."""
    s = Synced(base=base, main=main)
    s.frm = g.rev(cwd, "HEAD")
    if g.is_ancestor(cwd, main, s.frm):
        return s
    g.pin_attributes(cwd)
    try:
        g.git(cwd, "merge", "--no-ff", "--no-edit", "-m", msg, main)
    except GoError:
        pass
    else:
        s.updated = g.rev(cwd, "HEAD")
        return s
    files = conflicted(g, cwd)
    if not files:
        try:
            abort(g, cwd)
        except GoError:
            pass
        raise GoError("merging %s into HEAD %s failed without a conflict" % (base, s.frm))
    sides = ["HEAD", main]
    try:
        sides.append(g.git(cwd, "merge-base", "HEAD", main).strip())
    except GoError:
        pass
    try:
        s.resolved, s.conflicts, s.notes = resolve(g, cwd, conf, files, Scope(main=main, sides=sides))
    except Exception:
        try:
            abort(g, cwd)
        except GoError:
            pass
        raise
    if s.conflicts:
        return s
    s.updated = commit(g, cwd)
    return s


def merged_from(g: Git, cwd: str, base: str, merge: str, heads: List[str]) -> str:
    """Why merge — a PR's merge commit as the forge names it — is not the
    PR's change landed: "" when it is on origin's base and its branch side
    (its second parent) is one of heads. The default branch is fetched."""
    if merge == "":
        return "the forge names no merge commit for it"
    g.fetch(cwd, base)
    try:
        g.rev(cwd, merge)
        on_base = g.is_ancestor(cwd, merge, "refs/remotes/origin/" + base)
    except GoError:
        on_base = False
    if not on_base:
        return "its merge commit %s is not on %s" % (merge, base)
    try:
        side = g.rev(cwd, merge + "^2")
    except GoError:
        return ("its merge commit %s has no branch side (a squash or a rebase merge): what landed cannot be told from"
                " the published change" % merge)
    if side not in heads:
        return "its merge commit %s merged %s, which is neither the published head nor an update the lane recorded" % (merge, side)
    return ""
