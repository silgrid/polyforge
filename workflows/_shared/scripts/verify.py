"""verify.py — the mechanical verification of a capsule's task repos as the sync
step left them (develop's verification step).

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/verify.py" --tier auto|light|full \\
        [--unit-runs FILE] [--analysis FILE] [--capsule DIR] [--engine PATH] [--deadline D] [--log-dir DIR] [--json] [--out PATH]

Each task repo's tree is verified at its HEAD: no fetch, no merge (a merge in
progress makes the run not judged); the base, and the baseline a still-red
unit is proven pre-existing on, is ``merge-base HEAD origin/<default>``. A
capsule whose fixed target branch is set (``runtime-state/target-branch.json``,
a sub-ticket's) measures the same from ``merge-base HEAD origin/<target>`` —
the record's ``default_branch`` field keeps its name and carries the target —
and a target that cannot be named (a file that is there but cannot be read,
or missing on origin) is not judged naming why, never a fall-back to the
default branch. The
scope engine plans each touched repo ``--light`` and every nested engine root
the diff reaches (a subdirectory with its own tracked
``.polyforge/verify-scope.conf``; its commands run behind ``cd <root> &&``);
the whole plan runs, never stopping at the first failure; a failing unit is
re-run once at HEAD and, still red, run on the baseline; a command whose
every failure was explained is re-run whole (or its suite proven complete).
Every progress line is said on stderr and, inside a step, appended to the
step's progress file (``pfstep.progress``): each plan command opens with
``[i/n] <name> (<phase>): <cmd>`` and closes with ``PASS (<duration>)``,
``FAIL - exit <n> (<duration>), log <path>``, ``SKIP - <why>`` or ``not
started - <why>`` (gate.py's format), so a reader watching the capsule
follows a command that has not ended.

The tier: ``--tier auto`` (the lane's line) reads the capsule's work item
(``runtime-state/work-item.json``) — a ticket carrying a true
``integration_fix`` attr (the marker an integration batch's full-gate
writes on its fix tickets) runs the **full** bar: the engine plans ``--full``
(a forced-full path, a full tail — everything the light tier runs and the
rest), with no nested-root light planning, and the whole run's window is
the budget. Every other ticket runs the **light** tier, exactly as before.
``--tier light`` and ``--tier full`` are the hand run's explicit overrides
(an explicit light stays light on a marked ticket).

Where a repo's bar runs is the repo's: one whose HEAD carries
``.polyforge/Dockerfile.verify`` runs its engine and bar in a verification
container of its own (the ``pf.verify.*`` labels, the deadline label and a
keep-alive ending five minutes after the deadline; removed when the run
ends); any other runs on the host under the step's PATH. The step's
``image`` parameter (``PF_PARAM_IMAGE``, or ``--image``; the flag wins
and the result records where the value came from) names a prepared image
instead — a person's, resolved to its local ID and never built, never
pulled — and then every touched repo's bar runs in its container,
Dockerfile or not; an image the machine does not carry is `not judged`
(exit 2, the fixed mark ``pf-verify-image-missing`` in the summary: a
person prepares it, a retry cannot).

``--analysis`` names the build's change analysis
(``artifacts/build/change-analysis.json``): the tests it lists run beside
the plan — the union is add-only, the engine's selection never narrowed.
The analysis's ``supplement_limit`` (the script's default when it names
none) caps the supplemented runs: the first entries in listed order run,
the tail is cut and named in the result and the report. Each entry names
an existing test — its ``file`` must be a tracked file of its task repo at
HEAD and its ``name`` (when given) must appear in it — a ``.``, ``::`` or
``/``-joined name exists when every one of its segments appears in the
file, and a missing segment is named — and the run that shows it passing,
judged by its marker under ``--unit-runs``' own rules;
a listed test that does not exist fails its check naming it. A missing or
malformed analysis file is the invocation's (exit 2).

``--unit-runs`` names the build's units.json (``[{id, [pre_existing_red,] [timeout,]
[test_commit,] impl_commit, run{repo, cwd, argv}, marker}]``): every unit's green run is run
again as written beside the plan, each its own check
``<repo>:unit-run:<id>``. It passes when it exits 0 and one output line
matches its marker — a Python regular expression (``re.ASCII``, searched in
each line) that must not be empty, blank or able to match the empty string;
a run that cannot run as written fails its check without running. A unit
run is never pre-existing (it pins this change), and its test commit's files
are protected for the plan's units too. A unit's run is bounded by its
self-reported ``timeout``, else the shared 10m default (``pfstep/unitruns``):
a run its limit kills is an evidence problem, the why naming the limit — a
red-state run must exit non-zero within its limit, never hang. An unreadable
or malformed file (an invalid ``timeout`` included) is the invocation's
(exit 2).

The scope engine: ``--engine``, else the shared tree's
``$PF_SHARED_DIR/scripts/verify-scope``.

The logs — the run's result.json, report-mechanical.md and one log per
command — go to ``--log-dir``, else ``logs/`` beside ``--out``, else the
capsule's next ``runtime-state/verify/<n>``; a .gitignore keeps them out of
the capsule chain. The result file (pf-step-result/1) carries the heads,
and in ``details`` the tier (requested, effective, reason), the mechanical
verdict, every check with its units, the pre-existing failures, the repos and
the paths of result.json and report-mechanical.md. Every unit that failed
its first run (failed, flaky or pre-existing) carries ``failure``:
``browser_crash`` when that run's output for it shows Chromium's renderer
crash (``Target crashed`` / ``Page crashed``; a suite's whole block, a whole
command's whole log), else ``assertion``; the report marks the crashes. A
failed unit whose re-run never started (the budget spent, the deadline
passed) carries ``not_rerun: true`` and stays a failure; the report lists
those units by check under ``Not re-run``. Each command record
(``repos[].commands[]``) carries ``tmp_dir``, the real path of its first
run's private TMPDIR (on the host or inside the container), so its length
can be checked; the report lists it with its length in bytes under its
repo's plan, and the result file's ``details.checks[]`` carries it as
``tmp_dir``. The failure output and the failure signatures keep ``<tmp>``
in its place.

Exit 0: every check passed (a tree with no commit beyond its default branch
has nothing to run and passes). Exit 1: a check failed — a new failure, an
untracked test file, a bar command that did not run in full, a unit run red
or not runnable as written. Exit 2: not judged — no capsule, no engine, a
merge in progress, an unresolvable default branch, a container that could
not be had or was lost, the deadline, an interrupt — or the invocation
unusable.
"""

import argparse
import json
import os
import re
import shutil
import sys
import tempfile
import time
import traceback
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional, Sequence, Set, Tuple

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import pfstep  # noqa: E402
from pfstep import container, duration, git, gostr, jsonout, result, runner, stepenv, tmpdir  # noqa: E402
from pfstep import progress  # noqa: E402
from pfstep import scoped_plan  # noqa: E402
from pfstep.scoped_plan import append_new, base_branch_of, engine_argv, fallback_reasons, plan_light  # noqa: E402
from pfstep import targetbranch  # noqa: E402
from pfstep.analysis import Analysis, AnalysisDecl, read_analysis, selected_problem, static_problem  # noqa: E402
from pfstep.carries import file_carries, glob_match  # noqa: E402
from pfstep.completion import completion_proof, harness_command, read_go_harness  # noqa: E402
from pfstep.deadline import default_deadline_for  # noqa: E402
from pfstep.engine import TIER_FULL, TIER_LIGHT, EngineCommand, EngineReport, ReportError, choose, decode_report  # noqa: E402
from pfstep.engine import default_engine  # noqa: E402
from pfstep.flaky import RerunRun, rerun_flaky  # noqa: E402
from pfstep.junit import ReportRead, merge_reported, read_declared_report, snapshot_report  # noqa: E402
from pfstep.preexisting import PRESENT, BaseObservation, classify_base, short, unit_presence  # noqa: E402
from pfstep.rerun import CommandSpec, UnitState, locate, whole_command  # noqa: E402
from pfstep.shellwords import command_word, has_assignment, has_flag, has_go_test, shell_quote, shell_words, token_index  # noqa: E402
from pfstep.units import (  # noqa: E402
    KIND_COMMAND,
    KIND_E2E_SUITE,
    KIND_GO,
    KIND_GO_PKG,
    KIND_LIBTEST,
    KIND_PYTEST,
    browser_crashed,
    cap_text,
    find_unit,
    normalize_output,
    output_lines,
    parse_units,
    race_seen,
    log_tail,
)
from pfstep.unitruns import (  # noqa: E402
    UnitRunDecl,
    argv_line,
    limit_note,
    marker_matches,
    read_unit_runs,
    run_shape_problem,
    unit_limit_ns,
    unknown_repo_problem,
)

COMMAND = "verify.py"
VERSION = pfstep.LIBRARY

# The knobs the light tier runs with.
COMMAND_TIMEOUT = 20 * duration.MINUTE
BUDGET = 20 * duration.MINUTE
RERUNS = 1
BASE_RUNS = 1
# Kept free at the end of the run for the cleanup and the result.
WRITE_MARGIN = 20.0
GIT_TIMEOUT = 600.0

ORIGIN_PLAN = "plan"
ORIGIN_UNIT_RUN = "unit-run"
KIND_UNIT_RUN = "unit_run"
ORIGIN_ANALYSIS = "analysis"
KIND_ANALYSIS = "analysis"

# the plan's statuses, the fallback reasons and the measured branch: the
# one shared planning implementation's (pfstep/scoped_plan.py), the words
# verify.py's own records keep (polyforge#164)
PLAN_PLANNED = scoped_plan.PLAN_PLANNED
PLAN_EMPTY = scoped_plan.PLAN_EMPTY
PLAN_ENGINE_FAILED = scoped_plan.PLAN_ENGINE_FAILED
NO_FALLBACK_DIAGNOSTIC = scoped_plan.NO_FALLBACK_DIAGNOSTIC

MERGE_NONE = "none"
BASELINE_MERGE_BASE = "merge-base"

VERIFY_DIR = "runtime-state/verify"
VERIFY_GITIGNORE = "# pf verify-run: command logs are local, never capsule chain content\n*\n"

FAILURE_BROWSER_CRASH = "browser_crash"
FAILURE_ASSERTION = "assertion"
BROWSER_CRASH_NOTE = "browser crash"

GENERIC_TEST_BASE = ("*_test.go", "*.test.*", "*.spec.*", "test_*.py", "*_test.py")

_S = gostr.SPACE


class Usage(Exception):
    """An unusable invocation (exit 2)."""


# ---- the run's state ------------------------------------------------------


@dataclass
class MergeOutcome:
    default_branch: str = ""
    outcome: str = MERGE_NONE
    pre_head: str = ""
    head: str = ""
    touched: bool = False
    base: str = ""
    baseline: str = ""
    baseline_ref: str = ""
    detail: str = ""

    def as_dict(self) -> Dict[str, Any]:
        out: Dict[str, Any] = {
            "default_branch": self.default_branch, "outcome": self.outcome, "pre_head": self.pre_head,
            "head": self.head, "touched": self.touched, "base": self.base,
        }
        if self.baseline:
            out["baseline"] = self.baseline
        if self.baseline_ref:
            out["baseline_ref"] = self.baseline_ref
        if self.detail:
            out["detail"] = self.detail
        return out


@dataclass
class RepoPlan:
    status: str = ""
    reason: str = ""
    mode: str = ""  # the mode the chosen commands belong to (full | light)
    commands: List[EngineCommand] = field(default_factory=list)
    engine_ref: str = ""
    report: Optional[EngineReport] = None  # the repo root's own report
    base_setup: List[str] = field(default_factory=list)
    # nested_full: why the run is full for each nested engine root whose
    # own light report fell back to the full tier (one entry per reason)
    nested_full: List[str] = field(default_factory=list)

    def planned(self) -> bool:
        return self.status == PLAN_PLANNED


@dataclass
class CmdRun:
    repo: str
    check: str
    name: str
    phase: str
    cmd: str
    origin: str
    timeout: int  # nanoseconds
    skipped: str = ""
    exit: int = 0
    timed_out: bool = False
    deadline_hit: bool = False
    not_started: str = ""
    dur_ms: int = 0  # its first run's wall time
    log_path: str = ""
    units: List[UnitState] = field(default_factory=list)
    confirmed: str = ""
    completion: str = ""
    harness: bool = False
    report: Optional[Any] = None
    report_read: Optional[ReportRead] = None
    rerun_tmpl: str = ""
    dir: str = ""
    inner: str = ""
    tmp_dir: str = ""  # its first run's private TMPDIR, the real path

    def spec(self) -> CommandSpec:
        return CommandSpec(cmd=self.cmd, timeout=self.timeout, rerun_tmpl=self.rerun_tmpl, dir=self.dir, inner=self.inner)


@dataclass
class UnitRunState:
    decl: UnitRunDecl
    cwd: str = ""
    problem: str = ""
    exit: int = 0
    ran: bool = False
    runs: int = 0
    passed: bool = False
    flaky: bool = False
    timed_out: bool = False
    log_path: str = ""
    why: str = ""
    # crashed: its first failing run's output shows a browser crash
    crashed: bool = False
    # not_rerun: it failed and its re-run never started
    not_rerun: bool = False

    def status(self) -> str:
        if self.passed and self.flaky:
            return "flaky"
        if self.passed:
            return "pass"
        return "fail"


@dataclass
class AnalysisState(UnitRunState):
    """One listed test's supplement run: the same run, marker and flaky
    policy as a unit run, on the analysis's own declaration."""

    decl: AnalysisDecl  # type: ignore[assignment]


@dataclass
class UntrackedScan:
    files: List[str] = field(default_factory=list)
    err: str = ""


@dataclass
class RepoRun:
    name: str
    dir: str
    cwd_rel: str
    log_dir: str
    go_harness: str = ""
    merge: Optional[MergeOutcome] = None
    touched: bool = False
    plan: Optional[RepoPlan] = None
    runs: List[CmdRun] = field(default_factory=list)
    unit_runs: List[UnitRunState] = field(default_factory=list)
    analysis_runs: List[AnalysisState] = field(default_factory=list)
    untracked_before: UntrackedScan = field(default_factory=UntrackedScan)
    untracked_after: UntrackedScan = field(default_factory=UntrackedScan)
    box: Optional[container.Box] = None

    def is_harness(self, cmd: str) -> bool:
        """Whether cmd runs the declared Go harness directly (the one
        implementation, pfstep.completion)."""
        return harness_command(self.go_harness, cmd)


# ---- helpers ---------------------------------------------------------------


def git_quiet(directory: str, *args: str, literal: bool = True) -> str:
    """One read-only git call's stdout (git.GitError when it fails)."""
    return git.git(directory, *args, timeout=GIT_TIMEOUT, literal_pathspecs=literal)


def join_note(a: str, b: str) -> str:
    return b if a == "" else a + "; " + b


def log_ref(log_path: str) -> str:
    """A log named as <its directory>/<its name>."""
    return os.path.basename(os.path.dirname(log_path)) + "/" + os.path.basename(log_path)


def is_test_file(p: str, globs: Sequence[str]) -> bool:
    """One repo-relative path judged a test file: a generic name pattern, a
    path under a test/ or tests/ directory, or a conf test_glob (matched
    against the path and its base name, ``*`` crossing ``/``)."""
    p = gostr.path_clean(p)
    if p.startswith("./"):
        p = p[2:]
    base = p[p.rfind("/") + 1:]
    if any(glob_match(g, base) for g in GENERIC_TEST_BASE):
        return True
    parts = p.split("/")
    if any(d in ("test", "tests") for d in parts[:-1]):
        return True
    return any(glob_match(g, p) or glob_match(g, base) for g in globs)


# ---- the run ---------------------------------------------------------------


def bar_end_line(at: str, cr: CmdRun) -> str:
    """The end line of one bar command, gate.py's format: PASS with its
    duration, FAIL with its exit code, duration and log, SKIP / not started
    with why (the start line already named the command)."""
    if cr.skipped:
        return "%s SKIP - %s" % (at, cr.skipped)
    if cr.not_started:
        return "%s not started - %s" % (at, cr.not_started)
    took = duration.format((cr.dur_ms * duration.MILLISECOND + duration.SECOND // 2) // duration.SECOND * duration.SECOND)
    if cr.exit == 0:
        return "%s PASS (%s)" % (at, took)
    what = "killed at its timeout" if cr.timed_out else "exit %d" % cr.exit
    return "%s FAIL - %s (%s), log %s" % (at, what, took, cr.log_path)


class Verification:
    """One light run over a capsule."""

    def __init__(
        self,
        capsule: str,
        engine: str,
        deadline: int,
        run_dir: str,
        unit_runs: List[UnitRunDecl],
        env: Dict[str, str],
        say: Callable[[str], None],
        co: Optional[container.ContainerOptions],
        analysis: Optional[Analysis] = None,
        tier: str = TIER_LIGHT,
        tier_requested: str = "",
        tier_reason: str = "",
        image: str = "",
        image_source: str = "",
    ) -> None:
        self.capsule = capsule
        self.engine = engine
        self.deadline = deadline  # ns: the whole run's
        # the tier as resolved: light (the default) or full (an
        # integration-fix ticket); tier_requested is what the line asked
        # (auto names the work item's own decision) and tier_reason why a
        # full run is full
        self.tier_light = tier == TIER_LIGHT
        self.tier_requested = tier_requested or tier
        self.tier_reason = tier_reason
        self.unit_run_decls = unit_runs
        # the analysis's listed tests: the first `limit` run beside the
        # plan, the tail cut and named (the union is add-only); its machine
        # selection records are judged by the same shared validator the
        # build close's own check runs (pfstep/analysis.selected_problem)
        an = analysis or Analysis()
        self.analysis_given = analysis is not None
        self.analysis_taken, self.analysis_cut = an.taken()
        self.analysis_limit = an.limit
        self.analysis_selected = an.selected
        self.env = env
        self.say = say
        self.co = co
        # image: a prepared image every touched repo's bar runs in ("" =
        # each repo by its own Dockerfile); image_source: which named it —
        # "flag" (--image) or "param" (PF_PARAM_IMAGE)
        self.image = image
        self.image_source = image_source
        self.n, self.run_dir = self._run_dir(run_dir)
        start = time.time()
        # the light tier's rerun budget is its own knob; a full run is
        # bounded by the whole run's window (the bar itself is long)
        if self.tier_light:
            self.budget_until = start + BUDGET / duration.SECOND
        else:
            self.budget_until = start + deadline / duration.SECOND
        self.hard = start + deadline / duration.SECOND - WRITE_MARGIN
        self.r = runner.Runner(
            capsule, self.hard, env=env,
            deadline_what="verify-run's deadline (%s)" % duration.format(deadline),
            command_timeout=COMMAND_TIMEOUT / duration.SECOND,
        )
        self.repos_names: List[str] = []
        self.protected: List[str] = []
        self.boxes: List[container.Box] = []
        self.interrupted = False

    # -- the run directory

    def _run_dir(self, run_dir: str) -> Tuple[int, str]:
        if run_dir:
            d = os.path.abspath(run_dir)
            os.makedirs(d, exist_ok=True)
            self._gitignore(d)
            return 1, d
        root = os.path.join(self.capsule, VERIFY_DIR)
        os.makedirs(root, exist_ok=True)
        self._gitignore(root)
        n = 0
        for name in os.listdir(root):
            if re.fullmatch(r"[+-]?[0-9]+", name, re.ASCII):
                n = max(n, int(name))
        while True:
            n += 1
            d = os.path.join(root, str(n))
            try:
                os.mkdir(d)
                return n, d
            except FileExistsError:
                continue

    @staticmethod
    def _gitignore(d: str) -> None:
        gi = os.path.join(d, ".gitignore")
        try:
            with open(gi, encoding="utf-8") as f:
                if f.read() == VERIFY_GITIGNORE:
                    return
        except OSError:
            pass
        with open(gi, "w", encoding="utf-8") as f:
            f.write(VERIFY_GITIGNORE)

    # -- clocks

    def budget_left(self) -> bool:
        return time.time() < self.budget_until

    def fits(self, d: float) -> bool:
        return self.r.remaining() > d

    def budget_note(self) -> str:
        if self.tier_light:
            return "verify-run's budget (%s)" % duration.format(BUDGET)
        return "the whole run's window (%s)" % duration.format(self.deadline)

    def rel(self, p: str) -> str:
        return self.r.rel(p) if p else ""

    # -- running one command

    def exec(self, rr: RepoRun, argv: List[str], directory: str, cwd: str, timeout: float,
             private_tmp: bool = False, boxed: bool = False, capture: bool = False) -> runner.ExecResult:
        return self.r.exec(
            rr.log_dir,
            runner.ExecSpec(argv=argv, dir=directory, cwd=cwd, timeout=timeout, private_tmp=private_tmp, capture=capture),
            rr.box if boxed else None,
        )

    def exec_bash(self, rr: RepoRun, cmd: str, directory: str, cwd: str, timeout_ns: int) -> runner.ExecResult:
        return self.exec(rr, runner.bash_argv(cmd), directory, cwd, timeout_ns / duration.SECOND, private_tmp=True, boxed=True)

    # -- the phases

    def run(self) -> Dict[str, Any]:
        """The whole run; answers result.json's document."""
        repos = [self.new_repo_run(name) for name in self.repos_names]
        incomplete = ""
        try:
            try:
                # 1. the tree as it stands (serial): no fetch, no merge
                for rr in repos:
                    incomplete = self.head_phase(rr)
                    if incomplete:
                        break
                if not incomplete:
                    # 2a. the container of every repo that runs in one
                    incomplete = self.box_repos(repos)
                if not incomplete:
                    # 2b. plan per touched repo
                    for rr in repos:
                        if rr.merge is not None and rr.touched:
                            self.plan_repo(rr)
                    # 3. bars and the build's unit runs, one repo after another
                    for rr in repos:
                        self.run_bar(rr)
                        self.run_unit_runs(rr)
                        self.run_analysis_runs(rr)
                    # 4. pre-existing base runs, then the confirming re-runs
                    for rr in repos:
                        self.pre_existing_phase(rr)
                        self.confirm_phase(rr)
            except (KeyboardInterrupt, runner.Interrupted):
                self.interrupted = True
            if not incomplete and not self.interrupted:
                incomplete = self.box_lost_reason(repos)
        finally:
            self.unbox_all()
        if not incomplete and not self.interrupted:
            # 5. the untracked-test-file scan after the bar
            for rr in repos:
                rr.untracked_after = self.untracked_test_files(rr)
        if self.interrupted and not incomplete:
            incomplete = "verify-run was interrupted (context canceled): its record is partial and proves nothing"
        return self.assemble(repos, incomplete)

    def new_repo_run(self, name: str) -> RepoRun:
        # the clone's canonical absolute path (polyforge#153): the container
        # mounts it at exactly this path and the cache volume keys on it
        d = os.path.realpath(os.path.abspath(os.path.join(self.capsule, "repos", name)))
        return RepoRun(name=name, dir=d, cwd_rel="repos/" + name, log_dir=os.path.join(self.run_dir, name),
                       go_harness=read_go_harness(d))

    def head_phase(self, rr: RepoRun) -> str:
        """rr's state from the tree as it stands; the not-judged reason ("" =
        the bar may run)."""
        m = MergeOutcome()
        rr.merge = m

        def fail(msg: str) -> str:
            rr.merge = None
            return "repos/%s: %s" % (rr.name, msg)

        if self.r.remaining() < 1.0:
            return fail("%s passed before the run could start" % self.r.deadline_note())
        if git.run(rr.dir, "rev-parse", "-q", "--verify", "MERGE_HEAD", timeout=GIT_TIMEOUT).exit_code == 0:
            return fail("a merge is in progress (MERGE_HEAD): the tree is no verified state — the sync step finishes it"
                        " (its real conflicts resolved, then the sync skill's continue line, `\"$PF_PYTHON\""
                        " \"$PF_SHARED_DIR/scripts/sync.py\" --continue --json --out artifacts/sync/result.json`)"
                        " before a verification runs")
        default, unresolvable = base_branch_of(self.capsule, rr.dir)
        if default == "":
            return fail(unresolvable)
        m.default_branch = default
        try:
            head = gostr.trim_space(git_quiet(rr.dir, "rev-parse", "--verify", "-q", "HEAD"))
        except git.GitError as err:
            return fail("HEAD is unreadable: %s" % err)
        m.pre_head = m.head = head
        try:
            n = gostr.trim_space(git_quiet(rr.dir, "rev-list", "--count", "origin/" + default + "..HEAD"))
        except git.GitError as err:
            return fail("cannot count the commits beyond origin/%s: %s" % (default, err))
        m.touched = rr.touched = n.isdigit() and int(n) > 0
        try:
            m.base = gostr.trim_space(git_quiet(rr.dir, "merge-base", "HEAD", "origin/" + default))
        except git.GitError:
            m.base = ""
        if m.base:
            m.baseline, m.baseline_ref = m.base, BASELINE_MERGE_BASE
        m.detail = "HEAD verified as the sync step left it: no fetch, no merge; the base is merge-base HEAD origin/" + default
        return ""

    # -- containers

    def box_repos(self, repos: List[RepoRun]) -> str:
        """Start the container of every repo that runs in one; the not-judged
        reason ("" = every one is up)."""
        if self.co is None:
            return ""
        if self.image:
            # the step parameter names the image: every touched repo's bar
            # runs in its container, Dockerfile or not
            need = [rr for rr in repos if rr.merge is not None and rr.touched]
        else:
            need = [rr for rr in repos if rr.merge is not None and rr.touched and container.has_dockerfile_at(rr.dir, "HEAD")]
        if not need:
            return ""
        rt = self.co.engine()
        try:
            rt.available()
        except container.ContainerError as err:
            if self.image:
                return ("the step parameter image names %s, so every touched repo's bar runs in a container, but the"
                        " container engine is unavailable: %s" % (self.image, err))
            return ("repos/%s carries %s, so its bar runs in a container, but the container engine is unavailable: %s"
                    % (need[0].name, container.DOCKERFILE_VERIFY, err))
        if not os.path.exists(self.engine):
            return "the scope engine %s cannot be resolved for a container mount: no such file" % self.engine
        engine = os.path.realpath(self.engine)
        for rr in need:
            try:
                box = container.start_box(self.co, rr.name, rr.dir, rr.log_dir, self.hard, self.n, self.rel,
                                          engine=engine, base_root=os.path.join(self.run_dir, "base", rr.name),
                                          image=self.image)
            except container.ImageMissing as err:
                return ("repos/%s: the image %s is not present on this machine and is never pulled (%s): the image is"
                        " prepared by a person — have one prepared or clear the step parameter; the bar was not run"
                        % (rr.name, err.ref, container.IMAGE_MISSING_MARK))
            except container.BoxError as err:
                return "repos/%s: the verification container could not be prepared: %s" % (rr.name, err)
            rr.box = box
            self.boxes.append(box)
        return ""

    def unbox_all(self) -> None:
        for b in self.boxes:
            b.remove()

    def box_lost_reason(self, repos: List[RepoRun]) -> str:
        for rr in repos:
            if rr.box is not None:
                why = rr.box.lost_reason()
                if why:
                    return ("repos/%s: its verification container became unusable while the bar ran (%s): the record"
                            " proves nothing" % (rr.name, why))
        return ""

    def box_env(self) -> List[str]:
        return self.r.box_env()

    # -- the plan

    def ask_engine(self, rr: RepoRun, directory: str, cwd: str, base: str, light: bool = True) -> Tuple[Optional[EngineReport], str, str]:
        """The engine's report with directory as its root (the light one
        under the light tier, the full one under the full tier): (report,
        the invocation, why there is none)."""
        engine = rr.box.engine if rr.box is not None else self.engine
        # the invocation itself is the one shared planning implementation's
        # (pfstep/scoped_plan.py: engine_argv), so verification's light
        # request and the build close's ask the engine exactly alike
        argv = engine_argv(engine, base, light=light)
        ref = " ".join(argv)
        er = self.exec(rr, argv, directory, cwd, GIT_TIMEOUT, boxed=True, capture=True)
        if er.error:
            return None, ref, "the engine %s could not start: %s" % (engine, er.error)
        if er.exit != 0:
            return None, ref, "the engine exited %d: %s" % (er.exit, log_tail(er.log_path))
        try:
            return decode_report(er.stdout), ref, ""
        except ReportError as err:
            return None, ref, "the engine's report is unreadable: %s" % err

    def plan_repo(self, rr: RepoRun) -> None:
        """rr's plan: the light tier's own report's commands and, when that
        is light, every nested engine root's the diff reaches; the full
        tier's one full report (a forced-full path, a full tail — the conf's
        full plan covers the nested roots too, as gate.py's does)."""
        base = rr.merge.base if rr.merge is not None else ""
        if not self.tier_light:
            rep, ref, why = self.ask_engine(rr, rr.dir, rr.cwd_rel, base, light=False)
            p = RepoPlan(engine_ref=ref, report=rep)
            rr.plan = p
            if rep is None:
                p.status, p.reason = PLAN_ENGINE_FAILED, why
                return
            chosen, mode = choose(rep, TIER_FULL)
            p.mode = mode
            p.base_setup = append_new([], rep.base_setup)
            if not chosen:
                p.status = PLAN_EMPTY
                p.reason = "the engine planned no command (stack %s, conf %s)" % (gostr.quote(rep.stack), rep.conf)
                return
            p.status, p.commands = PLAN_PLANNED, chosen
            return
        # the light plan itself is the one shared planning
        # implementation's (pfstep/scoped_plan.py: plan_light) — the build
        # close's scoped selection plans through it too, so the two cannot
        # drift (tests/test_build_scoped_close.py's SelectionParityTest)
        lp = plan_light(rr.dir, rr.cwd_rel, base, lambda d, rel, b: self.ask_engine(rr, d, rel, b))
        p = RepoPlan(status=lp.status, reason=lp.reason, mode=lp.mode, commands=lp.commands,
                     engine_ref=lp.engine_ref, report=lp.report, base_setup=lp.base_setup,
                     nested_full=lp.nested_full)
        rr.plan = p

    # -- the bar

    def timeout_of(self, c: EngineCommand) -> int:
        try:
            d = duration.parse(gostr.trim_space(c.timeout))
        except ValueError:
            return COMMAND_TIMEOUT
        return d if d > 0 else COMMAND_TIMEOUT

    def run_bar(self, rr: RepoRun) -> None:
        if rr.merge is None:
            return
        rr.untracked_before = self.untracked_test_files(rr)
        if not (rr.touched and rr.plan is not None and rr.plan.planned()):
            return
        for i, c in enumerate(rr.plan.commands):
            at = "[%d/%d] %s" % (i + 1, len(rr.plan.commands), c.name)
            cr = CmdRun(repo=rr.name, check=rr.name + ":" + c.name, name=c.name, phase=c.phase, cmd=c.cmd,
                        origin=ORIGIN_PLAN, timeout=self.timeout_of(c), report=c.report, rerun_tmpl=c.rerun,
                        dir=c.dir, inner=c.inner)
            self.say("%s (%s): %s" % (at, c.phase, c.cmd))
            why = self.unmet(c, rr)
            if why:
                cr.skipped = why
                self.say("%s SKIP - %s" % (at, why))
            else:
                self.run_command(rr, cr)
                self.say(bar_end_line(at, cr))
            rr.runs.append(cr)

    def unmet(self, c: EngineCommand, rr: RepoRun) -> str:
        """The unmet precondition of a planned command ("" = none), judged
        where it runs (runner.Runner.unmet)."""
        return self.r.unmet(c.requires_env, c.requires_cmd, rr.dir, rr.box)

    def protected_path(self, p: str) -> bool:
        c = gostr.clean_rel(p)
        return c != "" and any(c == pr or c.startswith(pr + "/") for pr in self.protected)

    def rerun_runner(self, rr: RepoRun, timeout: int) -> Callable[[str], RerunRun]:
        def run(cmd: str) -> RerunRun:
            er = self.exec_bash(rr, cmd, rr.dir, rr.cwd_rel, timeout)
            dur = er.evidence.duration_ms / 1000.0 if er.evidence is not None else 0.0
            return RerunRun(not_started=er.not_started, exit=er.exit, log_path=er.log_path, tmp_dir=er.tmp_dir, duration=dur)
        return run

    def run_command(self, rr: RepoRun, cr: CmdRun) -> None:
        """One bar command run, its units parsed and located, the flaky
        policy applied."""
        before = snapshot_report(rr.dir, cr.report) if cr.report is not None else {}
        er = self.exec_bash(rr, cr.cmd, rr.dir, rr.cwd_rel, cr.timeout)
        if er.not_started:
            cr.exit, cr.not_started = -1, "not started: " + self.r.deadline_note() + " had passed"
            return
        cr.exit, cr.timed_out, cr.deadline_hit, cr.log_path = er.exit, er.timed_out, er.deadline_hit, er.log_path
        cr.dur_ms = er.evidence.duration_ms if er.evidence is not None else 0
        cr.tmp_dir = er.tmp_dir
        lines = normalize_output(output_lines(er.log_path), rr.dir, er.tmp_dir)
        words, _ = shell_words(cr.cmd)
        parsed = parse_units(lines, failfast=has_go_test(words) and has_flag(words, "-failfast"))
        cr.harness = rr.is_harness(cr.cmd)
        race = race_seen(lines)
        spec = cr.spec()
        for u in parsed:
            us = UnitState(unit=u)
            if er.exit == 0:
                # the exit status is the truth: a passing command's output
                # may quote failure markers
                u.failed = False
            locate(rr.dir, spec, us, race)
            cr.units.append(us)
        if cr.report is not None:
            if er.exit == 0:
                cr.report_read = ReportRead(format=cr.report.format, path=cr.report.path, note="not read: the command passed")
            else:
                cr.report_read, reported = read_declared_report(rr.dir, cr.report, before)
                for u2 in reported:
                    locate(rr.dir, spec, u2, race)
                cr.units = merge_reported(cr.units, reported)
        if er.exit != 0 and not any(u.failed for u in cr.units):
            w = whole_command(cr.check, er.exit)
            w.unit.crashed = browser_crashed(lines)
            locate(rr.dir, spec, w, race)
            cr.units.append(w)
        for us in cr.units:
            if us.file and self.protected_path(us.file):
                us.protected = True
            if us.failed and us.file:
                us.carries = file_carries(rr.dir, us.file)
        report_note = cr.report_read.note if cr.report_read is not None else ""
        for us in [u for u in cr.units if u.failed]:
            rerun_flaky(us, RERUNS, rr.dir, self.rerun_runner(rr, cr.timeout), self.budget_left, self.budget_note(),
                        self.r.deadline_note(), report_note)

    # -- the build's unit runs and the analysis's supplements

    def prepare_unit_run(self, s: UnitRunState, rr: RepoRun) -> str:
        """Why s cannot run as written in rr ("" = it can): the shared
        shape rules (pfstep/unitruns — the one implementation the unit
        runs and the analysis entries share; fast_checks.py's own check
        keeps its separate copy)."""
        d = s.decl
        problem, cwd = run_shape_problem(d.argv, d.cwd, d.marker, rr.name,
                                          lambda c: os.path.isdir(os.path.join(rr.dir, c)))
        s.cwd = cwd
        return problem

    def prepare_analysis_run(self, s: AnalysisState, rr: RepoRun) -> None:
        """Why the listed test cannot run or does not exist ("" = it can):
        the one shared validator (pfstep/analysis — the same rules the
        build close's facts check calls, polyforge#112): the unit-run shape
        rules, then the test itself — its file tracked at HEAD, its name
        inside it."""
        d = s.decl
        s.problem, s.cwd = static_problem(
            d, rr.name,
            lambda c: os.path.isdir(os.path.join(rr.dir, c)),
            self._tracked_at_head(rr),
            self._body_at_head(rr),
        )

    def _tracked_at_head(self, rr: RepoRun) -> Callable[[str], bool]:
        def tracked(p: str) -> bool:
            try:
                git_quiet(rr.dir, "cat-file", "-e", "HEAD:" + p)
                return True
            except git.GitError:
                return False
        return tracked

    def _body_at_head(self, rr: RepoRun) -> Callable[[str], str]:
        def body_of(p: str) -> str:
            try:
                return git_quiet(rr.dir, "show", "HEAD:" + p)
            except git.GitError:
                return ""
        return body_of

    def recorded_reruns(self, rr: RepoRun, s: UnitRunState, timeout: float) -> None:
        """A failed recorded run is re-run once at HEAD (RERUNS): a pass on
        the re-run is flaky; a re-run the budget never started leaves it a
        failure with not_rerun."""
        i = 1
        while not s.passed and s.ran and i <= RERUNS:
            runs = s.runs
            if self.budget_left():
                self.exec_unit_run(rr, s, timeout)
            if s.runs == runs:
                # not started: the budget spent or the deadline passed
                s.not_rerun = i == 1
                break
            if s.passed:
                s.flaky = True
            i += 1

    def run_unit_runs(self, rr: RepoRun) -> None:
        if rr.merge is None:
            return
        for d in self.unit_run_decls:
            if d.repo != rr.name:
                continue
            s = UnitRunState(decl=d)
            rr.unit_runs.append(s)
            s.problem = self.prepare_unit_run(s, rr)
            if s.problem:
                continue
            self.exec_unit_run(rr, s, self.unit_run_limit(d))
            self.recorded_reruns(rr, s, self.unit_run_limit(d))

    def unit_run_limit(self, d: UnitRunDecl) -> float:
        """A recorded unit run's limit: its self-reported `timeout`, else
        the shared default (pfstep/unitruns) — never the bar commands'
        own bound."""
        return unit_limit_ns(d.timeout)[0] / duration.SECOND

    def run_analysis_runs(self, rr: RepoRun) -> None:
        """The analysis's taken entries of rr: each runs beside the plan,
        one check per entry, the same marker and flaky policy as a unit
        run."""
        if rr.merge is None:
            return
        for d in self.analysis_taken:
            if d.repo != rr.name:
                continue
            s = AnalysisState(decl=d)
            rr.analysis_runs.append(s)
            self.prepare_analysis_run(s, rr)
            if s.problem:
                continue
            self.exec_unit_run(rr, s, COMMAND_TIMEOUT / duration.SECOND)
            self.recorded_reruns(rr, s, COMMAND_TIMEOUT / duration.SECOND)

    def exec_unit_run(self, rr: RepoRun, s: UnitRunState, timeout: float) -> None:
        er = self.exec(rr, list(s.decl.argv), os.path.join(rr.dir, s.cwd), gostr.path_join(rr.cwd_rel, s.cwd),
                       timeout, private_tmp=True, boxed=True)
        if er.not_started:
            if not s.ran:
                s.why = "not started: " + self.r.deadline_note() + " had passed"
            return
        s.ran, s.runs = True, s.runs + 1
        s.exit, s.timed_out, s.log_path = er.exit, er.timed_out, er.log_path
        if er.timed_out:
            s.passed, s.why = False, limit_note(int(timeout * duration.SECOND))
        elif er.exit != 0:
            s.passed, s.why = False, "exit %d" % er.exit
        elif not marker_matches(s.decl.marker, output_lines(er.log_path)):
            s.passed, s.why = False, ("exit 0 but no output line matches its marker `%s`: the run did not show its"
                                      " test passing" % s.decl.marker)
        else:
            s.passed, s.why = True, ""
        if s.runs == 1 and not s.passed:
            s.crashed = browser_crashed(output_lines(er.log_path))

    def fold_unit_runs(self) -> None:
        """Protect the files of every unit run's test commit."""
        seen: Set[str] = set(self.protected)
        for d in self.unit_run_decls:
            sha = gostr.trim_space(d.test_commit)
            if sha == "" or sha.startswith("-") or d.repo == "":
                continue
            repo = os.path.join(self.capsule, "repos", d.repo)
            if not os.path.isdir(repo):
                continue
            try:
                out = git_quiet(repo, "diff-tree", "--no-commit-id", "--name-only", "-r", "--root", sha)
            except git.GitError:
                continue
            for p in gostr.fields(out):
                c = gostr.clean_rel(p)
                if c and c not in seen:
                    seen.add(c)
                    self.protected.append(c)

    # -- the base runs

    def pre_existing_phase(self, rr: RepoRun) -> None:
        """rr's still-failing units classified against the baseline."""
        cands: List[UnitState] = []
        for cr in rr.runs:
            for u in cr.units:
                if not u.failing():
                    continue
                if u.protected:
                    u.note = join_note(u.note, "never pre-existing: it is a protected test that pins this change")
                elif u.kind in (KIND_COMMAND, KIND_GO_PKG, KIND_LIBTEST) or u.file == "":
                    u.note = join_note(u.note, "never pre-existing: no test file and test name locate it")
                elif u.rerun == "":
                    pass
                else:
                    cands.append(u)
        if not cands:
            return
        base = base_name = ""
        if rr.merge is not None:
            base = base_name = rr.merge.base
            if rr.merge.baseline:
                base, base_name = rr.merge.baseline, short(rr.merge.baseline)
                if rr.merge.baseline_ref != BASELINE_MERGE_BASE:
                    base_name = rr.merge.baseline_ref + " (" + base_name + ")"

        def mark_all(why: str) -> None:
            for u in cands:
                u.note = join_note(u.note, why)

        if base == "":
            mark_all("not pre-existing: no base commit is known")
            return
        if not self.budget_left():
            mark_all("not pre-existing: no base run started, %s is spent" % self.budget_note())
            return
        parent = rr.box.base_root if rr.box is not None else None
        try:
            if parent:
                os.makedirs(parent, exist_ok=True)
            tmp = tempfile.mkdtemp(prefix="pf-verify-base-", dir=parent)
        except OSError as err:
            mark_all("not pre-existing: no throwaway worktree: %s" % err)
            return
        try:
            add = self.exec(rr, ["git", "worktree", "add", "--detach", tmp, base], rr.dir, rr.cwd_rel, GIT_TIMEOUT)
            if add.exit != 0:
                mark_all("not pre-existing: the base worktree could not be checked out (exit %d)" % add.exit)
                return
            try:
                self.base_runs(rr, cands, tmp, base, base_name, mark_all)
            finally:
                git.run(rr.dir, "worktree", "remove", "--force", tmp, timeout=GIT_TIMEOUT)
                git.run(rr.dir, "worktree", "prune", timeout=GIT_TIMEOUT)
        finally:
            shutil.rmtree(tmp, ignore_errors=True)

    def base_runs(self, rr: RepoRun, cands: List[UnitState], tmp: str, base: str, base_name: str,
                  mark_all: Callable[[str], None]) -> None:
        if rr.plan is not None:
            for setup in rr.plan.base_setup:
                er = self.exec_bash(rr, setup, tmp, tmp, COMMAND_TIMEOUT)
                if er.exit != 0:
                    mark_all("not pre-existing: the base_setup command %s failed on the base (exit %d) — an"
                             " environmental failure" % (gostr.quote(setup), er.exit))
                    return
        for u in cands:
            presence, why = unit_presence(rr.dir, base, u.kind, u.name, u.file)
            exists = presence == PRESENT
            runs: List[BaseObservation] = []
            if exists:
                i = 0
                while i < BASE_RUNS and self.budget_left() and self.fits(u.rerun_dur):
                    er = self.exec_bash(rr, u.rerun, tmp, tmp, COMMAND_TIMEOUT)
                    us = parse_units(normalize_output(output_lines(er.log_path), tmp, er.tmp_dir))
                    obs = BaseObservation(exit=er.exit)
                    got = find_unit(us, u.kind, u.name)
                    if got is not None:
                        obs.found, obs.failed, obs.signature = True, got.failed, got.signature
                    runs.append(obs)
                    i += 1
            pre, detail = classify_base(u.unit.signature, u.head_sig, exists, why, runs, BASE_RUNS)
            if pre:
                u.pre_existing = True
                u.pre_detail = cap_text("fails on base %s too: %s, signature %s" % (base_name, detail, gostr.quote(u.head_sig)))
                u.base_sig = u.head_sig
            else:
                u.note = join_note(u.note, detail)

    # -- the confirming re-run

    def confirm_phase(self, rr: RepoRun) -> None:
        """A failed command is explained only when ALL of its non-zero exit
        is: one whose every located failure was explained away is run once
        more as a whole (in its skip form when a pre-existing failure was
        excluded), or its suite's completion proven; anything else leaves a
        residual whole-command failure."""
        for cr in rr.runs:
            if cr.skipped or cr.not_started or cr.exit == 0 or cr.timed_out:
                continue
            explained, pre = True, False
            for u in cr.units:
                if u.pre_existing:
                    pre = True
                elif u.failing():
                    explained = False
            if not explained:
                continue
            residual = whole_command(cr.check, cr.exit)
            cmd = cr.cmd
            if pre:
                form, note = skip_form(cr)
                if note:
                    proof = completion_proof(cr.harness, cr.timed_out, cr.log_path, cr.units)
                    if proof:
                        cr.completion = proof + " (the light tier carries no bar to a re-run, so its pre-existing failure needs no skip form)"
                        continue
                    residual.note = ("the command has no skip switch for its pre-existing failure(s), so the rest of it"
                                     " cannot be shown to pass: it stays failed — split the bar so each command can"
                                     " exclude its own failures")
                    cr.units.append(residual)
                    continue
                cmd = form
            proof = completion_proof(cr.harness, cr.timed_out, cr.log_path, cr.units)
            if proof:
                cr.completion = proof
                continue
            if not self.budget_left():
                residual.note = ("not confirmed: %s was spent before `%s` could be re-run whole to show nothing else"
                                 " failed" % (self.budget_note(), cmd))
            elif not self.fits(cr.dur_ms / 1000.0):
                residual.note = ("not confirmed: %s leaves less than the command's first-run time (%s) to re-run `%s`"
                                 " whole" % (self.r.deadline_note(), duration.format(cr.dur_ms * duration.MILLISECOND), cmd))
            if residual.note:
                cr.units.append(residual)
                continue
            er = self.exec_bash(rr, cmd, rr.dir, rr.cwd_rel, cr.timeout)
            if er.exit == 0:
                cr.confirmed = cmd
                continue
            if er.not_started:
                residual.note = "not confirmed: %s had passed before `%s` could be re-run whole" % (self.r.deadline_note(), cmd)
            elif er.deadline_hit:
                residual.note = "not confirmed: `%s` was killed at %s while being re-run whole" % (cmd, self.r.deadline_note())
            else:
                residual.unit.marker = "exit %d" % er.exit
                residual.note = ("after its located failures were explained (flaky or pre-existing), `%s` still exits"
                                 " %d: a failure no unit locates (log %s)" % (cmd, er.exit, log_ref(er.log_path)))
            cr.units.append(residual)

    # -- the untracked-test-file scan

    def untracked_test_files(self, rr: RepoRun) -> UntrackedScan:
        try:
            out = git_quiet(rr.dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
        except git.GitError as err:
            return UntrackedScan(err=str(err))
        globs: List[str] = []
        if rr.plan is not None and rr.plan.report is not None:
            globs = rr.plan.report.test_glob
        files: List[str] = []
        entries = out.split("\x00")
        i = 0
        while i < len(entries):
            e = entries[i]
            if len(e) < 4:
                i += 1
                continue
            if e[0] in "RC":
                i += 2  # the rename/copy source path follows
                continue
            if e.startswith("?? ") and is_test_file(e[3:], globs):
                files.append(e[3:])
            i += 1
        return UntrackedScan(files=sorted(files))

    # -- the record

    def assemble(self, repos: List[RepoRun], incomplete: str) -> Dict[str, Any]:
        """result.json's document (verifyrun's Result shape)."""
        checks: List[Dict[str, Any]] = []
        units: List[Dict[str, Any]] = []
        unit_runs: List[Dict[str, Any]] = []
        analysis_runs: List[Dict[str, Any]] = []
        pre_existing: List[str] = []
        merges = {rr.name: rr.merge for rr in repos if rr.merge is not None}
        conf_changed: Dict[str, bool] = {}
        tier: Dict[str, Any] = {"requested": self.tier_requested, "effective": TIER_FULL}
        doc: Dict[str, Any] = {
            "schema": 1, "run": self.n, "out_dir": self.rel(self.run_dir), "log_dir": self.rel(self.run_dir),
        }
        if incomplete:
            doc["incomplete"] = incomplete
        reports: Optional[List[Dict[str, Any]]] = None
        unplanned: List[str] = []
        scope: Dict[str, Any] = {"fast_gates_ref": ""}
        verdict, fail_reason = "", ""
        if not incomplete:
            reports = []
            effective_light, light_repos = True, 0
            full_why: List[str] = []
            refs: List[str] = []
            paths: Dict[str, Any] = {}
            for rr in repos:
                rep: Dict[str, Any] = {"name": rr.name, "touched": rr.touched}
                commands: List[Dict[str, Any]] = []
                p = rr.plan
                if not rr.touched:
                    default = rr.merge.default_branch if rr.merge is not None else ""
                    checks.append(check(rr.name + ":untouched", "skipped",
                                        "no commit beyond origin/%s: nothing of this change lives here, so no bar runs in"
                                        " repos/%s" % (default, rr.name)))
                elif p is not None:
                    rep.update(omit_empty({"plan": p.status, "plan_reason": p.reason}))
                    if p.report is not None:
                        rep.update(omit_empty({"stack": p.report.stack, "conf": p.report.conf, "mode": p.mode,
                                               "engine_ref": p.engine_ref, "paths": p.report.paths,
                                               "diagnostics": p.report.diagnostics}))
                        if p.mode == TIER_FULL and p.report.e2e_selection is not None:
                            rep["e2e_selection"] = p.report.e2e_selection
                        conf_changed[rr.name] = p.report.conf_changed_in_diff
                        paths[rr.name] = p.report.paths
                        base = rr.merge.base if rr.merge is not None else ""
                        refs.append("repos/%s: --base %s, conf %s, mode %s" % (rr.name, short(base), p.report.conf, p.report.mode))
                        if p.report.conf_changed_in_diff:
                            checks.append(check(rr.name + ":conf-changed", "skipped",
                                                "the repo's .polyforge/verify-scope.conf changed in this diff: the plan was"
                                                " read from HEAD's conf — deliver and the owner see it"))
                        if p.report.diagnostics:
                            checks.append(check(rr.name + ":engine-diagnostics", "skipped",
                                                cap_text("the scope engine reported: " + " | ".join(p.report.diagnostics))))
                    else:
                        rep.update(omit_empty({"mode": p.mode, "engine_ref": p.engine_ref}))
                        refs.append("repos/%s: engine failed" % rr.name)
                    if not p.planned():
                        unplanned.append(rr.name)
                        checks.append(check(rr.name + ":plan", "skipped",
                                            p.reason + " — the step derives and runs this repo's build/lint/test bar itself"))
                    if not p.planned() or p.mode != TIER_LIGHT or p.nested_full:
                        effective_light = False
                        full_why.extend("repos/" + rr.name + ": " + why for why in full_tier_reasons(p))
                    else:
                        light_repos += 1
                if rr.box is not None:
                    rep["container"] = rr.box.report.as_dict()
                for cr in rr.runs:
                    ck = command_check(cr)
                    checks.append(ck)
                    commands.append(omit_empty({
                        "check": cr.check, "name": cr.name, "phase": cr.phase, "origin": cr.origin, "command": cr.cmd,
                        "status": ck["status"], "exit_code": cr.exit, "timed_out": cr.timed_out, "skipped": cr.skipped,
                        "not_started": cr.not_started, "log_path": self.rel(cr.log_path), "tmp_dir": cr.tmp_dir,
                        "report": cr.report_read.as_dict() if cr.report_read is not None else None,
                    }, keep=("check", "name", "phase", "origin", "command", "status", "exit_code")))
                    for u in cr.units:
                        units.append(unit_report(rr.name, cr, u))
                        if u.pre_existing:
                            pre_existing.append(pre_existing_entry(u))
                for s in rr.unit_runs:
                    c, ur, rec, crep = self.unit_run_assembly(s)
                    checks.append(c)
                    units.append(ur)
                    unit_runs.append(rec)
                    commands.append(crep)
                for s in rr.analysis_runs:
                    c, ur, rec, crep = self.analysis_assembly(s)
                    checks.append(c)
                    units.append(ur)
                    analysis_runs.append(rec)
                    commands.append(crep)
                checks.extend(test_file_checks(rr))
                # the analysis's machine selection record of rr, when it
                # carries one: the one shared validator (pfstep/analysis),
                # the head it is judged at being the tree this run
                # verifies. Whether a touched repo carries one at all is
                # no machine question (the owner's Q1 ruling: the build
                # step's own text demands the record, review judges its
                # absence; a full-only repo has none to carry)
                sel = self.analysis_selected.get(rr.name)
                if sel is not None:
                    head = rr.merge.head if rr.merge is not None else ""
                    # the record is judged at the tree it was computed at:
                    # the build close's own HEAD, which a sync's --no-ff
                    # merge of origin's movement may sit under (only merge
                    # commits between — the change's own tree unchanged);
                    # anything else is a stale record
                    judged = head
                    if head and sel.head != head and merge_only_between(rr.dir, sel.head, head):
                        judged = sel.head
                    problem = selected_problem(sel, judged)
                    checks.append(check(rr.name + ":selected", "fail" if problem else "pass",
                                        problem or ("the selection record of " + short(sel.head))))
                rep["commands"] = commands
                reports.append(rep)
            have = {rr.name for rr in repos}
            for repo in (sorted(self.analysis_selected) if self.analysis_given else []):
                if repo not in have:
                    checks.append(check(repo + ":selected", "fail",
                                        "the analysis's selected record names no task repo of this capsule"))
            for d in self.unit_run_decls:
                if d.repo not in have:
                    s = UnitRunState(decl=d, problem=unknown_repo_problem(d.repo))
                    c, ur, rec, _ = self.unit_run_assembly(s)
                    checks.append(c)
                    units.append(ur)
                    unit_runs.append(rec)
            for ad in self.analysis_taken:
                if ad.repo not in have:
                    st = AnalysisState(decl=ad, problem=unknown_repo_problem(ad.repo))
                    c, ur, rec, _ = self.analysis_assembly(st)
                    checks.append(c)
                    units.append(ur)
                    analysis_runs.append(rec)
            for ad in self.analysis_cut:
                checks.append(check(ad.check(), "skipped", cap_text(
                    "listed after the analysis's first %d (its supplement cap): not run — the engine's"
                    " selection is unchanged" % self.analysis_limit)))
                analysis_runs.append({"id": ad.id, "check": ad.check(), "repo": ad.repo, "status": "cut",
                                      "why": ad.why, "file": ad.file, "name": ad.name})
            if not self.tier_light:
                # a full run is full by its own decision, never by a
                # report's fallback
                tier["reasons"] = [self.tier_reason or "the full tier was requested"]
            elif effective_light and light_repos > 0:
                tier["effective"] = TIER_LIGHT
            else:
                if not full_why:
                    full_why = ["no touched repo has a plan"]
                tier["reasons"] = full_why
            scope = {"fast_gates_ref": ""}
            if refs:
                scope["fast_gates_ref"] = self.engine + " --json (" + "; ".join(refs) + ")"
            if paths:
                scope["paths"] = {k: paths[k] for k in sorted(paths)}
            verdict = "pass"
            fails = []
            for c in checks:
                if c["status"] == "fail":
                    verdict = "fail"
                    fails.append(c["name"] + ": " + c.get("detail", ""))
            fail_reason = "\n".join(fails)
        doc["mechanical_verdict"] = verdict
        if fail_reason:
            doc["fail_reason"] = fail_reason
        doc["deadline"] = duration.format(self.deadline)
        doc["tier"] = tier
        doc["checks"] = checks
        doc["pre_existing_failures"] = pre_existing
        doc["merge_outcomes"] = {k: merges[k].as_dict() for k in sorted(merges)}
        doc["repos"] = reports
        doc["unplanned_repos"] = unplanned
        doc["units"] = units
        if unit_runs:
            doc["unit_runs"] = unit_runs
        if analysis_runs:
            doc["analysis_runs"] = analysis_runs
        doc["scope"] = scope
        doc["conf_changed_in_diff"] = {k: conf_changed[k] for k in sorted(conf_changed)}
        return doc

    def unit_run_assembly(self, s: UnitRunState) -> Tuple[Dict[str, Any], Dict[str, Any], Dict[str, Any], Dict[str, Any]]:
        """One unit run's check, unit, record and command report."""
        return self.recorded_assembly(s, KIND_UNIT_RUN, ORIGIN_UNIT_RUN, "unit-run", "unit")

    def analysis_assembly(self, s: AnalysisState) -> Tuple[Dict[str, Any], Dict[str, Any], Dict[str, Any], Dict[str, Any]]:
        """One listed test's check, unit, record and command report."""
        return self.recorded_assembly(s, KIND_ANALYSIS, ORIGIN_ANALYSIS, "analysis", "the analysis entry")

    def recorded_assembly(
        self, s: UnitRunState, kind: str, origin: str, crep_name: str, noun: str
    ) -> Tuple[Dict[str, Any], Dict[str, Any], Dict[str, Any], Dict[str, Any]]:
        """One recorded run's check, unit, record and command report (a unit
        run or an analysis supplement; the wording names the noun)."""
        d = s.decl
        name = d.check()
        line = argv_line(s.cwd, d.argv)
        cwd = "" if s.cwd == "." else s.cwd
        status = s.status()
        c_status, detail = "pass", ""
        exit_code: Optional[int] = None
        log = ""
        if s.problem:
            c_status, detail = "fail", "%s %s cannot run as written: %s" % (noun, d.id, s.problem)
        elif not s.ran:
            c_status, detail = "fail", "`%s` %s (the bar was not run in full: never a pass)" % (line, s.why)
        else:
            exit_code = s.exit
            log = self.rel(s.log_path)
            if s.flaky:
                detail = "`%s` passed on re-run %d (log %s)" % (line, s.runs, log_ref(s.log_path))
            elif s.passed:
                detail = "`%s` exit 0 (log %s)" % (line, log_ref(s.log_path))
            else:
                c_status, detail = "fail", "`%s` %s, still failing after %d run(s) (log %s)" % (line, s.why, s.runs, log_ref(s.log_path))
                if s.crashed:
                    detail += "; " + BROWSER_CRASH_NOTE
        detail = cap_text(detail)
        c = check(name, c_status, detail)
        unit = omit_empty({"repo": d.repo, "check": name, "kind": kind, "name": d.id, "status": status,
                           "protected": True, "marker": cap_text(d.marker), "detail": detail,
                           "failure": failure_of(s.crashed) if status != "pass" else "", "not_rerun": s.not_rerun},
                          keep=("repo", "check", "kind", "name", "status"))
        rec: Dict[str, Any] = {"id": d.id, "check": name, "repo": d.repo}
        if cwd:
            rec["cwd"] = cwd
        rec["argv"] = list(d.argv)
        if d.marker:
            rec["marker"] = d.marker
        rec["status"] = status
        if exit_code is not None:
            rec["exit_code"] = exit_code
        rec["runs"] = s.runs
        if log:
            rec["log"] = log
        if detail:
            rec["detail"] = detail
        crep: Dict[str, Any] = {"check": name, "name": crep_name + ":" + d.id, "phase": origin, "origin": origin,
                                "command": line}
        if d.argv:
            crep["argv"] = list(d.argv)
        if cwd:
            crep["cwd"] = cwd
        crep["status"] = c_status
        crep["exit_code"] = s.exit if s.ran else -1
        if s.timed_out:
            crep["timed_out"] = True
        if not s.ran and not s.problem and s.why:
            crep["not_started"] = s.why
        if log:
            crep["log_path"] = log
        return c, unit, rec, crep


# ---- the pure rules ----------------------------------------------------------


# The tier names the line may ask for.
TIER_AUTO = "auto"
FULL_TIER_REASON = ("the work item carries the integration_fix attr (an integration batch's fix"
                    " ticket): the full bar runs — it includes every light check")
WORK_ITEM_DOC = "runtime-state/work-item.json"


def resolve_tier(capsule: str, requested: str) -> Tuple[str, str]:
    """The tier the line's request resolves to, and why a full run is full
    ("" = light): auto reads the capsule's work item — a true
    integration_fix attr (the boolean the batch's skills write) runs the
    full bar; light and full are the hand
    run's explicit overrides."""
    if requested != TIER_AUTO:
        return (requested, "" if requested == TIER_LIGHT else "the full tier was requested")
    try:
        with open(os.path.join(capsule, WORK_ITEM_DOC), "rb") as f:
            doc = json.loads(f.read().decode("utf-8", "replace"))
    except (OSError, ValueError):
        return TIER_LIGHT, ""
    attrs = doc.get("attrs") if isinstance(doc, dict) else None
    v = attrs.get("integration_fix") if isinstance(attrs, dict) else None
    if v is True:  # the boolean the integration batch's skills write
        return TIER_FULL, FULL_TIER_REASON
    return TIER_LIGHT, ""


def check(name: str, status: str, detail: str = "") -> Dict[str, Any]:
    out: Dict[str, Any] = {"name": name, "status": status}
    if detail:
        out["detail"] = detail
    return out


def omit_empty(d: Dict[str, Any], keep: Sequence[str] = ()) -> Dict[str, Any]:
    """d without its empty values (Go's omitempty), the keep keys kept."""
    return {k: v for k, v in d.items() if k in keep or (v is not None and v != "" and v is not False and v != [] and v != {})}


def full_tier_reasons(p: RepoPlan) -> List[str]:
    """Why one touched repo's run is not light, one entry per reason: an
    unplanned repo's reason, the repo report's own full-tier fallbacks, and
    each nested root's."""
    if not p.planned():
        return [p.status + ": " + p.reason]
    why: List[str] = []
    if p.mode != TIER_LIGHT and p.report is not None:
        why.extend(fallback_reasons(p.report))
    why.extend(p.nested_full)
    return why or [NO_FALLBACK_DIAGNOSTIC]


def merge_only_between(repo_dir: str, a: str, b: str) -> bool:
    """Whether only merge commits sit between a and b (a an ancestor of
    b): a sync's --no-ff merge of origin's movement leaves the change's
    own commits as they were, so a selection record of a still names the
    change's own tree (review r1 #1)."""
    if a == "" or b == "" or a == b:
        return False
    if git.run(repo_dir, "merge-base", "--is-ancestor", a, b, timeout=GIT_TIMEOUT).exit_code != 0:
        return False
    try:
        out = git_quiet(repo_dir, "rev-list", "--count", "--first-parent", "--no-merges", a + ".." + b)
    except git.GitError:
        return False
    return out.strip() == "0"


def skip_form(cr: CmdRun) -> Tuple[str, str]:
    """cr's bar form with its pre-existing units excluded, or the run form
    plus a note when it cannot exclude them."""
    pre = [u for u in cr.units if u.pre_existing]
    if not pre:
        return cr.cmd, ""
    words, simple = shell_words(cr.cmd)
    kind = pre[0].kind
    if any(u.kind != kind for u in pre):
        kind = ""
    names = [u.name for u in pre]
    if kind == KIND_GO and simple and has_go_test(words) and not has_flag(words, "-skip"):
        return cr.cmd + " -skip '^(" + "|".join(names) + ")$'", ""
    if kind == KIND_GO and simple and cr.harness and not has_assignment(cr.cmd, "E2E_SKIP"):
        # the e2e harness protocol: E2E_SKIP excludes top-level tests
        return "E2E_SKIP='^(" + "|".join(names) + ")$' " + cr.cmd, ""
    if kind == KIND_PYTEST and simple and token_index(words, "pytest") >= 0:
        return cr.cmd + "".join(" --deselect " + shell_quote(n) for n in names), ""
    return cr.cmd, ("repos/%s `%s` carries the pre-existing failure(s) %s but has no skip switch verify-run can apply: it"
                    " is listed as run, its failure(s) named here" % (cr.repo, cr.cmd, ", ".join(names)))


def command_check(cr: CmdRun) -> Dict[str, Any]:
    """One bar command's check entry."""
    if cr.skipped:
        return check(cr.check, "skipped", "`%s` not run: %s" % (cr.cmd, cr.skipped))
    if cr.not_started:
        # the bar was not run in full: never a pass
        return check(cr.check, "fail", "`%s` %s" % (cr.cmd, cr.not_started))
    log = log_ref(cr.log_path)
    if cr.exit == 0:
        return check(cr.check, "pass", "`%s` exit 0 (log %s)" % (cr.cmd, log))
    new_fails: List[str] = []
    pre: List[str] = []
    flaky: List[str] = []
    for u in cr.units:
        if u.flaky:
            flaky.append("%s (%s)" % (u.name, u.flaky))
        elif u.pre_existing:
            pre.append(pre_existing_entry(u) + " — " + u.pre_detail)
        elif u.failed:
            s = u.name
            if u.unit.signature:
                s += ": " + u.unit.signature
            if u.note:
                s += " [" + u.note + "]"
            if u.unit.crashed:
                s += " (" + BROWSER_CRASH_NOTE + ")"
            new_fails.append(s)
    head = "`%s` exit %d" % (cr.cmd, cr.exit)
    if cr.deadline_hit:
        head += " (killed at verify-run's deadline)"
    elif cr.timed_out:
        head += " (killed at its timeout)"
    parts: List[str] = []
    if new_fails or cr.timed_out:
        status = "fail"
        if new_fails:
            parts.append("new failures: " + "; ".join(new_fails))
    elif pre:
        status = "pre_existing"
    else:
        status = "pass"
    if pre:
        parts.append("pre-existing: " + "; ".join(pre))
    if flaky:
        parts.append("passed on re-run: " + "; ".join(flaky))
    if cr.confirmed:
        parts.append("re-run whole as `%s`: exit 0" % cr.confirmed)
    if cr.completion:
        parts.append("complete: " + cr.completion)
    return check(cr.check, status, cap_text(head + "; " + "; ".join(parts) + " (log " + log + ")"))


def failure_of(crashed: bool) -> str:
    """A first-run failure's class."""
    return FAILURE_BROWSER_CRASH if crashed else FAILURE_ASSERTION


def pre_existing_entry(u: UnitState) -> str:
    name = u.name
    if u.kind == KIND_PYTEST and "::" in name:
        name = name.split("::", 1)[1]
    return u.file + ": " + name


def unit_report(repo: str, cr: CmdRun, u: UnitState) -> Dict[str, Any]:
    if u.flaky:
        status, detail = "flaky", u.flaky + ": passed on a re-run at HEAD"
    elif u.pre_existing:
        status, detail = "pre_existing", u.pre_detail
    elif u.failed:
        status, detail = "fail", u.note
    else:
        status, detail = "pass", ""
    return omit_empty({
        "repo": repo, "check": cr.check, "kind": u.kind, "name": u.name, "file": u.file, "status": status,
        "protected": u.protected, "marker": cap_text(u.unit.marker), "signature": u.unit.signature,
        "detail": cap_text(detail), "carries": list(u.carries),
        "failure": failure_of(u.unit.crashed) if status != "pass" else "", "not_rerun": u.not_rerun,
    }, keep=("repo", "check", "kind", "name", "status"))


def test_file_checks(rr: RepoRun) -> List[Dict[str, Any]]:
    """rr's no-untracked-test-files check (before and after the bar)."""
    problems: List[str] = []
    for when, scan in (("before the bar", rr.untracked_before), ("after the bar", rr.untracked_after)):
        if scan.err:
            problems.append("git status failed " + when + ": " + scan.err)
        if scan.files:
            problems.append("untracked test files " + when + ": " + ", ".join(scan.files))
    if problems:
        return [check(rr.name + ":no-untracked-test-files", "fail", "; ".join(problems))]
    return [check(rr.name + ":no-untracked-test-files", "pass",
                  "git status --porcelain --untracked-files=all lists no untracked test file before or after the bar")]


# ---- the report ----------------------------------------------------------------


def _cell(s: str) -> str:
    return s.replace("\n", " ").replace("|", "\\|")


def _crash_mark(u: Dict[str, Any]) -> str:
    return " — **" + BROWSER_CRASH_NOTE + "**" if u.get("failure") == FAILURE_BROWSER_CRASH else ""


def _code_list(xs: Optional[Sequence[str]]) -> str:
    if not xs:
        return "none"
    return ", ".join("`" + x + "`" for x in xs)


def render_report(res: Dict[str, Any]) -> str:
    """report-mechanical.md: the mechanical section of the verification report."""
    b: List[str] = ["## Mechanical run (run %d)\n\n" % res["run"]]
    if res.get("incomplete"):
        b.append("**Incomplete — no merged-tree proof could be made:** %s\n\n" % res["incomplete"])
    else:
        t = res["tier"]
        b.append("Mechanical verdict: **%s**. Tier: requested %s, effective %s.\n\n" % (res["mechanical_verdict"], t["requested"], t["effective"]))
        if t.get("reasons"):
            b.append("Why the run is full:\n\n")
            b.extend("- %s\n" % why for why in t["reasons"])
            b.append("\n")
    b.append("Command logs (local, not part of the capsule chain): `%s/`. Whole-run deadline: %s.\n\n" % (res["log_dir"], res["deadline"]))
    b.append("### Merge of the latest default branch\n\n")
    b.append("| repo | default | outcome | HEAD | base |\n| --- | --- | --- | --- | --- |\n")
    merges = res["merge_outcomes"]
    for name in sorted(merges):
        m = merges[name]
        outcome = m["outcome"] + ("" if m["touched"] else ", untouched")
        b.append("| %s | %s | %s | %s | %s |\n" % (name, m["default_branch"], outcome, short(m["head"]), short(m["base"])))
    b.append("\n")
    for name in sorted(merges):
        m = merges[name]
        if m.get("baseline"):
            b.append("Light-tier baseline of %s: %s (%s).\n" % (name, m.get("baseline_ref", ""), short(m["baseline"])))
    if res.get("repos"):
        b.append("### Plans\n\n")
        for rp in res["repos"]:
            if not rp["touched"]:
                b.append("- `%s`: untouched — no bar.\n" % rp["name"])
            elif rp.get("plan") != PLAN_PLANNED:
                b.append("- `%s`: **unplanned** (%s): %s\n" % (rp["name"], rp.get("plan", ""), rp.get("plan_reason", "")))
            else:
                b.append("- `%s`: stack %s, conf %s, mode %s, %d command(s) — `%s`\n" % (
                    rp["name"], rp.get("stack", ""), rp.get("conf", ""), rp.get("mode", ""), len(rp["commands"]), rp.get("engine_ref", "")))
            for cmd in rp.get("commands") or []:
                if cmd.get("tmp_dir"):
                    b.append("  - `%s`: private TMPDIR `%s` (%d bytes)\n" % (cmd["check"], cmd["tmp_dir"], len(cmd["tmp_dir"].encode("utf-8"))))
            c = rp.get("container")
            if c:
                if c.get("digest"):
                    b.append("  - ran in container `%s` from the prepared image `%s` (local ID `%s`), limits cpus %s,"
                             " memory %s, pids %d\n" % (c["name"], c["image"], c["digest"], c["cpus"], c["memory"], c["pids"]))
                else:
                    built = "built by this run" if c["built"] else "present"
                    b.append("  - ran in container `%s` from image `%s` (%s; build log `%s`), limits cpus %s, memory %s, pids %d\n" % (
                        c["name"], c["image"], built, c.get("build_log", ""), c["cpus"], c["memory"], c["pids"]))
                if c.get("lost"):
                    b.append("  - **the container became unusable:** %s\n" % c["lost"])
                if c.get("left"):
                    b.append("  - the container was left behind: %s\n" % c["left"])
            s = rp.get("e2e_selection")
            if s:
                b.append("  - e2e selection (smoke_suite opt-in): mode **%s**; suites %s; uncovered paths %s; carried full command(s) %s\n" % (
                    s["mode"], _code_list(s.get("suites")), _code_list(s.get("uncovered_paths")), _code_list(s.get("full_commands"))))
        b.append("\n")
    supplements = res.get("analysis_runs") or []
    if supplements:
        cut = [a for a in supplements if a.get("status") == "cut"]
        b.append("### The change analysis's supplements\n\n")
        b.append("Listed tests run beside the plan (the union is add-only; the engine's selection is unchanged).")
        if cut:
            b.append(" Over the supplement cap the tail was cut, in listed order, and named here.\n\n")
        else:
            b.append("\n\n")
        for a in supplements:
            what = a.get("file", "") + (": " + a["name"] if a.get("name") else "")
            line = "- `%s` (%s) — %s" % (a["id"], what, a.get("why", ""))
            if a.get("status") == "cut":
                line += " — cut over the cap, not run"
            b.append(line + "\n")
        b.append("\n")
    if res["checks"]:
        b.append("### Checks\n\n| check | status | detail |\n| --- | --- | --- |\n")
        for c in res["checks"]:
            b.append("| %s | %s | %s |\n" % (c["name"], c["status"], _cell(c.get("detail", ""))))
        b.append("\n")
    flaky = [u for u in res["units"] if u["status"] == "flaky"]
    pre = [u for u in res["units"] if u["status"] == "pre_existing"]
    if pre:
        b.append("### Pre-existing failures (reported, never this change's fault)\n\n")
        for u in pre:
            b.append("- `%s: %s` (%s) — %s%s\n" % (u.get("file", ""), u["name"], u["check"], u.get("detail", ""), _crash_mark(u)))
        b.append("\n")
    if flaky:
        b.append("### Flaky units (failed, then passed on a re-run at HEAD)\n\n")
        for u in flaky:
            b.append("- `%s` (%s) — %s; first failure: %s%s\n" % (u["name"], u["check"], u.get("detail", ""), u.get("signature", ""),
                                                                _crash_mark(u)))
        b.append("\n")
    not_rerun: Dict[str, List[str]] = {}
    for u in res["units"]:
        if u.get("not_rerun"):
            not_rerun.setdefault(u["check"], []).append(u["name"])
    if not_rerun:
        b.append("### Not re-run (failed; the budget or the deadline ran out before their re-run)\n\n")
        for ck, names in not_rerun.items():
            b.append("- `%s`: %s\n" % (ck, _code_list(names)))
        b.append("\n")
    if res["unplanned_repos"]:
        b.append("Unplanned repos (their bar is the step's own): %s.\n" % ", ".join(res["unplanned_repos"]))
    return "".join(b)


def details_of(res: Dict[str, Any]) -> Dict[str, Any]:
    """The result file's details, folded from result.json's document."""
    out_dir = res["out_dir"]
    d: Dict[str, Any] = {"tier": res["tier"]}
    if res.get("image"):
        d["image"] = res["image"]
    if res["mechanical_verdict"]:
        d["mechanical_verdict"] = res["mechanical_verdict"]
    if res.get("fail_reason"):
        d["fail_reason"] = res["fail_reason"]
    if res.get("incomplete"):
        d["incomplete"] = res["incomplete"]
    cmds: Dict[str, Dict[str, Any]] = {}
    repo_of: Dict[str, str] = {}
    repos: List[Dict[str, Any]] = []
    merges = res["merge_outcomes"]
    for r in res["repos"] or []:
        vr: Dict[str, Any] = {"name": r["name"], "touched": r["touched"]}
        m = merges.get(r["name"])
        if m is not None:
            vr.update(omit_empty({"head": m["head"], "default_branch": m["default_branch"], "base": m["base"]}))
        if r.get("plan"):
            vr["plan"] = r["plan"]
        if r.get("container"):
            vr["container"] = r["container"]["name"]
        # the Go struct's order: name, touched, head, default_branch, base, plan, container
        repos.append({k: vr[k] for k in ("name", "touched", "head", "default_branch", "base", "plan", "container") if k in vr})
        for c in r["commands"]:
            cmds[c["check"]] = c
            repo_of[c["check"]] = r["name"]
    units: Dict[str, List[Dict[str, Any]]] = {}
    for u in res["units"]:
        units.setdefault(u["check"], []).append(omit_empty({
            "unit": u["name"], "kind": u["kind"], "status": u["status"], "flaky": u["status"] == "flaky",
            "pre_existing": u["status"] == "pre_existing", "detail": u.get("detail", ""),
            "failure": u.get("failure", ""), "not_rerun": u.get("not_rerun", False),
        }, keep=("unit", "kind", "status", "flaky", "pre_existing")))
    unit_runs = {ur["check"]: ur for ur in res.get("unit_runs", [])}
    for ar in res.get("analysis_runs", []):
        if ar.get("argv"):
            unit_runs[ar["check"]] = ar
    checks: List[Dict[str, Any]] = []
    for c in res["checks"]:
        vc: Dict[str, Any] = {"id": c["name"]}
        ur = unit_runs.get(c["name"])
        cr = cmds.get(c["name"])
        if ur is not None:
            vc.update(omit_empty({"repo": ur["repo"], "argv": ur["argv"], "cwd": ur.get("cwd", "")}))
            if "exit_code" in ur:
                vc["exit"] = ur["exit_code"]
        elif cr is not None:
            vc.update({"repo": repo_of[c["name"]], "argv": ["bash", "-c", cr["command"]], "exit": cr["exit_code"]})
        else:
            repo, sep, _ = c["name"].partition(":")
            if sep and repo in merges:
                vc["repo"] = repo
        vc["status"] = c["status"]
        if c.get("detail"):
            vc["detail"] = c["detail"]
        log = ur.get("log", "") if ur is not None else (cr.get("log_path", "") if cr is not None else "")
        if log:
            vc["log"] = log
        if cr is not None and cr.get("tmp_dir"):
            vc["tmp_dir"] = cr["tmp_dir"]
        if units.get(c["name"]):
            vc["units"] = units[c["name"]]
        checks.append({k: vc[k] for k in ("id", "repo", "argv", "cwd", "exit", "status", "detail", "log", "tmp_dir", "units") if k in vc})
    d["checks"] = checks
    d["pre_existing_failures"] = list(res["pre_existing_failures"])
    d["repos"] = repos
    d["result"] = gostr.path_join(out_dir, "result.json")
    d["report"] = gostr.path_join(out_dir, "report-mechanical.md")
    return d


def logs_of(res: Dict[str, Any], d: Dict[str, Any]) -> List[str]:
    """What the run wrote: its result and report, every image build log,
    every command log."""
    logs = [d["result"], d["report"]]
    for r in res["repos"] or []:
        c = r.get("container")
        if c and c.get("build_log"):
            logs.append(c["build_log"])
        for cmd in r["commands"]:
            if cmd.get("log_path"):
                logs.append(cmd["log_path"])
    return logs


# ---- the command -----------------------------------------------------------------


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog=COMMAND, description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter, allow_abbrev=False)
    p.add_argument("--tier", default="", help="the verification tier: auto (the work item's attrs decide — an"
                   " integration fix ticket runs the full bar), light or full (a hand run's explicit tier)")
    p.add_argument("--capsule", default=".", help="the capsule root (capsule.json, repos/)")
    p.add_argument("--engine", default="", help="the scope engine (default: the shared tree's scripts/verify-scope)")
    p.add_argument("--unit-runs", default="", help="the build's units.json: every unit's recorded run, run again as"
                   " written beside the plan")
    p.add_argument("--analysis", default="", help="the build's change analysis (artifacts/build/change-analysis.json):"
                   " the tests it lists run beside the plan under its supplement cap, add-only")
    p.add_argument("--deadline", default="", help="the whole run's deadline, a Go duration (default: derived from the"
                   " step's tool window, two minutes under it)")
    p.add_argument("--log-dir", default="", help="where the logs go (default: logs/ beside --out, else the capsule's"
                   " runtime-state/verify/<n>)")
    p.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    p.add_argument("--out", default="", help="write the result file there (atomically)")
    p.add_argument("--image", default="", help="the prepared image every touched repo's bar runs in (default: each"
                   " repo by its own .polyforge/Dockerfile.verify); a step dispatch's PF_PARAM_IMAGE names it too —"
                   " --image wins, and the result records which one did")
    return p


def main(
    argv: Optional[List[str]] = None,
    environ: Optional[Dict[str, str]] = None,
    runtime: Optional[container.ContainerRuntime] = None,
) -> int:
    """The command: argv (default sys.argv[1:]) under environ (default the
    process's); runtime replaces the docker CLI (a test's fake engine)."""
    args_list = list(sys.argv[1:] if argv is None else argv)
    env = dict(os.environ if environ is None else environ)
    res = result.begin(COMMAND, args_list, VERSION, env)
    p = parser()
    try:
        a = p.parse_args(args_list)
    except SystemExit as e:
        return int(e.code or 0)

    def say(msg: str) -> None:
        progress.emit("%s: %s" % (COMMAND, msg), sys.stderr, env)

    try:
        code = run(a, env, res, say, runtime)
    except Usage as err:
        say(str(err))
        res.summary = str(err)
        code = 2
    except (KeyboardInterrupt, runner.Interrupted) as err:
        res.summary = "not judged: verify-run was interrupted (%s)" % (err or "an interrupt")
        code = 2
    except Exception as err:  # a fault of the script is never a verdict
        traceback.print_exc()
        res.summary = "the verification could not run: %s: %s" % (type(err).__name__, err)
        code = 2
    try:
        result.finish(res, code, a.out or None)
    except OSError as err:
        say("the result could not be written to %s: %s" % (a.out, err))
        code = 2
        res.exit_code = code
    if a.json:
        print(jsonout.line(res.to_dict()))
    elif res.summary:
        print("%s: %s (%s)" % (COMMAND, res.summary, res.verdict))
    return code


def run(
    a: argparse.Namespace,
    env: Dict[str, str],
    res: result.Result,
    say: Callable[[str], None],
    runtime: Optional[container.ContainerRuntime] = None,
) -> int:
    if a.tier == "":
        raise Usage("--tier is required (light, full or auto: the lane's line says auto — the work item's attrs decide)")
    if a.tier not in (TIER_LIGHT, TIER_FULL, TIER_AUTO):
        raise Usage("--tier %s: want light, full or auto (auto reads the work item's attrs — an integration"
                    " fix ticket runs the full bar)" % gostr.quote(a.tier))
    capsule = os.path.abspath(a.capsule)
    eng = os.path.abspath(a.engine or default_engine(env))
    if not os.path.isfile(eng):
        raise Usage("no scope engine at %s (--engine names it; the shared tree's is <PF_SHARED_DIR>/scripts/verify-scope)"
                    % eng)
    decls: List[UnitRunDecl] = []
    if a.unit_runs:
        try:
            decls = read_unit_runs(a.unit_runs)
        except ValueError as err:
            raise Usage("--unit-runs: %s" % err) from None
    analysis: Optional[Analysis] = None
    if a.analysis:
        try:
            analysis = read_analysis(a.analysis)
        except ValueError as err:
            raise Usage("--analysis: %s" % err) from None
    if a.deadline:
        try:
            window = duration.parse(a.deadline)
        except ValueError as err:
            raise Usage("--deadline: %s" % err) from None
        if window <= 0:
            window = default_deadline_for(env)
    else:
        window = default_deadline_for(env)
    logs = a.log_dir or (os.path.join(os.path.dirname(a.out), "logs") if a.out else "")
    deadline_at = time.time() + window / duration.SECOND
    # the options of every container the run starts (one boot label for
    # all); a repo needs one only when its tree carries the Dockerfile
    co = container.for_run(container.ContainerOptions(runtime=runtime, say=say), deadline_at, env)
    try:
        co.engine().available()
    except container.ContainerError:
        pass  # nothing to sweep; a repo that needs a container says so
    else:
        container.sweep_run(co, say)
    tmpdir.sweep_stale()
    repos, why = capsule_repos(capsule)
    if why:
        res.summary = "the verification could not run: %s" % why
        return 2
    cmd_env = dict(env)
    image = a.image or env.get("PF_PARAM_IMAGE", "")
    image_source = "flag" if a.image else ("param" if image else "")
    try:
        tier, tier_reason = resolve_tier(capsule, a.tier)
        v = Verification(capsule, eng, window, logs, decls, cmd_env, say, co, analysis=analysis,
                         tier=tier, tier_requested=a.tier, tier_reason=tier_reason,
                         image=image, image_source=image_source)
    except OSError as err:
        res.summary = "the verification could not run: %s" % err
        return 2
    v.repos_names = repos
    v.fold_unit_runs()
    doc = v.run()
    if image:
        doc["image"] = {"value": image, "source": image_source}
    try:
        with open(os.path.join(v.run_dir, "result.json"), "w", encoding="utf-8") as f:
            f.write(json.dumps(doc, ensure_ascii=False, indent=2) + "\n")
        with open(os.path.join(v.run_dir, "report-mechanical.md"), "w", encoding="utf-8") as f:
            f.write(render_report(doc))
    except OSError as err:
        res.summary = "the verification could not run: %s" % err
        return 2
    d = details_of(doc)
    res.details = d
    res.logs = logs_of(doc, d)
    for name, m in doc["merge_outcomes"].items():
        if m["head"]:
            res.heads[name] = m["head"]
    if doc.get("incomplete"):
        res.summary = "not judged: " + doc["incomplete"]
        return 2
    if doc["mechanical_verdict"] != "pass":
        fails = sum(1 for c in doc["checks"] if c["status"] == "fail")
        res.summary = "%d check(s) failed: %s" % (fails, doc.get("fail_reason", "").split("\n", 1)[0])
        return 1
    over = ", ".join("repos/" + r["name"] for r in doc["repos"] or [])
    res.summary = "%s verification passed: %d check(s) over %s (tier %s)" % (
        doc["tier"]["effective"], len(doc["checks"]), over, doc["tier"]["effective"])
    return 0


def capsule_repos(capsule: str) -> Tuple[List[str], str]:
    """The task repos capsule.json names that are cloned under repos/, sorted;
    or why there are none."""
    try:
        with open(os.path.join(capsule, "capsule.json"), "rb") as f:
            raw = f.read()
    except OSError as err:
        return [], "not a capsule root (capsule.json unreadable: %s)" % err
    try:
        ident = json.loads(raw.decode("utf-8", "replace"))
    except ValueError as err:
        return [], "capsule.json is not valid JSON: %s" % err
    names = ident.get("repos") if isinstance(ident, dict) else None
    repos = sorted(n for n in (names or {}) if isinstance(names, dict) and os.path.isdir(os.path.join(capsule, "repos", n)))
    if not repos:
        return [], "capsule.json names no task repo present under repos/"
    return repos, ""


if __name__ == "__main__":
    # SIGTERM / SIGHUP unwind like an interrupt: the containers are removed
    # and the result is written, not judged
    runner.raise_on_termination()
    sys.exit(main())
