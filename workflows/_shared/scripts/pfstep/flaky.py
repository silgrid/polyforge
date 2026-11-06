"""A failing unit's flaky re-runs at HEAD: its targeted re-run run again,
serially, at most a re-run count of times while the budget allows; one
passing run (positive evidence of the unit passing, ``units.unit_passed``)
records it flaky (``flaky 1/<runs>``) and ends the re-runs. A unit without
a targeted re-run is never flaky: its note says why. A unit whose first
re-run never started (the budget spent, the deadline passed) is marked
``not_rerun``. (The Go side's twin,
verifyrun's flaky, left with the step command pf-verify.)
"""

from dataclasses import dataclass
from typing import Callable

from .rerun import UnitState
from .units import KIND_COMMAND, KIND_JUNIT, find_unit, normalize_output, output_lines, parse_units, unit_passed


@dataclass
class RerunRun:
    """One re-run as the caller ran it."""

    # not_started: the run's deadline had passed; nothing ran
    not_started: bool = False
    exit: int = 0
    log_path: str = ""
    # tmp_dir: the run's private TMPDIR (its output naming it is a token)
    tmp_dir: str = ""
    # duration in seconds
    duration: float = 0.0


def note_unrerunnable(u: UnitState, report_note: str = "") -> None:
    """The note of a failing unit without a targeted re-run (report_note:
    why the command's declared report located nothing)."""
    if u.note == "":
        u.note = "not re-runnable: its targeted re-run cannot be derived"
    if u.kind == KIND_COMMAND:
        u.note = "no failure unit could be located in the output: the whole command is the unit"
        if report_note != "":
            u.note += "; " + report_note
    if u.kind == KIND_JUNIT:
        u.note = "located by the declared junit report; not re-runnable: the report names no targeted re-run"
    if u.unit.build_failed:
        u.note = "the package did not build"


def rerun_flaky(
    u: UnitState,
    reruns: int,
    repo_dir: str,
    run: Callable[[str], RerunRun],
    budget_left: Callable[[], bool],
    budget_note: str,
    deadline_note: str,
    report_note: str = "",
) -> None:
    """Re-run u (failing) up to reruns times by its targeted re-run (run
    answers one run of a command line in the repo at repo_dir). budget_note
    names the budget that may stop the re-runs, deadline_note the deadline
    a re-run that could not start ran into."""
    if u.rerun == "":
        note_unrerunnable(u, report_note)
        return
    for i in range(1, reruns + 1):
        if not budget_left():
            u.note = "re-run %d not started: %s is spent" % (i, budget_note)
            u.not_rerun = i == 1
            return
        res = run(u.rerun)
        if res.not_started:
            u.note = "re-run %d not started: %s had passed" % (i, deadline_note)
            u.not_rerun = i == 1
            return
        u.runs += 1
        u.rerun_dur = res.duration
        lines = normalize_output(output_lines(res.log_path), repo_dir, res.tmp_dir)
        us = parse_units(lines)
        if unit_passed(u.kind, u.name, res.exit, us, lines):
            # one passing run ends the re-runs: passes/runs
            u.flaky = "flaky 1/%d" % u.runs
            u.pass_logs.append(res.log_path)
            return
        got = find_unit(us, u.kind, u.name)
        if got is not None and got.failed:
            u.head_sig = got.signature
    u.note = "still failing on %d re-run(s) at HEAD" % reruns
