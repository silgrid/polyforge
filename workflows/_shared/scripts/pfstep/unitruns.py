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
    raise NotImplementedError


def clean_cwd(cwd: str) -> Tuple[str, str]:
    """A unit run's cwd cleaned ("." = the root), or the problem with it."""
    return "", "not implemented"


def marker_problem(marker: str) -> str:
    """Why a unit run's marker proves no pass ("" = it can): none, blank,
    no Python regular expression, or one the empty string matches."""
    return "not implemented"


def marker_matches(marker: str, lines: Sequence[str]) -> bool:
    """Whether one output line matches the marker (searched, re.ASCII)."""
    return False
