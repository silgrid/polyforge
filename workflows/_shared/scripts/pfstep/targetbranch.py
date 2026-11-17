"""pfstep/targetbranch.py — the capsule's fixed target branch.

The daemon fixes a sub-ticket's target branch into
``runtime-state/target-branch.json`` at its first claim (the lane key
``children_branch``; the file's shape and its write are the daemon's, and
every later claim keeps the fixed value). The step scripts read it here,
one reader for all of them: the branch the change is based on and merged
into — sync's merge, merge's base, the verification's diff base, the
review facts' change base and the scope engine's default base all take
it in the place of the origin's default branch. A capsule without the
file, or with an empty ``target``, has none: every reader keeps its own
default-branch logic, unchanged.

The read is strict (never a silent fall-back to the default branch): a
file that is there but cannot be read, or is not the JSON object with a
string ``target``, raises :class:`TargetBranchError` naming why — the
caller refuses (a step's not-judged exit 2 naming the reason). Unknown
keys are tolerated: an EPIC's own record (polyforge#117) carries a
``checkout`` key (the branch of its own seq it works on) and NO
``target`` — the epic merges into the origin's default branch, so every
reader's default-branch logic is its base, exactly as for a capsule
without a fixed target; the ``checkout`` key itself is the daemon's own
(the runtime's checkout), no step script reads it here.
"""

import json
import os
from typing import Any
# The fixed file's place under the capsule root.
DIR = "runtime-state"
FILE = "target-branch.json"


class TargetBranchError(Exception):
    """runtime-state/target-branch.json is there but cannot be read as the
    fixed target branch (the message names why)."""


def path_of(capsule_dir: str) -> str:
    """The fixed file's path under the capsule root."""
    return os.path.join(capsule_dir, DIR, FILE)


def read(capsule_dir: str) -> str:
    """The capsule's fixed target branch ("" = none): the file's ``target``
    key, stripped; a missing file, a missing key or an empty value is no
    target. A file that is there but unreadable, or not the JSON object
    with a string ``target``, raises TargetBranchError naming why."""
    try:
        with open(path_of(capsule_dir), "rb") as f:
            raw = f.read()
    except FileNotFoundError:
        return ""
    except OSError as err:
        raise TargetBranchError("runtime-state/%s is unreadable: %s" % (FILE, err)) from None
    try:
        doc: Any = json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        raise TargetBranchError("runtime-state/%s is not valid JSON: %s" % (FILE, err)) from None
    if not isinstance(doc, dict):
        raise TargetBranchError("runtime-state/%s is not the JSON object expected" % FILE) from None
    target = doc.get("target")
    if target is None:
        return ""
    if not isinstance(target, str):
        raise TargetBranchError("runtime-state/%s: target is not a string" % FILE) from None
    return target.strip()
