"""pfstep/children.py — the epic's children snapshot.

The daemon snapshots an epic's children into
``runtime-state/children.json`` at its first claim (the lane key ``epic``
over ``children_branch``; the file's shape is the daemon's, and every
later claim keeps it over unchanged children — a changed set writes the
next version beside the resume marker). The step scripts read it here,
one reader for all of them: merge.py attaches the children to the epic's
own landing record (the ONE record the integrate pool takes; each
child's ``landed_on_target`` evidence rides it verbatim and stays on the
child's own work item).

The read is strict (never a landing over an unreadable snapshot): a file
that is there but cannot be read, is not valid JSON, or is not the
expected object raises :class:`ChildrenError` naming why — the caller
refuses (a step's not-judged exit 2 naming the reason). A capsule
without the file is not an epic's — the landing needs no children then:
:func:`read` answers ``None`` and the caller keeps its own shape,
unchanged.

The snapshot's children are exactly the claim's own, sorted by seq; each
entry carries ``id``, ``slug``, ``status`` and the ``landing`` record the
hub held (JSON ``null`` when the child carried none).
"""

import json
import os
from typing import Any, Dict, Optional

# The fixed file's place under the capsule root.
DIR = "runtime-state"
FILE = "children.json"


class ChildrenError(Exception):
    """runtime-state/children.json is there but cannot be read as the
    epic's children snapshot (the message names why)."""


def path_of(capsule_dir: str) -> str:
    """The snapshot's path under the capsule root."""
    return os.path.join(capsule_dir, DIR, FILE)


def read(capsule_dir: str) -> Optional[Dict[str, Any]]:
    """The capsule's children snapshot (``None`` = the capsule carries
    none: not an epic's): the parsed object, its ``version`` a positive
    number and its ``children`` a list of objects. A file that is there
    but unreadable, not valid JSON, or not the expected shape raises
    :class:`ChildrenError` naming why."""
    try:
        with open(path_of(capsule_dir), "rb") as f:
            raw = f.read()
    except FileNotFoundError:
        return None
    except OSError as err:
        raise ChildrenError("runtime-state/%s is unreadable: %s" % (FILE, err)) from None
    try:
        doc: Any = json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        raise ChildrenError("runtime-state/%s is not valid JSON: %s" % (FILE, err)) from None
    if not isinstance(doc, dict):
        raise ChildrenError("runtime-state/%s is not the JSON object expected" % FILE) from None
    version = doc.get("version")
    children = doc.get("children")
    if not isinstance(version, int) or isinstance(version, bool) or version < 1:
        raise ChildrenError("runtime-state/%s: version is not a positive number" % FILE) from None
    if not isinstance(children, list):
        raise ChildrenError("runtime-state/%s: children is not the list expected" % FILE) from None
    for child in children:
        if not isinstance(child, dict):
            raise ChildrenError("runtime-state/%s: a child is not the object expected" % FILE) from None
    return doc
