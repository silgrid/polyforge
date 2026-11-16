"""The human merge's landing rules (polyforge#36): when a pull request a
person merged on the forge counts as the change landed on the default
branch, and as WHAT it landed — the commit set and whether each is a
merge commit. Only the human-merge mode applies them (the lane's own
merge keeps the default landing rules); nothing here touches the forge
beyond reading what the caller fetched."""

from dataclasses import dataclass
from typing import List, Tuple

from .ctx import GoError
from .git import Git
from .gostr import short


@dataclass(frozen=True)
class Landed:
    """One commit a change landed as on the default branch, and whether
    it is a merge commit (a plain commit: a rebase's replay or a
    squash)."""

    sha: str
    merge: bool


def human_landing(g: Git, cwd: str, base: str, head: str, merge: str) -> Tuple[List[Landed], str]:
    """Why merge — the forge's mergeCommit of a PR a person merged — is
    not the change at the verified head landed on origin's base: "" and
    the commit set it landed as when it is (each with its merge flag, the
    oldest first). The default branch is fetched.

    The fingerprint is the patch-id (``git patch-id --stable``, what
    ``git cherry`` compares): a merge commit must carry the verified head
    as its branch side; a rebase landing must replay every commit of the
    branch — a consecutive run of patch-id matches on the default
    branch, walking the merge commit's first parents; a squash landing
    must carry the branch's whole diff since its fork point (the
    merge-base of the head and the landing — the branch's own commits,
    however far the default branch moved meanwhile). Anything else — a
    commit added to the branch before the person merged, a content
    changed on the way — is a mismatch, the caller's to ask the owner
    about."""
    if merge == "":
        return [], "the forge names no merge commit for it"
    g.fetch(cwd, base)
    try:
        g.rev(cwd, merge)
        on_base = g.is_ancestor(cwd, merge, "refs/remotes/origin/" + base)
    except GoError:
        on_base = False
    if not on_base:
        return [], "its merge commit %s is not on %s" % (short(merge), base)
    # 1. a merge commit: its branch side is the verified head, exactly
    # the lane's own landing shape (a side that moved — a commit added to
    # the branch before the person merged — is a mismatch)
    try:
        side = g.rev(cwd, merge + "^2")
    except GoError:
        side = ""
    if side != "":
        if side == head:
            return [Landed(merge, True)], ""
        return [], ("its merge commit %s merged %s, which is not the verified head %s — the branch"
                    " moved before the person merged" % (short(merge), short(side), short(head)))
    # 2. the branch's own commits since its fork point with the landing
    fork = g.git(cwd, "merge-base", head, merge).strip()
    if fork == "":
        return [], ("the landing %s and the verified head %s share no fork point"
                    % (short(merge), short(head)))
    pids: List[Tuple[str, str]] = []
    for c in g.git(cwd, "rev-list", "--reverse", "--no-merges", fork + ".." + head).split():
        pid = g.patch_id(cwd, c)
        if pid != "":
            pids.append((c, pid))
    # 3. a rebase landing: a consecutive run of patch-id matches from the
    # merge commit down its first-parent chain, covering every one of the
    # branch's commits
    rest = list(pids)
    run: List[str] = []
    at = merge
    while rest and g.is_ancestor(cwd, fork, at):
        pid = g.patch_id(cwd, at)
        hit = -1
        for i, (_, p) in enumerate(rest):
            if p == pid:
                hit = i
                break
        if hit < 0:
            break
        run.append(at)
        del rest[hit]
        try:
            at = g.rev(cwd, at + "^")
        except GoError:
            break
    if not rest and run:
        return [Landed(c, False) for c in reversed(run)], ""
    # 4. a squash landing: the whole diff of the branch since its fork
    # point, on the landing's parent
    parent = g.rev(cwd, merge + "^")
    if pids and g.patch_id(cwd, fork, head) == g.patch_id(cwd, parent, merge):
        return [Landed(merge, False)], ""
    return [], ("what the person merged does not match the verified change at %s — the branch moved"
                " (a commit added before the merge) or the content changed on the way" % short(head))
