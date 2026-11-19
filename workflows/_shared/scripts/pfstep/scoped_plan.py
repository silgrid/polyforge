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
from typing import Callable, List, Optional, Sequence, Tuple

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

# ask(directory, cwd_rel, base) answers the engine report of directory
# (or None and why it has none) plus the invocation's reference.
Ask = Callable[[str, str, str], Tuple[Optional[EngineReport], str, str]]


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
    argv = engine_argv_of(engine) + ["--json", "--light" if light else "--full"]
    if base:
        argv += ["--base", base]
    return argv


def append_new(lst: List[str], vals: Sequence[str]) -> List[str]:
    """vals appended to lst, each name once, in order."""
    out = list(lst)
    for v in vals:
        if v not in out:
            out.append(v)
    return out


def resolve_default(repo_dir: str) -> str:
    """The origin default branch name: what refs/remotes/origin/HEAD names,
    else main, else master ("" = none)."""
    c = git.run(repo_dir, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD", timeout=GIT_TIMEOUT)
    if c.exit_code == 0:
        b = gostr.trim_space(c.stdout.decode("utf-8", "replace"))
        if b.startswith("origin/"):
            b = b[len("origin/"):]
        if b:
            return b
    for b in ("main", "master"):
        if git.run(repo_dir, "rev-parse", "-q", "--verify", "refs/remotes/origin/" + b, timeout=GIT_TIMEOUT).exit_code == 0:
            return b
    return ""


def base_branch_of(capsule: str, repo_dir: str) -> Tuple[str, str]:
    """The branch the change is measured against: the capsule's fixed
    target branch when it carries one (runtime-state/target-branch.json,
    read strictly, the origin ref checked to exist), else the origin
    default branch; ("", why) when neither can be named. The branch lands
    in the record's default_branch field, which keeps its name and carries
    either branch (the diff base and the baseline follow it)."""
    try:
        target = targetbranch.read(capsule)
    except targetbranch.TargetBranchError as err:
        return "", str(err)
    if target:
        if git.run(repo_dir, "rev-parse", "-q", "--verify", "refs/remotes/origin/" + target,
                   timeout=GIT_TIMEOUT).exit_code != 0:
            return "", ("origin has no branch %s — the capsule's fixed target branch is missing on origin; never a"
                        " fall-back to the default branch" % target)
        return target, ""
    default = resolve_default(repo_dir)
    if default == "":
        return "", ("the origin default branch is unresolvable (no refs/remotes/origin/HEAD, origin/main or"
                    " origin/master in the clone): nothing to measure the change against")
    return default, ""


def fallback_reasons(rep: EngineReport) -> List[str]:
    """Why one engine report is full, one entry per reason: its own
    full-tier fallback diagnostics, else the bare fact."""
    why = [d for d in (rep.diagnostics or []) if FALLBACK_PHRASE in d]
    return why or [NO_FALLBACK_DIAGNOSTIC]


def nested_full_reasons(root: str, rep: EngineReport) -> List[str]:
    """Why a nested engine root's light report made the run full."""
    why = [d for d in (rep.diagnostics or []) if FALLBACK_PHRASE in d]
    if not why:
        return ["the nested engine root %s answered mode full with no fallback diagnostic (a forced-full path or a"
                " full-only declaration)" % root]
    return ["the nested engine root %s fell back to the full tier: %s" % (root, d) for d in why]


def _nested(repo_dir: str, cwd_rel: str, base: str, ask: Ask, paths: List[str]
            ) -> Tuple[List[EngineCommand], List[str], List[str], List[str], str]:
    """The nested engine roots the changed paths reach (each path owned by
    its deepest root): their commands behind ``cd <root> &&`` (named
    ``<root>/<name>``), their base setup likewise, the engine invocations,
    and — for a nested root whose own light report fell back to the full
    tier — why the run is full; the last answer is why a nested engine
    failed ("" = none did)."""
    cmds: List[EngineCommand] = []
    setup: List[str] = []
    refs: List[str] = []
    nested_full: List[str] = []
    for d in reached_roots(nested_roots(repo_dir), paths):
        rep, ref, why = ask(os.path.join(repo_dir, d), cwd_rel + "/" + d, base)
        if rep is None:
            return [], [], [], [], "the nested engine root %s: %s" % (d, why)
        refs.append(ref + " (in " + d + ")")
        chosen, mode = choose(rep, TIER_LIGHT)
        if mode != TIER_LIGHT:
            nested_full.extend(nested_full_reasons(d, rep))
        cd = "cd " + shell_quote(d) + " && "
        for c in chosen:
            cmds.append(EngineCommand(
                name=d + "/" + c.name, phase=c.phase, cmd=cd + c.cmd, requires_env=c.requires_env,
                requires_cmd=c.requires_cmd, timeout=c.timeout, report=c.report,
                rerun=cd + c.rerun if c.rerun else "", source=c.source, dir=d, inner=c.cmd,
            ))
        setup += [cd + s for s in rep.base_setup]
    return cmds, setup, refs, nested_full, ""


def plan_light(repo_dir: str, cwd_rel: str, base: str, ask: Ask) -> LightPlan:
    """One repo's light-tier plan from the engine's reports: the root
    report's fast gates plus its scoped selection (a report that answers
    full makes the plan full, its commands the full tail) and, when the
    root answered light, every nested engine root the changed paths reach
    (each path owned by its deepest root), their commands behind
    ``cd <root> &&``. ``ask(directory, cwd_rel, base)`` answers the engine
    report of directory (or None and why it has none); the plan's status
    is one of PLAN_PLANNED, PLAN_EMPTY, PLAN_ENGINE_FAILED, its reason the
    why of a plan that cannot run."""
    rep, ref, why = ask(repo_dir, cwd_rel, base)
    plan = LightPlan(engine_ref=ref, report=rep)
    if rep is None:
        plan.status, plan.reason = PLAN_ENGINE_FAILED, why
        return plan
    chosen, mode = choose(rep, TIER_LIGHT)
    plan.mode = mode
    plan.base_setup = append_new([], rep.base_setup)
    if mode == TIER_LIGHT:
        nested, setup, refs, nested_full, nwhy = _nested(repo_dir, cwd_rel, base, ask, rep.paths or [])
        if nwhy:
            plan.status, plan.reason = PLAN_ENGINE_FAILED, nwhy
            return plan
        chosen = chosen + nested
        plan.base_setup = append_new(plan.base_setup, setup)
        plan.engine_ref += "".join("; " + r for r in refs)
        plan.nested_full = nested_full
    if not chosen:
        plan.status = PLAN_EMPTY
        plan.reason = "the engine planned no command (stack %s, conf %s)" % (gostr.quote(rep.stack), rep.conf)
        return plan
    plan.status, plan.commands = PLAN_PLANNED, chosen
    return plan
