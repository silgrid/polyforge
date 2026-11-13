"""The landing rules: when a pull request the lane did not merge itself
counts as its change landed on the default branch. The merge script
applies them; nothing here touches the forge beyond reading what the caller
fetched."""

from typing import List, Tuple

from .git import Git
from .gostr import short
from .merge import merged_from


def landing_rule(g: Git, cwd: str, base: str, merge: str, head: str) -> str:
    """The mechanical rule that takes a PR merged outside the lane as the
    change's landing: its merge commit is on origin's base and contains
    head — the head the lane verified. Why it refuses ("" = it holds); the
    caller fetched the default branch."""
    if merge == "":
        return "the forge names no merge commit for it"
    if not g.is_ancestor(cwd, merge, "refs/remotes/origin/" + base):
        return "its merge commit %s is not on %s" % (short(merge), base)
    if head == "":
        return ("this work item recorded no light-verified head for it to contain (and no waiver of verification that"
                " would bind the published head)")
    if not g.is_ancestor(cwd, head, merge):
        return "its merge commit %s does not contain the light-verified head %s" % (short(merge), short(head))
    return ""


def take_as_landing(g: Git, cwd: str, base: str, merge: str, heads: List[str], landing: str) -> Tuple[bool, str]:
    """A PR found MERGED, its merge commit merge, judged: (outside, refused).
    Taken as it is — outside False — when the lane made it (its branch side
    is one of heads); one made outside the lane is taken — outside True —
    only by the landing rule. refused says why neither holds ("" = taken).
    The default branch is fetched."""
    why = merged_from(g, cwd, base, merge, heads)
    if why == "":
        return False, ""
    refused = landing_rule(g, cwd, base, merge, landing)
    if refused:
        return True, why + "; " + refused
    return True, ""
