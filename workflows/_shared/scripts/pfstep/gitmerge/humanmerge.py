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
    ``git cherry`` compares): a merge commit must have the verified head
    as its branch side; a rebase landing must replay every commit of the
    branch (a consecutive run of patch-id matches on the default branch);
    a squash landing must carry the branch's whole diff. Anything else —
    a commit added to the branch before the merge, a content changed on
    the way — is a mismatch, the caller's to ask the owner about."""
    del g, cwd, base, head, merge
    return [], "not implemented"
