"""The unit runs a capsule's build recorded (units.json) — their one
declaration, the file's parsing, and the marker rules that judge a green
run's output: the one implementation both verify.py (the ``--unit-runs``
check) and fast_checks.py (build's on-the-spot check at record time) use,
so no second copy of the rules can drift.
"""

import json
import os
import re
from dataclasses import dataclass, field
from typing import Any, List, Sequence, Tuple

from . import gostr


@dataclass
class UnitRunDecl:
    """One unit's recorded run (units.json)."""

    id: str
    repo: str = ""
    cwd: str = ""
    argv: List[str] = field(default_factory=list)
    marker: str = ""
    test_commit: str = ""

    def check(self) -> str:
        return self.repo + ":unit-run:" + self.id


def read_unit_runs(path: str) -> List[UnitRunDecl]:
    """units.json as unit runs (a unit without a run is one with an empty
    argv: its check fails, never skips). ValueError when it is unusable."""
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as err:
        raise ValueError(str(err)) from None
    shape = "a JSON array of {id, test_commit, impl_commit, run{repo, cwd, argv}}"
    try:
        doc = json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        raise ValueError("%s is not %s: %s" % (path, shape, err)) from None

    def text(v: Any, what: str) -> str:
        if v is None:
            return ""
        if not isinstance(v, str):
            raise ValueError("%s is not %s: %s is not a string" % (path, shape, what))
        return v

    if doc is None:
        return []
    if not isinstance(doc, list):
        raise ValueError("%s is not %s: it is not an array" % (path, shape))
    out: List[UnitRunDecl] = []
    for i, u in enumerate(doc):
        if u is None:
            u = {}
        if not isinstance(u, dict):
            raise ValueError("%s is not %s: entry %d is not an object" % (path, shape, i))
        d = UnitRunDecl(id=text(u.get("id"), "id"), marker=text(u.get("marker"), "marker"),
                        test_commit=text(u.get("test_commit"), "test_commit"))
        text(u.get("impl_commit"), "impl_commit")
        r = u.get("run")
        if r is not None:
            if not isinstance(r, dict):
                raise ValueError("%s is not %s: entry %d's run is not an object" % (path, shape, i))
            d.repo, d.cwd = text(r.get("repo"), "run.repo"), text(r.get("cwd"), "run.cwd")
            argv = r.get("argv")
            if argv is not None:
                if not isinstance(argv, list) or not all(isinstance(a, str) for a in argv):
                    raise ValueError("%s is not %s: entry %d's run.argv is not an array of strings" % (path, shape, i))
                d.argv = list(argv)
        out.append(d)
    return out


def clean_cwd(cwd: str) -> Tuple[str, str]:
    """A unit run's cwd cleaned ("." = the root), or the problem with it."""
    c = gostr.path_clean(gostr.trim_space(cwd))
    if gostr.trim_space(cwd) == "" or c == ".":
        return ".", ""
    if c.startswith("/") or c == ".." or c.startswith("../"):
        return "", "its cwd %s is not a directory inside the repo" % gostr.quote(cwd)
    return c, ""


def marker_problem(marker: str) -> str:
    """Why a unit run's marker proves no pass ("" = it can): none, blank,
    no Python regular expression, or one the empty string matches."""
    if gostr.trim_space(marker) == "":
        return "it names no marker (units.json marker is empty): exit 0 alone proves nothing — a filter that ran no test exits 0 too"
    try:
        r = re.compile(marker, re.ASCII)
    except re.error as err:
        return "its marker %s is not a regexp: %s" % (gostr.quote(marker), err)
    if r.search("") is not None:
        return "its marker %s matches the empty string: every output would show it, so it proves no pass" % gostr.quote(marker)
    return ""


def marker_matches(marker: str, lines: Sequence[str]) -> bool:
    """Whether one output line matches the marker (searched, re.ASCII)."""
    try:
        r = re.compile(marker, re.ASCII)
    except re.error:
        return False
    return any(r.search(ln) for ln in lines)
