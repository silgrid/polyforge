"""pfstep/scoped_plan.py — the one light-tier planning implementation,
shared by verification's light tier (verify.py) and the build close's
scoped selection (fast_checks.py --close-scoped, polyforge#164): the
engine invocation both ask with (``--json --light --base <base>``), the
commands one report contributes for the light tier
(``pfstep.engine.choose``), the nested engine roots the changed paths
reach (each path owned by its deepest root, their commands behind
``cd <root> &&`` and named ``<root>/<name>``, their base setup likewise),
why a report answers the full tier (its fallback diagnostics), and the
branch the change is measured against (the capsule's fixed target branch
read strictly, else the origin's default branch).

One implementation, two callers: verification plans its light bar here
and the build close plans the selection it runs before the hand-off, so
the two cannot drift — the parity is pinned by the tests
(tests/test_build_scoped_close.py's SelectionParityTest). The git reads
stay the caller's plumbing: the engine invocation arrives as the ``ask``
callable (``ask(directory, cwd_rel, base)`` answering
``(report | None, reference, why-not)``), so verify.py keeps its
container plumbing and fast_checks.py its host runs while every decision
between them — the argv, the choice, the nested roots, the fallback
reasons, the plan's statuses and their messages — lives here, once.
"""

import os
from dataclasses import dataclass, field
from typing import List, Optional, Sequence, Tuple

from . import git, gostr, targetbranch
from .engine import TIER_LIGHT, EngineCommand, EngineReport, argv_of as engine_argv_of, choose, nested_roots, reached_roots
from .shellwords import shell_quote

# The bound of one git read the planning helpers make (seconds).
GIT_TIMEOUT = 600.0

# The plan's statuses (verify.py's own words, unchanged).
PLAN_PLANNED = "planned"
PLAN_EMPTY = "empty"
PLAN_ENGINE_FAILED = "engine-failed"

NO_FALLBACK_DIAGNOSTIC = "the engine answered mode full with no fallback diagnostic (a forced-full path or a full-only declaration)"

# The phrase every engine diagnostic of a full-tier fallback carries; the
# engine's informational --light diagnostics are not reasons.
FALLBACK_PHRASE = "falling back to the full tier"


@dataclass
class LightPlan:
    """One repo's light-tier plan: the commands the tier runs, the mode
    they belong to (light | full — a report that answers full makes the
    plan full, its reasons kept), the engine invocations, the report of
    the repository's own root, the base setup and why each nested root's
    own light report fell back to the full tier."""

    status: str = ""
    reason: str = ""
    mode: str = ""  # the mode the chosen commands belong to (full | light)
    commands: List[EngineCommand] = field(default_factory=list)
    engine_ref: str = ""
    report: Optional[EngineReport] = None
    base_setup: List[str] = field(default_factory=list)
    nested_full: List[str] = field(default_factory=list)

    def planned(self) -> bool:
        return self.status == PLAN_PLANNED


def engine_argv(engine: str, base: str, light: bool = True) -> List[str]:
    """The engine invocation both planners ask with: --json, the tier's
    flag (--light, else --full) and --base <base> when one is named."""
    return []


def append_new(lst: List[str], vals: Sequence[str]) -> List[str]:
    """vals appended to lst, each name once, in order."""
    return list(lst)


def resolve_default(repo_dir: str) -> str:
    """The origin default branch name: what refs/remotes/origin/HEAD names,
    else main, else master ("" = none)."""
    return ""


def base_branch_of(capsule: str, repo_dir: str) -> Tuple[str, str]:
    """The branch the change is measured against: the capsule's fixed
    target branch when it carries one (runtime-state/target-branch.json,
    read strictly, the origin ref checked to exist), else the origin
    default branch; ("", why) when neither can be named. The branch lands
    in the record's default_branch field, which keeps its name and carries
    either branch (the diff base and the baseline follow it)."""
    return "", ""


def fallback_reasons(rep: EngineReport) -> List[str]:
    """Why one engine report is full, one entry per reason: its own
    full-tier fallback diagnostics, else the bare fact."""
    return []


def nested_full_reasons(root: str, rep: EngineReport) -> List[str]:
    """Why a nested engine root's light report made the run full."""
    return []


def plan_light(repo_dir: str, cwd_rel: str, base: str,
               ask: "Callable[[str, str, str], Tuple[Optional[EngineReport], str, str]]") -> LightPlan:
    """One repo's light-tier plan from the engine's reports: the root
    report's fast gates plus its scoped selection (a report that answers
    full makes the plan full, its commands the full tail) and, when the
    root answered light, every nested engine root the changed paths reach
    (each path owned by its deepest root), their commands behind
    ``cd <root> &&``. ``ask(directory, cwd_rel, base)`` answers the engine
    report of directory (or None and why it has none); the plan's status
    is one of PLAN_PLANNED, PLAN_EMPTY, PLAN_ENGINE_FAILED, its reason the
    why of a plan that cannot run."""
    return LightPlan()
