"""fast_checks.py — build's written fast/scoped check: the scope engine's
default reports for one task repo, their fast- and scoped-phase commands run
as written (the full phase is never run), each command in its report's
``root``, the result file the artifact build judges red or green from.

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo <name> \\
        [--phase fast] [--root <dir>]... \\
        [--capsule DIR] [--engine PATH] [--deadline D] [--log-dir DIR] [--json] [--out PATH]
    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo <name> --close-scoped [--reuse FILE]... \\
        [--capsule DIR] [--engine PATH] [--deadline D] [--log-dir DIR] [--json] [--out PATH]
    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --check-units <units.json> [--unit <id>] \\
        [--capsule DIR] [--deadline D] [--log-dir DIR] [--json] [--out PATH]

The command answers what `workflows/_shared/fast_checks_build.md` used to
ask the model to do by hand (polyforge#24: a report's commands were copied
out and run at the repository root, so a nested root's `npm ci` hit the
wrong directory and the step blocked on a dependency problem that did not
exist) — and what no one checked at record time (polyforge#23: a marker
wrong by one `$` was only found at verification, costing the lane a
rewind):

- it runs the scope engine ``--json`` in the repo clone
  (``<capsule>/repos/<name>``; the engine in this lane's snapshot, or
  --engine) — the default report, the one the model ran by hand, so a
  change on a forced-full path behaves exactly as before (its fast gates
  and nothing more) — decodes the report and takes the commands whose
  ``phase`` is ``fast`` or ``scoped``, never one whose ``phase`` is
  ``full``, in the report's order. ``--phase fast`` narrows that to the
  fast commands alone (the per-unit bar's own shape: the scoped
  selection is the close's, never the per-unit's), and a repeatable
  ``--root <dir>`` to the named engine roots — ``.`` the repository's own
  root, a nested root its directory, a name that is neither a usage
  error — the roots the run never asks keeping their green entries
  carried in the result file; without the flags every root the change
  reaches runs, exactly as before (the close's own shape);
- every command runs as ``bash -c <cmd>`` **in the report's ``root``**, the
  directory the engine itself names, with the command's own declared
  ``timeout`` (a command declaring none gets the run's default, 20m) and a
  private TMPDIR, in its own process group, killed whole at its timeout;
- a command missing a declared prerequisite (``requires_env`` unset or
  empty, ``requires_cmd`` not resolvable) is skipped and its reason
  recorded — a skip is not a red check;
- the run never stops at a failure: every command runs, and each one's
  exit code, its log and its skip reason land in the result file's
  ``details.commands[]`` — any non-zero exit (or a command killed at its
  timeout) makes the whole run red, unless the failing units it located
  re-ran flaky and the command was confirmed (below).

The flaky re-run phase (polyforge#157): a red command's failing units are
located from its output and each re-run **once** by its derived targeted
re-run — verification's own rules and implementation (``pfstep/rerun`` +
``pfstep/flaky``, never a second copy) — under the run's deadline. One
passing re-run records the unit ``flaky`` in ``details.commands[].units[]``
beside the first failure's log, and a unit still red after its one re-run
keeps the check red. A red command every failing unit of which re-ran
flaky is then confirmed the way verification confirms its bar: by its
first-run output's completion proof (the declared Go harness's trailer,
an aggregate e2e runner's closing summary — ``pfstep/completion``, the one
implementation) or by one whole re-run exiting 0 — recorded
``confirmed``; a re-run that still exits non-zero, or a deadline that
cannot fit it, leaves the command red (``not_confirmed``). No re-run ever
happens by hand: the check already contains it, and its result is the
record.

The baseline triage (polyforge#212): a unit still red after its one
re-run is judged on the change's baseline — the same judgment
verification's pre-existing phase runs, one entry
(``pfstep/preexisting.py``'s ``judge_on_base``, the executors this run's
own: the worktree on the host, the base setup and the re-runs over it).
A throwaway worktree of ``merge-base HEAD origin/<branch>``, the report's
declared ``base_setup``, then the unit's own targeted re-run there: the
same signature on every base run is ``pre_existing`` (not this change's
red — the run may pass, the record carrying its signature and the
baseline commit), a test the base lacks or that passes there is
``this_change``, and anything the base runs could not settle is
``undetermined`` — fail closed, the red rules hold. ``--close-scoped``
takes ``--unit-runs <units.json>``: the files its test commits touched
are protected, never pre-existing. A pre-existing failure is excluded
from its command's confirming re-run by the skip form verification uses
(go ``-skip``, ``E2E_SKIP``, pytest ``--deselect``); a command that
cannot exclude it and has no completion proof keeps a residual
whole-command failure. ``details.baseline`` records the judgment's own
facts (commit, setup, whether it ran, a whole-batch failure's note) and
``details.reds[]`` every still-red unit (or command) with its class,
signatures, baseline and why.

The reuse phase (polyforge#182): a ``--repo`` run reuses the previous
run's green command instead of executing it when its key — the command
text, its root's subtree tree hash of the working tree (tracked
modifications plus untracked, unignored files; the repository root's key
counts the whole tree, nested roots included), and its sorted declared
``requires_env``/``requires_cmd`` prerequisites — is unchanged. The
reused entry is not executed: it is recorded ``reused`` with its
evidence (the original log path, ``exit_code: 0``) kept. A red,
timed-out, never-started or flaky-confirmed result is never reused (a
plain green run only), and a green entry the run does not plan stays in
the result file (``carried``) so a later run — or the close — can still
reuse it. A previous result file that is missing, unreadable or not a
usable result is no cache: every command runs, never a usage error.

Its logs go to ``<log-dir>/<n>/<repo>/<seq>/command.log``, one directory
``<n>`` per run (a later run's executions never overwrite a reused
entry's original log), the log dir being --log-dir, else ``logs/``
beside --out, else the capsule's next ``runtime-state/fast-checks/<n>``;
a .gitignore keeps them out of the capsule chain. The result
(``pf-step-result/1``) carries the repo's HEAD,
and in ``details`` the engine invocation (``engine``), one entry per report
(``reports[]``: its root, mode and command count) and one per command
(``commands[]``: name, phase, root, cmd, exit_code, timed_out,
duration_ms, log — or skipped and skip_reason — and, on a green run of
the reuse key, the root-subtree tree hash ``tree`` and the serialized
prerequisites ``prereq_key`` beside the reuse marker ``reused``).

``--close-scoped`` is the build close's own mode (polyforge#164): the
same light-tier selection verification runs, planned by the one shared
implementation (``pfstep/scoped_plan.py`` — the same engine argv
``--json --light --base <merge-base>``, the same ``choose``, the same
nested roots), executed on the host over the final tree so the existing
tests the selection pins run before the hand-off, not first at
verification. A command whose reuse key (the same key the ``--repo`` run
reuses by, polyforge#182) a green entry in a ``--reuse`` result file of
this repo already proved — the close's fast-checks run — is not re-run:
its evidence is reused and its entry names the file it came from; a
green-through-flaky confirmation is never that evidence. A red command gets the same one
targeted re-run and whole-command confirmation the per-unit bar gives.
A selection the engine answers with the full tier is never run — the
run records ``incomplete`` with the engine's own fallback reasons and
exits non-zero, claiming nothing (the owner decides; the full bar stays
verification's); a selected command that cannot run (an unmet declared
prerequisite) is the same incomplete, never a skip-as-green. The
result's ``details.selected`` is the change analysis's machine
selection record — the repo's head, tree, base and default branch, the
engine invocations, the conf's identity, the selection's paths and
suites, every command with its run's evidence (or its reuse source),
and the status: ``green``, ``pre_existing`` — every red its baseline
triage kept is pre-existing, nothing this change's, exit 0 and the
shared validator's green-equivalent evidence — ``red`` or
``incomplete`` with its reason. ``--unit-runs <units.json>`` (the
build's own) protects the files its test commits touched: a test this
change authored is never pre-existing.

``--check-units`` is build's on-the-spot unit check: the build's
``artifacts/build/units.json`` re-run as written — every unit's own
``argv`` in its own ``cwd`` of its own ``repos/<repo>`` — each unit judged
by the marker rules verification itself runs (``pfstep.unitruns``: the one
implementation, never a second copy): a unit passes when it exits 0 and
one output line matches its marker; a marker problem (blank, no regular
expression, one matching the empty string), a non-zero exit, a run that
cannot run as written, or no matching line — the #23 shape, a marker
anchored ``$`` against a line carrying a suffix — is red on the spot, and
the result's ``details.units[]`` names the unit, its marker and (on a
mismatch) the closest output line, so build fixes the record before review
or verification ever reads it. A red unit declared ``pre_existing_red``
(its reproduction test predates the lane) is re-run once, verification's
own recorded-run policy: a pass records it ``flaky`` with both logs; a
unit this lane just wrote is never re-run here — a test just written must
be stable, so red it stays. ``--unit <id>`` checks only that unit.
The result carries the repo HEADs the units ran at.

Exit 0: every command that ran exited 0 (skips cost nothing) or turned
green through its flaky re-runs and confirmation, was reused, or carries
only reds the baseline proved pre-existing; every checked unit passed.
Exit 1: a command stayed red after the flaky phase, its baseline triage
or its confirmation, a unit is
red, or the close-scoped selection is red or incomplete. Exit 2: not
judged — no such repo, the engine missing or failing, an unreadable
report, a repo the engine planned no command for, an unusable units.json,
the deadline, an interrupt — or the invocation unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import difflib  # noqa: E402
import json  # noqa: E402
import time  # noqa: E402
import traceback  # noqa: E402
from typing import Any, Callable, Dict, List, Optional, Sequence, Set, Tuple  # noqa: E402

from pfstep import container, duration, git, gostr, runner  # noqa: E402
from pfstep import scoped_plan  # noqa: E402
from pfstep.completion import completion_proof, harness_command, read_go_harness  # noqa: E402
from pfstep.engine import TIER_LIGHT, EngineCommand, EngineReport, ReportError, decode_report  # noqa: E402
from pfstep.engine import argv_of as engine_argv_of  # noqa: E402
from pfstep.engine import default_engine, nested_roots, reached_roots  # noqa: E402
from pfstep.flaky import RerunRun, rerun_flaky  # noqa: E402
from pfstep.invoke import Invocation, duration_flag  # noqa: E402
from pfstep.preexisting import (  # noqa: E402
    CLASS_PRE_EXISTING,
    CLASS_THIS_CHANGE,
    CLASS_UNDETERMINED,
    candidates,
    judge_on_base,
    join_note,
    path_is_protected,
    protected_paths,
    short,
    skip_form,
)
from pfstep.rerun import CommandSpec, UnitState, locate, whole_command  # noqa: E402
from pfstep.runner import Runner, bash_argv, raise_on_termination  # noqa: E402
from pfstep.shellwords import has_flag, has_go_test, shell_quote, shell_words  # noqa: E402
from pfstep.unitruns import UnitRunDecl, clean_cwd, limit_note, marker_matches, marker_problem, read_unit_runs, unit_limit_ns  # noqa: E402
from pfstep.units import KIND_COMMAND, normalize_output, output_lines, parse_units, race_seen  # noqa: E402

NAME = "fast_checks.py"

# A command that declares no timeout runs under the run's own bound (s).
COMMAND_TIMEOUT = 20 * duration.MINUTE / duration.SECOND
GIT_TIMEOUT = 600.0

# The one targeted re-run a failing unit gets — verification's own count
# (verify.py's RERUNS), the same rules rerun_flaky enforces.
RERUNS = 1

# A log directory's .gitignore: logs are local, never capsule chain content.
LOGS_GITIGNORE = "# pf step command logs are local, never capsule chain content\n*\n"

# The capsule's own log namespace when --out and --log-dir name none.
FAST_CHECKS_DIR = "runtime-state/fast-checks"

# A command's reuse key (polyforge#182): (its root as the record names it,
# its text, its root's subtree tree hash of the working tree, its declared
# prerequisites serialized). The owner's rulings: the hash is of the
# WORKING tree — tracked modifications plus untracked, unignored files,
# a bare commit changes nothing — the repository root's key counts the
# whole tree, nested roots included (Q2: never a missed rerun for a
# root command that may read below a nested root), and the prerequisites
# enter as their sorted declarations, never their values (Q7: a secret
# never lands in a result file).
ReuseKey = Tuple[str, str, str, str]


def prereq_key(c: EngineCommand) -> str:
    """The command's declared prerequisites as its reuse key carries them:
    the sorted declarations, serialized."""
    return json.dumps({"cmd": sorted(c.requires_cmd), "env": sorted(c.requires_env)}, sort_keys=True)


def entry_key(e: Dict[str, Any]) -> Optional[ReuseKey]:
    """The reuse key a previous result entry carries, or None when it
    carries none (a file an older run wrote: no cache, never an error)."""
    tree, prereq = e.get("tree"), e.get("prereq_key")
    if not isinstance(tree, str) or not tree or not isinstance(prereq, str):
        return None
    return (e.get("root", ""), e.get("cmd", ""), tree, prereq)


def reusable_entry(e: Any) -> bool:
    """A previous entry a later run may reuse: green in one run — exit 0,
    neither killed at its timeout nor never started, not green through a
    flaky re-run's confirmation (Q4: only a plain green run is cache) —
    and carrying its key fields and its log."""
    if (not isinstance(e, dict) or e.get("skipped") or e.get("not_started") or e.get("timed_out")
            or isinstance(e.get("exit_code"), bool) or not isinstance(e.get("exit_code"), int)
            or e["exit_code"] != 0 or e.get("confirmed") or e.get("complete")):
        return False
    return entry_key(e) is not None and isinstance(e.get("log"), str) and e["log"] != ""


def ensure_log_dir(d: str) -> None:
    """d created with the .gitignore that keeps it out of the capsule chain."""
    os.makedirs(d, exist_ok=True)
    gi = os.path.join(d, ".gitignore")
    try:
        with open(gi, encoding="utf-8") as f:
            if f.read() == LOGS_GITIGNORE:
                return
    except OSError:
        pass
    with open(gi, "w", encoding="utf-8") as f:
        f.write(LOGS_GITIGNORE)


def next_run_dir(root: str) -> str:
    """<root>/<n> allocated (n = one past the highest existing run)."""
    ensure_log_dir(root)
    n = 0
    for name in os.listdir(root):
        if name.isdigit() and name.isascii() and int(name) > n:
            n = int(name)
    while True:
        n += 1
        d = os.path.join(root, str(n))
        try:
            os.mkdir(d)
            return d
        except FileExistsError:
            continue


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Run a task repo's fast and scoped checks per the scope engine's report, or check a build's recorded unit runs.")
    ap.add_argument("--repo", default="", help="the task repo (repos/<name>) whose fast/scoped checks run")
    ap.add_argument("--close-scoped", action="store_true",
                    help="with --repo: the build close's own mode — the same light-tier selection verification runs, executed over the final tree, its record in details.selected")
    ap.add_argument("--phase", default="", choices=["fast"],
                    help="with --repo: run only that phase's commands (fast is the per-unit bar's own narrowing; default: fast and scoped)")
    ap.add_argument("--root", action="append", default=[], metavar="DIR",
                    help="with --repo: only the named engine root's commands run — . is the repository's own root, a nested root its directory (repeatable; default: every root the change reaches)")
    ap.add_argument("--reuse", action="append", default=[], metavar="FILE",
                    help="with --close-scoped: a fast-checks result file whose green runs of an unchanged reuse key are reused, not re-run (repeatable)")
    ap.add_argument("--unit-runs", default="", metavar="FILE",
                    help="with --close-scoped: the build's units.json, its test commits' files protected — a test this change authored is never pre-existing")
    ap.add_argument("--check-units", default="", metavar="FILE",
                    help="the build's units.json: re-run its recorded unit runs as written and check each unit's marker against its run's output (verification's own rules)")
    ap.add_argument("--unit", default="", help="with --check-units: only the unit of that id")
    ap.add_argument("--capsule", default=".", help="the capsule root (its repos/<name> are the clones)")
    ap.add_argument("--engine", default="", help="the scope engine (default: the shared tree's scripts/verify-scope)")
    ap.add_argument("--deadline", type=duration_flag, default=0,
                    help="the whole run's deadline, a Go duration (default: derived from the step's tool window, two minutes under it)")
    ap.add_argument("--log-dir", default="", help="where the logs go (default: logs/ beside --out, else the capsule's next runtime-state/fast-checks/<n>)")
    ap.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout")
    ap.add_argument("--out", default="", help="write the result file there (atomically)")
    return ap


def timeout_of(c: EngineCommand) -> float:
    """The command's own declared timeout in seconds, or the run's default."""
    try:
        d = duration.parse(gostr.trim_space(c.timeout))
    except ValueError:
        return COMMAND_TIMEOUT
    return d / duration.SECOND if d > 0 else COMMAND_TIMEOUT


def timeout_ns(c: EngineCommand) -> int:
    """The command's declared timeout in nanoseconds — the form a derived
    re-run carries (`go test -timeout`)."""
    try:
        d = duration.parse(gostr.trim_space(c.timeout))
    except ValueError:
        d = 0
    return d if d > 0 else int(COMMAND_TIMEOUT * duration.SECOND)


def nested_command(root: str, c: EngineCommand) -> EngineCommand:
    """A nested root's command as the record names it: <root>/<name>. Its
    cmd stays as its own report gave it — it runs in that report's root."""
    return EngineCommand(name=root + "/" + c.name, phase=c.phase, cmd=c.cmd, requires_env=c.requires_env,
                          requires_cmd=c.requires_cmd, timeout=c.timeout, report=c.report,
                          rerun=c.rerun, source=c.source)


def closest_line(marker: str, lines: Sequence[str]) -> str:
    """The output line closest to the marker (the one a wrong marker most
    likely meant); "" when there is no output."""
    best, ratio = "", -1.0
    for ln in lines:
        r = difflib.SequenceMatcher(None, marker, ln).ratio()
        if r > ratio:
            best, ratio = ln, r
    return best


class UnitCheck:
    """One recorded unit run, re-run as written and judged by the marker
    rules verification itself uses (pfstep.unitruns: the one
    implementation, never a second copy)."""

    def __init__(self, inv: Invocation, capsule: str, log_dir: str, r: Runner) -> None:
        self.inv = inv
        self.capsule = capsule
        self.log_dir = log_dir
        self.r = r
        self.seq = 0
        self.details: Dict[str, Any] = {"units": []}

    def prepare(self, d: UnitRunDecl) -> Tuple[Optional[str], Optional[str]]:
        """Why d cannot run as written ("" = it can): (the directory to run
        in, the cwd as the record names it), or the problem twice."""
        if not d.argv or gostr.trim_space(d.argv[0]) == "":
            return None, "its run has no argv (units.json run.argv is empty): nothing to run as written"
        clone = os.path.join(self.capsule, "repos", d.repo)
        if not os.path.isdir(clone):
            return None, "repos/%s is not a task repo clone of the capsule: it cannot run as written" % d.repo
        cwd, why = clean_cwd(d.cwd)
        if why:
            return None, why
        if not os.path.isdir(os.path.join(clone, cwd)):
            return None, "its cwd %s is no directory of repos/%s" % (gostr.quote(d.cwd), d.repo)
        return os.path.join(clone, cwd), ("repos/" + d.repo) + (("/" + cwd) if cwd != "." else "")

    def check(self, d: UnitRunDecl) -> Dict[str, Any]:
        entry: Dict[str, Any] = {"id": d.id, "check": d.check(), "repo": d.repo, "cwd": d.cwd,
                                "argv": list(d.argv), "marker": d.marker}
        directory, prepared = self.prepare(d)
        why = prepared if directory is None else marker_problem(d.marker)
        if directory is None or why:
            entry["passed"] = False
            entry["why"] = why
            return entry
        self.seq += 1
        log_path = os.path.join(self.log_dir, d.repo, "unit-%d" % self.seq, "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        # the run's limit: the unit's self-reported `timeout`, else the
        # shared default (never the report commands' own bound) — a run
        # killed at it is an evidence problem, named with the limit
        limit = unit_limit_ns(d.timeout)[0]
        er = self.r.exec_to(runner.ExecSpec(argv=list(d.argv), dir=directory, cwd=prepared or "",
                                             timeout=limit / duration.SECOND, private_tmp=True), log_path)
        entry["exit_code"] = er.exit
        if er.timed_out:
            entry["timed_out"] = True
        if er.evidence is not None:
            entry["duration_ms"] = er.evidence.duration_ms
        if er.log_path:
            entry["log"] = self.r.rel(er.log_path)
            self.inv.logs.append(entry["log"])
        if er.timed_out:
            entry["passed"] = False
            entry["why"] = limit_note(limit)
        elif er.exit != 0:
            entry["passed"] = False
            entry["why"] = "exit %d" % er.exit
        elif not marker_matches(d.marker, output_lines(er.log_path)):
            entry["passed"] = False
            entry["why"] = ("exit 0 but no output line matches its marker `%s`: the run did not show its"
                            " test passing" % d.marker)
            entry["closest_line"] = closest_line(d.marker, output_lines(er.log_path))
        else:
            entry["passed"] = True
            entry["why"] = ""
        if entry["passed"]:
            return entry
        # the flaky policy (polyforge#157, the owner's ruling): a unit this
        # lane just wrote is never re-run here — a test just written must
        # be stable, so red it stays; a unit declared pre_existing_red (its
        # test predates the lane) gets verification's own recorded-run
        # policy: one re-run, and a pass records it flaky and passes
        if not d.pre_existing_red:
            entry["why"] += ("; a unit this lane just wrote is not re-run here: a test just written"
                             " must be stable, so red it stays")
            return entry
        rerun_path = os.path.join(os.path.dirname(log_path), "rerun.log")
        er2 = self.r.exec_to(runner.ExecSpec(argv=list(d.argv), dir=directory, cwd=prepared or "",
                                             timeout=limit / duration.SECOND, private_tmp=True), rerun_path)
        if er2.log_path:
            entry["rerun_log"] = self.r.rel(er2.log_path)
            self.inv.logs.append(entry["rerun_log"])
        if not er2.timed_out and er2.exit == 0 and marker_matches(d.marker, output_lines(er2.log_path)):
            entry["passed"] = True
            entry["flaky"] = True
            entry["why"] = ""
        return entry

    def run(self, decls: List[UnitRunDecl]) -> int:
        for d in decls:
            self.inv.say("%s: %s" % (d.check(), " ".join(d.argv)))
            clone = os.path.join(self.capsule, "repos", d.repo)
            if os.path.isdir(clone) and d.repo not in self.inv.heads:
                try:
                    head = git.git(clone, "rev-parse", "--verify", "HEAD^{commit}", timeout=GIT_TIMEOUT)
                    self.inv.heads[d.repo] = head.strip()
                except git.GitError:
                    pass
            self.details["units"].append(self.check(d))
        red = [u for u in self.details["units"] if not u["passed"]]
        flaky = [u for u in self.details["units"] if u.get("flaky")]
        flaky_note = ""
        if flaky:
            flaky_note = ", %d flaky on their one re-run: %s" % (
                len(flaky), "; ".join("%s (first failure log %s)" % (u["id"], u.get("log", "")) for u in flaky))
        if red:
            u = red[0]
            self.inv.summary = "%s: %s%s" % (u["check"], u["why"],
                                             (" (closest output line: %s)" % u["closest_line"]) if u.get("closest_line") else "")
            if len(red) > 1:
                self.inv.summary += "; %d unit(s) red in all" % len(red)
            self.inv.summary += flaky_note
            return 1
        self.inv.summary = "%d unit(s) checked, every marker matched%s" % (len(self.details["units"]), flaky_note)
        return 0


class CheckRun:
    """One repo's fast/scoped checks: its engine reports, and the commands
    they contribute, run in their report's root."""

    def __init__(self, inv: Invocation, capsule: str, repo: str, engine: str,
                 deadline: float, log_dir: str, r: Runner,
                 phase: str = "", roots: Optional[Sequence[str]] = None) -> None:
        self.inv = inv
        self.capsule = capsule
        self.repo = repo
        self.engine = engine
        self.deadline = deadline
        self.log_dir = log_dir
        self.r = r
        # the per-unit narrowing (polyforge#182): --phase fast runs only
        # the fast commands, a --root name only its engine root's
        self.phase = phase
        self.roots: List[str] = list(roots or [])
        self.seq = 0
        # the close's own container (polyforge#222): None on the host —
        # the per-unit run's permanent shape — and the one Box a
        # --close-scoped run of a repo that declares its verification
        # image executes everything through
        self.box: Optional[container.Box] = None
        self.details: Dict[str, Any] = {"repo": repo, "reports": [], "commands": []}
        self.head = ""
        # the baseline triage (polyforge#212): the base setup the engine's
        # reports declare, the protected paths of the close's --unit-runs
        # decls, and the states of every red command's units — the rerun
        # phase fills them, the baseline phase judges, the confirm reads
        self.base_setup: List[str] = []
        self.protected: List[str] = []
        self.red_states: List[Tuple[Dict[str, Any], EngineCommand, str, str, str, List[UnitState]]] = []
        # the baseline record (polyforge#212): the commit the triage judged
        # on, and the whole-batch failure's why ("" = none)
        self.base_commit = ""
        self.base_note = ""
        # the previous result file's reusable green entries, keyed by their
        # reuse key, and this run's per-root subtree hashes of the working
        # tree ("" = the repository root's whole tree)
        self.previous: Dict[ReuseKey, Dict[str, Any]] = {}
        self.trees: Dict[str, str] = {}
        # the repo conf's go_harness= declaration: a harness command's
        # trailer is a completion proof
        self.go_harness = ""

    def load_previous(self) -> Dict[ReuseKey, Dict[str, Any]]:
        """The reusable green entries of the result file at the run's --out
        path — the per-unit runs and the close share one file per repo, so
        one unit's green run is the next's cache. A file that is missing,
        unreadable or not a usable result is no cache: every command runs
        (never a usage error; a red or incomplete past never blocks)."""
        out = self.inv.out
        if not out or not os.path.exists(out):
            return {}
        try:
            with open(out, "rb") as f:
                doc = json.load(f)
        except (OSError, ValueError) as err:
            self.inv.say("the previous result file %s is not a usable cache (%s): nothing is reused" % (out, err))
            return {}
        if not isinstance(doc, dict) or not isinstance(doc.get("details"), dict) \
                or not isinstance(doc["details"].get("commands"), list):
            self.inv.say("the previous result file %s is not a usable result: nothing is reused" % out)
            return {}
        prev: Dict[ReuseKey, Dict[str, Any]] = {}
        for e in doc["details"]["commands"]:
            if reusable_entry(e):
                key = entry_key(e)
                if key is not None:
                    prev[key] = e
        return prev

    def tree_of(self, d: str) -> str:
        """The working tree's hash of the root d ("" = the repository root,
        whose key counts the whole tree), memoized; "" when git cannot
        answer — nothing is reused and nothing is judged, every command
        runs."""
        if d in self.trees:
            return self.trees[d]
        clone = os.path.join(self.capsule, "repos", self.repo)
        try:
            tree = git.worktree_tree(clone, d, timeout=GIT_TIMEOUT)
        except git.GitError as err:
            self.inv.say("repos/%s: the working tree's hash of %s is unreadable (%s): nothing is reused"
                         % (self.repo, d or "the repository root", err))
            tree = ""
        self.trees[d] = tree
        return tree

    def cwd_rel(self, directory: str) -> str:
        """The directory as the record names it: capsule-relative inside the
        capsule ("repos/<repo>" at its root, plus the nested root)."""
        return self.r.rel(directory)

    def ask_engine(self, directory: str, argv: Optional[List[str]] = None) -> Tuple[Optional[EngineReport], str, str]:
        """The engine's report with directory as its root — the default
        report, no tier flag, exactly the report the model ran by hand,
        unless a caller hands its own argv (the close mode's light
        request): (the report, the invocation, why there is none). The
        engine runs where the close runs — in the box, its mounted
        path."""
        engine = self.box.engine if self.box is not None else self.engine
        if argv is None:
            argv = engine_argv_of(engine) + ["--json"]
        ref = " ".join(argv)
        self.seq += 1
        log_path = os.path.join(self.log_dir, self.repo, "engine-%d" % self.seq, "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        er = self.r.exec_to(runner.ExecSpec(argv=argv, dir=directory, cwd=self.cwd_rel(directory),
                                             timeout=GIT_TIMEOUT, capture=True), log_path, self.box)
        if er.error:
            return None, ref, "the engine %s could not start: %s" % (self.engine, er.error)
        if er.exit != 0:
            tail = ""
            try:
                with open(er.log_path, "rb") as f:
                    tail = f.read().decode("utf-8", "replace").rstrip().split("\n")[-1][:200]
            except OSError:
                pass
            return None, ref, "the engine exited %d: %s" % (er.exit, tail)
        try:
            return decode_report(er.stdout), ref, ""
        except ReportError as err:
            return None, ref, "the engine's report is unreadable: %s" % err

    def run_command(self, c: EngineCommand, directory: str, root_rel: str) -> Tuple[Dict[str, Any], str, str]:
        """One planned command in its report's root, skipped when its
        declared prerequisites are unmet: (its entry, its log's absolute
        path, its private TMPDIR)."""
        entry: Dict[str, Any] = {"name": c.name, "phase": c.phase, "root": root_rel, "cmd": c.cmd}
        unmet = self.r.unmet(c.requires_env, c.requires_cmd, directory, self.box)
        if unmet:
            entry["skipped"] = True
            entry["skip_reason"] = unmet
            return entry, "", ""
        self.seq += 1
        log_path = os.path.join(self.log_dir, self.repo, str(self.seq), "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        er = self.r.exec_to(runner.ExecSpec(argv=bash_argv(c.cmd), dir=directory, cwd=root_rel,
                                             timeout=timeout_of(c), private_tmp=True), log_path, self.box)
        entry["exit_code"] = er.exit
        if er.timed_out:
            entry["timed_out"] = True
        if er.not_started:
            entry["not_started"] = True
        entry["duration_ms"] = er.evidence.duration_ms if er.evidence is not None else 0
        if er.log_path:
            entry["log"] = self.r.rel(er.log_path)
            self.inv.logs.append(entry["log"])
        return entry, er.log_path, er.tmp_dir

    # -- the flaky re-run phase (the one targeted re-run a failing unit
    #    gets, verification's own rules — then the whole-command confirm)

    def unit_spec(self, c: EngineCommand, d: str) -> CommandSpec:
        """The CommandSpec a failing unit of c derives its re-run from —
        verify's own shape: a nested root's command carries its cd, and
        its declared re-run template behind the same cd."""
        cd = "cd " + shell_quote(d) + " && " if d else ""
        return CommandSpec(cmd=cd + c.cmd, timeout=timeout_ns(c),
                           rerun_tmpl=(cd + c.rerun) if c.rerun else "",
                           dir=d, inner=c.cmd if d else "")

    def rerun_runner(self, timeout: float) -> "Callable[[str], RerunRun]":
        """One derived re-run's runner: bash in the clone's root, the
        command's own timeout, its log in the run's log dir."""
        clone = os.path.join(self.capsule, "repos", self.repo)

        def run(cmd: str) -> RerunRun:
            self.seq += 1
            log_path = os.path.join(self.log_dir, self.repo, str(self.seq), "command.log")
            os.makedirs(os.path.dirname(log_path), exist_ok=True)
            er = self.r.exec_to(runner.ExecSpec(argv=bash_argv(cmd), dir=clone, cwd="repos/" + self.repo,
                                                 timeout=timeout, private_tmp=True), log_path)
            return RerunRun(not_started=er.not_started, exit=er.exit, log_path=er.log_path,
                            tmp_dir=er.tmp_dir,
                            duration=(er.evidence.duration_ms / 1000.0) if er.evidence is not None else 0.0)

        return run

    def unit_entry(self, us: UnitState) -> Dict[str, Any]:
        """One unit of the result's details — verify's own statuses, a
        flaky carrying its re-run's log beside the first failure's; a
        still-failing unit carries its baseline class, its signatures and
        the base runs' observation summary beside the judgment's why."""
        out: Dict[str, Any] = {"kind": us.kind, "name": us.name, "file": us.file,
                               "status": "flaky" if us.flaky else ("pre_existing" if us.pre_existing else ("fail" if us.failed else "pass"))}
        if us.flaky:
            out["flaky"] = us.flaky
            out["detail"] = "passed on a re-run at HEAD"
            if us.pass_logs:
                out["rerun_log"] = self.r.rel(us.pass_logs[0])
                self.inv.logs.append(out["rerun_log"])
        elif us.pre_existing:
            out["detail"] = us.pre_detail
        elif us.failed:
            out["detail"] = us.note
        if us.failed and us.base_class:
            out["class"] = us.base_class
            out["signature"] = us.unit.signature
            out["head_signature"] = us.head_sig
            if us.base_detail:
                out["base_detail"] = us.base_detail
        if us.not_rerun:
            out["not_rerun"] = True
        return out

    def rerun_units(self, entry: Dict[str, Any], c: EngineCommand, d: str,
                    log_abs: str, tmp_dir: str) -> List[UnitState]:
        """A red command's failing units, each re-run once by its derived
        targeted re-run (verification's own locate + rerun_flaky, the one
        implementation); a pass records the unit flaky. The command's
        entry carries every unit, a still-failing one its note."""
        clone = os.path.join(self.capsule, "repos", self.repo)
        lines = normalize_output(output_lines(log_abs), clone, tmp_dir)
        words, _ = shell_words(c.cmd)
        parsed = parse_units(lines, failfast=has_go_test(words) and has_flag(words, "-failfast"))
        race = race_seen(lines)
        spec = self.unit_spec(c, d)
        us_list: List[UnitState] = []
        for u in parsed:
            us = UnitState(unit=u)
            locate(clone, spec, us, race)
            if us.file and path_is_protected(us.file, self.protected):
                us.protected = True
            us_list.append(us)
        if not any(us.failed for us in us_list):
            # the exit is non-zero but no unit of the output failed: the
            # whole command is the unit — never re-runnable
            w = whole_command(c.name, entry["exit_code"])
            locate(clone, spec, w, race)
            us_list.append(w)
        for us in [x for x in us_list if x.failed]:
            rerun_flaky(us, RERUNS, clone, self.rerun_runner(timeout_of(c)),
                        lambda: self.r.remaining() > 1.0, "the fast checks' deadline",
                        "the fast checks' deadline")
        return us_list

    def confirm(self, entry: Dict[str, Any], c: EngineCommand, directory: str, root_rel: str,
                log_abs: str, us_list: List[UnitState]) -> None:
        """Verification's whole-command confirmation, the one policy: a red
        command every still-failing unit of which the flaky policy or the
        baseline phase explained (flaky or pre-existing) is proven whole —
        in its skip form when a pre-existing failure was excluded, by its
        first-run output's completion proof, or by one whole re-run exiting
        0; a command that cannot exclude its pre-existing failures and has
        no completion proof keeps a residual whole-command failure, named
        (verify's own confirm_phase, isomorphic). A command the deadline
        cannot fit is not confirmed: it stays red."""
        if entry.get("timed_out") or entry.get("not_started"):
            return
        if any(us.failing() and not us.pre_existing for us in us_list):
            return  # a failure no re-run or the baseline explained
        pre = any(us.pre_existing for us in us_list)
        cmd = c.cmd
        if pre:
            form, note = skip_form(c.cmd, self.repo, us_list, harness_command(self.go_harness, c.cmd), "the fast checks")
            if note:
                proof = completion_proof(harness_command(self.go_harness, c.cmd), False, log_abs, us_list)
                if proof:
                    entry["complete"] = proof
                    return
                residual = whole_command(c.name, entry["exit_code"])
                residual.note = ("the command has no skip switch for its pre-existing failure(s), so the rest of it"
                                 " cannot be shown to pass: it stays failed — split the bar so each command can"
                                 " exclude its own failures")
                residual.base_class = CLASS_UNDETERMINED
                us_list.append(residual)
                return
            cmd = form
        proof = completion_proof(harness_command(self.go_harness, c.cmd), False, log_abs, us_list)
        if proof:
            entry["complete"] = proof
            return
        if self.r.remaining() < 1.0:
            entry["not_confirmed"] = ("not confirmed: the fast checks' deadline was spent before `%s`"
                                      " could be re-run whole to show nothing else failed" % cmd)
            return
        dur = entry.get("duration_ms", 0) / 1000.0
        if self.r.remaining() < dur:
            entry["not_confirmed"] = ("not confirmed: the fast checks' deadline leaves less than the command's"
                                       " first-run time (%s) to re-run `%s` whole"
                                       % (duration.format(int(dur * duration.SECOND)), cmd))
            return
        self.seq += 1
        log_path = os.path.join(self.log_dir, self.repo, str(self.seq), "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        er = self.r.exec_to(runner.ExecSpec(argv=bash_argv(cmd), dir=directory, cwd=root_rel,
                                             timeout=timeout_of(c), private_tmp=True), log_path)
        entry["confirm_exit_code"] = er.exit
        if er.timed_out:
            entry["confirm_timed_out"] = True
        if er.log_path:
            entry["confirm_log"] = self.r.rel(er.log_path)
            self.inv.logs.append(entry["confirm_log"])
        if er.exit == 0:
            entry["confirmed"] = cmd
        elif er.not_started:
            entry["not_confirmed"] = "not confirmed: the fast checks' deadline had passed before the re-run could start"
        else:
            entry["not_confirmed"] = ("after its located failures were explained (flaky or pre-existing), `%s` still"
                                      " exits %d: a failure no unit locates (log %s)"
                                      % (cmd, er.exit, entry.get("confirm_log", "")))

    def rerun_phase(self, runs: List[Tuple[Dict[str, Any], EngineCommand, str, str, str, str, str]]) -> None:
        """The flaky re-run phase's first half: every red command's failing
        units re-run once by its derived targeted re-run; the states stay
        for the baseline phase and the confirm."""
        for entry, c, directory, root_rel, d, log_abs, tmp_dir in runs:
            # a command its own timeout killed is red on its own — its
            # confirm never runs either — and re-running units out of its
            # partial output would only cost time and name flaky units
            # beside a command that stays red (r1#N5)
            if (entry.get("skipped") or entry.get("not_started") or entry.get("timed_out")
                    or entry.get("exit_code") == 0 or not log_abs):
                continue
            us_list = self.rerun_units(entry, c, d, log_abs, tmp_dir)
            self.red_states.append((entry, c, directory, root_rel, log_abs, us_list))

    def baseline_ref(self) -> Tuple[str, str]:
        """The change's baseline commit and why it cannot be read ("" = it
        can): the same one verification's head phase takes — the capsule's
        fixed target branch, else the origin's default, and
        merge-base HEAD origin/<branch> — so a triaged red and
        verification's own judgment cannot disagree on the base."""
        clone = os.path.join(self.capsule, "repos", self.repo)
        branch, why_not = scoped_plan.base_branch_of(self.capsule, clone)
        if branch == "":
            return "", why_not
        try:
            return git.git(clone, "merge-base", "HEAD", "origin/" + branch, timeout=GIT_TIMEOUT).strip(), ""
        except git.GitError as err:
            return "", "the diff base is unreadable: %s" % err

    def worktree_add_of(self, clone: str) -> "Callable[[str, str], int]":
        def add(tmp: str, sha: str) -> int:
            return git.run(clone, "worktree", "add", "--detach", tmp, sha, timeout=GIT_TIMEOUT).exit_code
        return add

    def run_bash_of(self) -> "Callable[[str, str, float], RerunRun]":
        def run(cmd: str, d: str, t: float) -> RerunRun:
            self.seq += 1
            log_path = os.path.join(self.log_dir, self.repo, "base-%d" % self.seq, "command.log")
            os.makedirs(os.path.dirname(log_path), exist_ok=True)
            er = self.r.exec_to(runner.ExecSpec(argv=bash_argv(cmd), dir=d, cwd=d, timeout=t,
                                                 private_tmp=True), log_path)
            if er.log_path:
                self.inv.logs.append(self.r.rel(er.log_path))
            return RerunRun(not_started=er.not_started, exit=er.exit, log_path=er.log_path, tmp_dir=er.tmp_dir,
                            duration=(er.evidence.duration_ms / 1000.0) if er.evidence is not None else 0.0)
        return run

    def baseline_phase(self) -> None:
        """The baseline triage (polyforge#212): every unit still red after
        its one re-run is judged on the change's baseline through the one
        shared entry (pfstep.preexisting.judge_on_base) — the same judgment
        verification runs, this run's own executors injected: the base
        worktree on the host, the base setup and the re-runs over it, the
        run's deadline as the budget. The result lands on each unit (its
        class, the observation summary, the pre-existing record), and a red
        the base could not settle keeps its note — fail closed, the existing
        red rules hold. The record names the baseline itself: its commit,
        the setup it would run, whether it ran (no red to judge, no base,
        a spent budget or a failed setup: it did not) and the whole-batch
        failure's why."""
        self.base_commit, why = self.baseline_ref()
        failing = [us for (_e, _c, _d, _r, _l, us_list) in self.red_states for us in us_list if us.failing()]
        cands = candidates(failing)
        ran = False
        if cands and self.base_commit == "":
            for u in cands:
                u.note = join_note(u.note, "not pre-existing: no base commit is known")
                u.base_class = CLASS_UNDETERMINED
            self.base_note = why
        elif cands:
            notes: List[str] = []
            clone = os.path.join(self.capsule, "repos", self.repo)
            judge_on_base(clone, self.base_commit, short(self.base_commit), cands, self.base_setup,
                          self.worktree_add_of(clone), self.run_bash_of(),
                          lambda: self.r.remaining() > 1.0, lambda d: self.r.remaining() > d,
                          "the fast checks' deadline", COMMAND_TIMEOUT, None, notes.append)
            ran = not notes
            self.base_note = "; ".join(notes)
        self.details["baseline"] = {"commit": self.base_commit, "ref": "merge-base",
                                    "setup": list(self.base_setup), "ran": ran}
        if self.base_note:
            self.details["baseline"]["note"] = self.base_note

    def command_red(self, e: Dict[str, Any]) -> bool:
        """Whether one command entry stayed red after its flaky re-run, its
        baseline triage and its confirmation."""
        return (not e.get("skipped") and not e.get("reused") and not e.get("carried")
                and (e.get("timed_out") or e.get("exit_code") != 0)
                and not (e.get("confirmed") or e.get("complete")))

    def build_reds(self) -> List[Dict[str, Any]]:
        """Every red that stayed: one entry per still-failing unit — its
        class, its signatures, the baseline it was judged on and the why
        (a pre-existing one's base record, anything else its note) — and
        one undetermined entry per red command no unit of its own explains
        (a command its timeout killed, a never-started run, a whole-command
        confirmation that could not run): the command is the unit."""
        reds: List[Dict[str, Any]] = []
        for entry, _c, _d, _r, _l, us_list in self.red_states:
            for us in us_list:
                if not us.failing():
                    continue
                reds.append({"command": entry["name"], "unit": us.name, "kind": us.kind, "file": us.file,
                             "class": us.base_class or CLASS_UNDETERMINED, "signature": us.unit.signature,
                             "base": self.base_commit,
                             "detail": us.pre_detail if us.pre_existing else us.note})
        for e in self.details["commands"]:
            if not self.command_red(e):
                continue
            if any(us.failing() for st in self.red_states if st[0] is e for us in st[5]):
                continue  # its own units name it
            what = ("killed at its timeout" if e.get("timed_out")
                    else ("not started" if e.get("not_started")
                          else (e.get("not_confirmed") or ("exited %d" % e["exit_code"]))))
            reds.append({"command": e["name"], "unit": e["name"], "kind": KIND_COMMAND, "file": "",
                         "class": CLASS_UNDETERMINED, "signature": "", "base": self.base_commit,
                         "detail": what})
        return reds

    def classes_note(self, reds: List[Dict[str, Any]]) -> str:
        """The red classes as a summary's tail: one count per class, the
        baseline they were judged on."""
        if not reds:
            return ""
        return "; classes: %d this change, %d undetermined, %d pre-existing (base %s)" % (
            len([r for r in reds if r["class"] == CLASS_THIS_CHANGE]),
            len([r for r in reds if r["class"] == CLASS_UNDETERMINED]),
            len([r for r in reds if r["class"] == CLASS_PRE_EXISTING]),
            short(self.base_commit))

    def pre_existing_note(self, reds: List[Dict[str, Any]]) -> str:
        """The pre-existing reds as a green summary's tail: each unit with
        its signature, the baseline they were proven on."""
        pre = [r for r in reds if r["class"] == CLASS_PRE_EXISTING]
        if not pre:
            return ""
        return ", %d pre-existing on base %s: %s" % (len(pre), short(self.base_commit),
                "; ".join("%s (%s)" % (r["unit"], r["signature"]) for r in pre))

    def confirm_phase(self) -> None:
        """The flaky re-run phase's second half: the whole-command
        confirmation over the states the rerun phase left (a pre-existing
        failure excluded by its skip form), the units recorded with their
        baseline classes."""
        for entry, c, directory, root_rel, log_abs, us_list in self.red_states:
            self.confirm(entry, c, directory, root_rel, log_abs, us_list)
            entry["units"] = [self.unit_entry(us) for us in us_list]

    def run(self) -> int:
        """The repo's whole check: the report's fast and scoped commands, in
        order, none stopped for a failure. The run's exit code."""
        clone = os.path.join(self.capsule, "repos", self.repo)
        if not os.path.isdir(clone):
            return self.inv.usage("repos/%s is not a task repo clone of the capsule" % self.repo)
        if not os.path.exists(self.engine):
            return self.inv.usage("the scope engine %s does not exist" % self.engine)
        try:
            head = git.git(clone, "rev-parse", "--verify", "HEAD^{commit}", timeout=GIT_TIMEOUT)
        except git.GitError as err:
            return self.inv.usage("repos/%s: HEAD unreadable: %s" % (self.repo, err))
        self.head = head.strip()
        self.inv.heads[self.repo] = self.head
        self.go_harness = read_go_harness(clone)

        # every planned command: (the command, the directory it runs in —
        # its report's root —, that root as the record names it, the
        # nested root it belongs to ("" = the repository root))
        commands: List[Tuple[EngineCommand, str, str, str]] = []
        # the roots a --root name narrows the run to (the per-unit bar,
        # polyforge#182): "." or "" is the repository's own root, a nested
        # root its directory — a name that is neither is a usage error
        # naming the roots there are; without --root every root the change
        # reaches runs (the default, the close's own shape)
        nested = nested_roots(clone)
        named: List[str] = []
        for r in self.roots:
            d = "" if r == "." else r.strip("/")
            if d != "" and d not in nested:
                return self.inv.usage("--root %s names no engine root of repos/%s: the roots are . (the repository's own) and %s"
                                      % (gostr.quote(r), self.repo, ", ".join(nested) if nested else "none"))
            if d not in named:
                named.append(d)
        # --phase fast narrows the run to the fast commands (the scoped
        # selection is the close's own, never the per-unit's)
        phases: Tuple[str, ...] = ("fast",) if self.phase == "fast" else ("fast", "scoped")
        root_rep: Optional[EngineReport] = None
        if not named or "" in named:
            rep, ref, why = self.ask_engine(clone)
            self.details["engine"] = ref
            if rep is None:
                self.inv.summary = "repos/%s: %s" % (self.repo, why)
                return 2
            root_rep = rep
            # AC2/AC5: the report is the default one — the report the model
            # ran by hand — and only its fast and scoped phases ever run: a
            # change on a forced-full path behaves exactly as before (its
            # fast gates and nothing more)
            chosen = [c for c in rep.commands if c.phase in phases]
            self.details["reports"].append({"root": self.cwd_rel(clone), "mode": rep.mode,
                                            "commands": len(chosen)})
            # the base setup the report declares: the baseline phase runs it
            # in the base worktree before any re-run there (Q2: the same
            # preparation verification's base runs get)
            self.base_setup = scoped_plan.append_new(self.base_setup, rep.base_setup)
            commands += [(c, clone, self.cwd_rel(clone), "") for c in chosen]
        # every nested engine root the changed paths reach is a repository
        # of its own whatever the root report answered — the #24 shape has
        # the root conf force the full tier for the nested root's paths, and
        # the nested root's own report still runs there, in its root, not
        # behind a cd the model writes — a --root that names one asks it
        # whether or not the root report's paths reach it
        reached = ([d for d in nested if d in named] if named
                    else reached_roots(nested, (root_rep.paths if root_rep is not None else []) or []))
        for d in reached:
            directory = os.path.join(clone, d)
            root_rel = self.cwd_rel(directory)
            nrep, nref, nwhy = self.ask_engine(directory)
            if "engine" not in self.details:
                self.details["engine"] = nref
            if nrep is None:
                self.inv.summary = "repos/%s: the nested engine root %s: %s" % (self.repo, d, nwhy)
                return 2
            nchosen = [c for c in nrep.commands if c.phase in phases]
            self.details["reports"].append({"root": root_rel, "mode": nrep.mode,
                                            "commands": len(nchosen)})
            self.base_setup = scoped_plan.append_new(
                self.base_setup, ["cd " + shell_quote(d) + " && " + s for s in nrep.base_setup])
            commands += [(nested_command(d, c), directory, root_rel, d) for c in nchosen]
        if not commands:
            self.inv.summary = ("repos/%s: the engine planned no command (stack %s, conf %s)"
                                % (self.repo,
                                   gostr.quote(root_rep.stack if root_rep is not None else ""),
                                   (root_rep.conf if root_rep is not None else "the named root")))
            return 2
        # every root's subtree hash, before any command runs: one snapshot
        # of the working tree keys the whole run's reuse
        for d in sorted({d for _c, _dir2, _rel, d in commands}):
            self.tree_of(d)
        self.previous = self.load_previous()
        consumed: Set[ReuseKey] = set()
        # the (root, cmd) pairs this run plans: a previous green entry of a
        # pair this run re-ran is superseded (dropped), one of a pair it
        # never planned — another root, a removed gate — is carried
        planned: Set[Tuple[str, str]] = set()
        # (the entry, its command, its directory, its root, its nested
        # root, its log's absolute path, its private TMPDIR)
        runs: List[Tuple[Dict[str, Any], EngineCommand, str, str, str, str, str]] = []
        for c, directory, root_rel, d in commands:
            self.inv.say("repos/%s: `%s` (%s)" % (self.repo, c.cmd, c.phase))
            planned.add((root_rel, c.cmd))
            subtree = self.trees.get(d, "")
            key: Optional[ReuseKey] = (root_rel, c.cmd, subtree, prereq_key(c)) if subtree else None
            prev = self.previous.get(key) if key is not None else None
            if key is not None and prev is not None \
                    and not self.r.unmet(c.requires_env, c.requires_cmd, directory, None):
                # a green run of the same key: reused, not executed — the
                # original log path and exit code 0 are its evidence
                entry: Dict[str, Any] = {"name": c.name, "phase": c.phase, "root": root_rel,
                                         "cmd": c.cmd, "exit_code": 0, "log": prev.get("log", ""),
                                         "tree": subtree, "prereq_key": key[3], "reused": True}
                self.details["commands"].append(entry)
                consumed.add(key)
                continue
            entry, log_abs, tmp_dir = self.run_command(c, directory, root_rel)
            if subtree and not entry.get("skipped"):
                entry["tree"] = subtree
                entry["prereq_key"] = prereq_key(c)
            self.details["commands"].append(entry)
            if not entry.get("skipped"):
                runs.append((entry, c, directory, root_rel, d, log_abs, tmp_dir))
        # a green entry of a (root, cmd) this run did not plan stays in the
        # file (carried): the close — or the next unit's run — can still
        # reuse it
        for key, prev in self.previous.items():
            if key in consumed or (prev.get("root", ""), prev.get("cmd", "")) in planned:
                continue
            carried = dict(prev)
            carried.pop("reused", None)
            carried["carried"] = True
            self.details["commands"].append(carried)
        # the flaky phase: every red command's located failing units re-run
        # once, the still-red judged on the baseline, then the whole command
        # confirmed — a check green through a flaky re-run names it in its
        # summary
        self.rerun_phase(runs)
        self.baseline_phase()
        self.confirm_phase()
        self.details["reds"] = self.build_reds()
        red = [e for e in self.details["commands"] if self.command_red(e)]
        skipped = len([e for e in self.details["commands"] if e.get("skipped")])
        reused_n = len([e for e in self.details["commands"] if e.get("reused")])
        carried_n = len([e for e in self.details["commands"] if e.get("carried")])
        ran = len(self.details["commands"]) - skipped - reused_n - carried_n
        flaky_notes: List[str] = []
        for e in self.details["commands"]:
            for u in e.get("units", []):
                if u["status"] == "flaky":
                    flaky_notes.append("%s (%s, first failure log %s)" % (u["name"], u["flaky"], e.get("log", "")))
        flaky_note = ""
        if flaky_notes:
            flaky_note = ", %d unit(s) flaky on their one re-run: %s" % (len(flaky_notes), "; ".join(flaky_notes))
        if red:
            e = red[0]
            what = "was killed at its timeout" if e.get("timed_out") else "exited %d" % e["exit_code"]
            self.inv.summary = "repos/%s: `%s` (%s) %s" % (self.repo, e["cmd"], e["phase"], what)
            if e.get("log"):
                self.inv.summary += " (log %s)" % e["log"]
            if len(red) > 1:
                self.inv.summary += "; %d command(s) red in all" % len(red)
            if flaky_note:
                self.inv.summary += flaky_note
            self.inv.summary += self.classes_note(self.details["reds"])
            return 1
        self.inv.summary = "repos/%s: %d command(s) ran and exited 0%s%s%s%s%s" % (
            self.repo, ran,
            (", %d skipped" % skipped) if skipped else "",
            (", %d reused" % reused_n) if reused_n else "",
            (", %d carried over" % carried_n) if carried_n else "",
            flaky_note,
            self.pre_existing_note(self.details["reds"]))
        return 0


class CloseRun(CheckRun):
    """The build close's scoped selection (--close-scoped, polyforge#164):
    the same light-tier plan verification runs — one shared implementation
    (pfstep/scoped_plan.plan_light, fed this run's own engine asks) — its
    commands executed on the host over the final tree, a command whose
    reuse key a green run in a --reuse result already proved reused instead
    of re-run (polyforge#182: the same key the --repo run reuses by — one
    key, two modes). A selection the engine answers with the full tier is
    never run: the run records incomplete with the engine's own fallback
    reasons and exits non-zero, claiming nothing. The result's
    details.selected is the change analysis's machine selection record."""

    def __init__(self, inv: Invocation, capsule: str, repo: str, engine: str,
                 reuse: List[str], log_dir: str, r: Runner,
                 runtime: Optional[container.ContainerRuntime] = None) -> None:
        super().__init__(inv, capsule, repo, engine, r.deadline, log_dir, r)
        self.reuse: List[str] = list(reuse)
        self.reuse_green: Dict[ReuseKey, str] = {}
        # the container engine the close starts its box through (a test's
        # injection replaces the docker CLI, the way verify.py's does)
        self.runtime = runtime
        self.details: Dict[str, Any] = {"repo": repo, "mode": "close-scoped", "commands": []}

    def dockerfile_declared(self, clone: str) -> Tuple[bool, str]:
        """Whether the repo's HEAD carries DOCKERFILE_VERIFY — the one
        declaration that selects the container (a working-tree-only copy
        never counts) — and why the declaration cannot be read ("" = it
        can): unreadable is the run's error, never a silent host
        fall-back."""
        c = git.run(clone, "ls-tree", "--name-only", "HEAD", "--", container.DOCKERFILE_VERIFY)
        if c.exit_code != 0:
            return False, ("%s unreadable at HEAD: %s"
                           % (container.DOCKERFILE_VERIFY,
                              c.stderr.decode("utf-8", "replace").strip() or "git exited %d" % c.exit_code))
        return bool(c.stdout.decode("utf-8", "replace").strip()), ""

    def base_root(self) -> str:
        """The directory the baseline's throwaway worktree lives in — inside
        the run's logs, mounted into the container so its re-runs see the
        base tree (the same base_root shape verification's boxes mount)."""
        return os.path.join(self.log_dir, "base")

    def start_the_box(self, clone: str) -> int:
        """Start the repo's verification container for the close (0 = up, 2
        = the run is not judged, the reason said). The same machinery
        verification starts its boxes with (pfstep/container.py, its
        public entry): the image of the HEAD-committed Dockerfile's bytes,
        the clone mounted at its own path, the tool cache volume, the
        engine mounted read-only beside it, the baseline's base worktree
        directory under the run's logs."""
        co = container.for_run(container.ContainerOptions(runtime=self.runtime, say=self.inv.say),
                               self.r.deadline, self.inv.env)
        try:
            co.engine().available()
        except container.ContainerError:
            pass  # nothing to sweep; the box below says why it cannot start
        else:
            container.sweep_run(co, self.inv.say)
        try:
            run_no = int(os.path.basename(self.log_dir))
        except ValueError:
            run_no = 0
        self.box = container.start_box(co, self.repo, clone, self.log_dir, self.r.deadline, run_no,
                                       self.r.rel, engine=os.path.realpath(self.engine),
                                       base_root=self.base_root())
        return 0

    def baseline_ref(self) -> Tuple[str, str]:
        """The close's own baseline: the merge-base its light plan was
        computed against, already read — the same base verification plans
        its light tier over."""
        return self.details.get("base", ""), ""

    def command_root(self, c: EngineCommand) -> str:
        """The root the record names for c: the repository's own, plus the
        nested root a command belongs to."""
        rel = self.cwd_rel(os.path.join(self.capsule, "repos", self.repo))
        return rel + (("/" + c.dir) if c.dir else "")

    def reuse_key(self, c: EngineCommand) -> Optional[ReuseKey]:
        """What a green run in a --reuse file is matched by: the same key a
        --repo run reuses by — the command's own root, the command as its
        own report gave it (a nested root's entry runs there, without the
        cd the light plan wraps it with), its root's subtree tree hash of
        the working tree and its declared prerequisites. None when the
        hash is unreadable: nothing is reused, everything runs."""
        subtree = self.tree_of(c.dir)
        if not subtree:
            return None
        return self.command_root(c), (c.inner if c.dir else c.cmd), subtree, prereq_key(c)

    def load_reuse(self) -> int:
        """The reusable green entries of the --reuse files, keyed by the
        same key the --repo run reuses by: a green-in-one-run entry (never
        a flaky-confirmed one, Q4) carrying its key fields and its log — a
        plain green run, a reused entry or a carried one alike; an entry
        an older run wrote without its key fields is no cache, and an
        unusable file is the invocation's (exit 2)."""
        for path in self.reuse:
            try:
                with open(path, "rb") as f:
                    doc = json.load(f)
            except (OSError, ValueError) as err:
                return self.inv.usage("--reuse %s is not a usable result file: %s" % (path, err))
            if not isinstance(doc, dict) or not isinstance(doc.get("details"), dict):
                return self.inv.usage("--reuse %s is not a usable result file: no details" % path)
            for e in doc["details"].get("commands") or []:
                if not isinstance(e, dict) or not reusable_entry(e):
                    continue
                key = entry_key(e)
                if key is not None:
                    self.reuse_green[key] = "%s#%s" % (path, e.get("name", ""))
        return 0

    def ask(self, directory: str, cwd_rel: str, base: str) -> Tuple[Optional[EngineReport], str, str]:
        """plan_light's ask: the engine's light report of directory, the
        invocation the one shared implementation derives (the same argv
        verification's light tier asks with) — asked of the engine where
        the close runs, the box's mounted path."""
        return self.ask_engine(directory, scoped_plan.engine_argv(
            self.box.engine if self.box is not None else self.engine, base))

    def unit_spec(self, c: EngineCommand, d: str) -> CommandSpec:
        """The CommandSpec a failing unit of c derives its re-run from — a
        light-plan command carries its own cd (verify's own CmdRun.spec
        shape), so nothing is wrapped here."""
        return CommandSpec(cmd=c.cmd, timeout=timeout_ns(c), rerun_tmpl=c.rerun, dir=c.dir, inner=c.inner)

    def selected_of(self, head: str, tree: str, base: str, branch: str, plan: scoped_plan.LightPlan,
                    status: str, reason: str) -> Dict[str, Any]:
        """The change analysis's machine selection record: the identity the
        selection was computed at and every command's evidence."""
        rep = plan.report
        sel: Dict[str, Any] = {
            "head": head, "tree": tree, "base": base, "default_branch": branch,
            "engine": plan.engine_ref,
            "conf": rep.conf if rep is not None else "",
            "conf_changed_in_diff": rep.conf_changed_in_diff if rep is not None else False,
            "mode": plan.mode,
            "paths": list(rep.paths or []) if rep is not None else [],
            "suites": list(((rep.e2e_selection or {}).get("suites") or [])) if rep is not None else [],
            "test_glob": list(rep.test_glob) if rep is not None else [],
            "commands": self.details["commands"],
            "status": status,
        }
        if reason:
            sel["reason"] = reason
        return sel

    def run(self) -> int:
        """The close's whole run: the shared light plan of the repo's final
        tree, its commands run (or reused), the selection recorded. The
        run's exit code."""
        clone = os.path.join(self.capsule, "repos", self.repo)
        if not os.path.isdir(clone):
            return self.inv.usage("repos/%s is not a task repo clone of the capsule" % self.repo)
        if not os.path.exists(self.engine):
            return self.inv.usage("the scope engine %s does not exist" % self.engine)
        try:
            head = git.git(clone, "rev-parse", "--verify", "HEAD^{commit}", timeout=GIT_TIMEOUT).strip()
            tree = git.git(clone, "rev-parse", "HEAD^{tree}", timeout=GIT_TIMEOUT).strip()
        except git.GitError as err:
            return self.inv.usage("repos/%s: HEAD unreadable: %s" % (self.repo, err))
        self.head = head
        self.inv.heads[self.repo] = head
        self.go_harness = read_go_harness(clone)
        code = self.load_reuse()
        if code:
            return code
        branch, why_not = scoped_plan.base_branch_of(self.capsule, clone)
        if branch == "":
            self.inv.summary = "repos/%s: not judged: %s" % (self.repo, why_not)
            return 2
        try:
            base = git.git(clone, "merge-base", "HEAD", "origin/" + branch, timeout=GIT_TIMEOUT).strip()
        except git.GitError as err:
            self.inv.summary = "repos/%s: not judged: the diff base is unreadable: %s" % (self.repo, err)
            return 2
        self.details["base"], self.details["default_branch"] = base, branch
        # the close's isolation (polyforge#222): the repo's own declared
        # verification image carries the whole close — the engine asks and
        # the selected commands, the same image and cache verification
        # runs — while a repo without the declaration keeps the host close
        # exactly as before; the declaration is the HEAD-committed
        # Dockerfile alone, read strictly (unreadable is the run's error,
        # never a silent host fall-back)
        declared, why_not = self.dockerfile_declared(clone)
        if why_not:
            self.inv.summary = "repos/%s: not judged: %s" % (self.repo, why_not)
            return 2
        if declared:
            self.details["isolation"] = "container"
            code = self.start_the_box(clone)
            if code:
                return code
        else:
            self.details["isolation"] = "host"
        try:
            return self.close_run(clone, head, tree, base, branch)
        finally:
            if self.box is not None:
                self.box.remove()
                # the container's own facts, with whatever the run left on
                # it (lost, a failed removal) — recorded after the removal
                # so the record is the end state
                self.details["container"] = self.box.report.as_dict()

    def close_run(self, clone: str, head: str, tree: str, base: str, branch: str) -> int:
        """The close's whole run once the isolation stands: the shared light
        plan of the repo's final tree, its commands run (or reused), the
        selection recorded. The run's exit code."""
        plan = scoped_plan.plan_light(clone, self.cwd_rel(clone), base, self.ask)
        self.details["engine"] = plan.engine_ref
        if plan.status == scoped_plan.PLAN_ENGINE_FAILED:
            self.inv.summary = "repos/%s: %s" % (self.repo, plan.reason)
            return 2
        if plan.status == scoped_plan.PLAN_EMPTY:
            self.inv.summary = "repos/%s: %s" % (self.repo, plan.reason)
            return 2
        # a selection the engine answers with the full tier is never run
        # (the full bar is verification's): the run is incomplete, the
        # engine's own reasons kept, nothing claimed
        reasons = (scoped_plan.fallback_reasons(plan.report) if (plan.report is not None and plan.mode != TIER_LIGHT) else []) + plan.nested_full
        if reasons:
            status, reason = "incomplete", "; ".join(reasons)
            self.details["selected"] = {self.repo: self.selected_of(head, tree, base, branch, plan, status, reason)}
            self.inv.summary = ("repos/%s: the light selection fell back to the full tier — incomplete, nothing"
                                " ran: %s" % (self.repo, reason))
            return 1
        # every planned command: reused, or run in the repository's own
        # root (a nested root's command carries its own cd), never stopped
        # for a failure — the subtree hashes are taken before any command
        # runs, one snapshot of the working tree keying the whole run
        runs: List[Tuple[Dict[str, Any], EngineCommand, str, str, str, str, str]] = []
        incomplete: List[str] = []
        for c in plan.commands:
            self.tree_of(c.dir)
        for c in plan.commands:
            self.inv.say("repos/%s: `%s` (%s)" % (self.repo, c.cmd, c.phase))
            key = self.reuse_key(c)
            if key is not None and key in self.reuse_green:
                entry: Dict[str, Any] = {"name": c.name, "phase": c.phase, "root": self.command_root(c),
                                        "cmd": c.cmd, "source": c.source, "reused": self.reuse_green[key]}
                self.details["commands"].append(entry)
                continue
            entry, log_abs, tmp_dir = self.run_command(c, clone, self.command_root(c))
            entry["source"] = c.source
            self.details["commands"].append(entry)
            if entry.get("skipped"):
                incomplete.append("`%s` cannot run: %s" % (c.cmd, entry.get("skip_reason", "")))
            else:
                runs.append((entry, c, clone, self.command_root(c), c.dir, log_abs, tmp_dir))
        # the close's flaky phase: the selected commands' located failing
        # units re-run once, the still-red judged on the baseline (the same
        # light plan's own base setup preparing the base worktree), then the
        # whole command confirmed
        self.base_setup = list(plan.base_setup)
        self.rerun_phase(runs)
        self.baseline_phase()
        self.confirm_phase()
        self.details["reds"] = self.build_reds()
        red = [e for e in self.details["commands"] if self.command_red(e)]
        flaky_notes: List[str] = []
        for e in self.details["commands"]:
            for u in e.get("units", []):
                if u["status"] == "flaky":
                    flaky_notes.append("%s (%s, first failure log %s)" % (u["name"], u["flaky"], e.get("log", "")))
        flaky_note = (", %d unit(s) flaky on their one re-run: %s" % (len(flaky_notes), "; ".join(flaky_notes))) if flaky_notes else ""
        if red:
            status, reason = "red", ""
            e = red[0]
            what = "was killed at its timeout" if e.get("timed_out") else "exited %d" % e["exit_code"]
            self.inv.summary = "repos/%s: the selected `%s` (%s) %s" % (self.repo, e["cmd"], e["phase"], what)
            if e.get("log"):
                self.inv.summary += " (log %s)" % e["log"]
            if len(red) > 1:
                self.inv.summary += "; %d selected command(s) red in all" % len(red)
            if flaky_note:
                self.inv.summary += flaky_note
            self.inv.summary += self.classes_note(self.details["reds"])
            code = 1
        elif incomplete:
            status, reason = "incomplete", "; ".join(incomplete)
            self.inv.summary = "repos/%s: the selection is incomplete — %s" % (self.repo, reason)
            if flaky_note:
                self.inv.summary += flaky_note
            self.inv.summary += self.classes_note(self.details["reds"])
            code = 1
        elif self.details["reds"]:
            # every red the baseline proved pre-existing, nothing else red
            # and nothing incomplete: the selection stands beside them —
            # the analysis's validator takes the record as green-equivalent
            # evidence, and the hand-off cites the result file
            status, reason = "pre_existing", ""
            reused = len([e for e in self.details["commands"] if e.get("reused")])
            self.inv.summary = "repos/%s: %d selected command(s) green%s%s%s" % (
                self.repo, len(self.details["commands"]),
                (", %d reused" % reused) if reused else "", flaky_note,
                self.pre_existing_note(self.details["reds"]))
            code = 0
        else:
            status, reason = "green", ""
            reused = len([e for e in self.details["commands"] if e.get("reused")])
            self.inv.summary = "repos/%s: %d selected command(s) green%s%s" % (
                self.repo, len(self.details["commands"]),
                (", %d reused" % reused) if reused else "", flaky_note)
            code = 0
        self.details["selected"] = {self.repo: self.selected_of(head, tree, base, branch, plan, status, reason)}
        return code


def run(inv: Invocation, runtime: Optional[container.ContainerRuntime] = None) -> int:
    a = parser().parse_args(inv.argv)
    inv.out, inv.json = a.out, a.json
    if (a.repo.strip() == "") == (a.check_units.strip() == ""):
        return inv.usage("exactly one of --repo <name> and --check-units <file> is given")
    if a.unit and not a.check_units:
        return inv.usage("--unit %s names a unit of --check-units, which is missing" % gostr.quote(a.unit))
    if a.close_scoped and a.repo.strip() == "":
        return inv.usage("--close-scoped names the repo it closes: --repo <name> is missing")
    if a.close_scoped and a.check_units:
        return inv.usage("--close-scoped and --check-units are two runs: name one")
    if a.reuse and not a.close_scoped:
        return inv.usage("--reuse names a result file of --close-scoped, which is missing")
    if a.unit_runs and not a.close_scoped:
        return inv.usage("--unit-runs names the build's units.json of --close-scoped, which is missing")
    if (a.phase or a.root) and (a.close_scoped or a.check_units):
        return inv.usage("--phase and --root narrow a --repo run: they name no --close-scoped or --check-units run")
    capsule = os.path.abspath(a.capsule)
    engine = a.engine or default_engine(inv.env)
    logs = a.log_dir
    try:
        if logs == "":
            logs = os.path.join(os.path.dirname(a.out), "logs") if a.out else os.path.join(capsule, FAST_CHECKS_DIR)
        # one log directory per run (Q5): a later run's executions never
        # overwrite a reused entry's original log
        logs = next_run_dir(os.path.abspath(logs))
    except OSError as err:
        return inv.usage("the log directory %s: %s" % (logs, err))
    window = inv.deadline(a.deadline)
    r = Runner(capsule, time.time() + duration.seconds(window), env=dict(inv.env),
               deadline_what="the fast checks' timeout (%s)" % duration.format(window),
               command_timeout=COMMAND_TIMEOUT)
    if a.check_units:
        try:
            decls = read_unit_runs(a.check_units)
        except ValueError as err:
            return inv.usage("--check-units %s is not a usable units.json: %s" % (a.check_units, err))
        if a.unit:
            decls = [d for d in decls if d.id == a.unit]
            if not decls:
                return inv.usage("--unit %s names no unit of %s" % (gostr.quote(a.unit), a.check_units))
        if not decls:
            return inv.usage("--check-units %s names no unit: nothing to check" % a.check_units)
        uc = UnitCheck(inv, capsule, logs, r)
        uc.details["units_file"] = a.check_units
        inv.details = uc.details
        try:
            return uc.run(decls)
        except (KeyboardInterrupt, runner.Interrupted):
            inv.summary = "interrupted (context canceled): nothing is proven"
            return 2
        except runner.ExecutorError as err:
            inv.summary = "the units could not be checked (an infrastructure failure, never a verdict): %s" % err
            return 2
    if a.close_scoped:
        cr: CheckRun = CloseRun(inv, capsule, a.repo, engine, a.reuse, logs, r, runtime=runtime)
        if a.unit_runs:
            try:
                decls = read_unit_runs(a.unit_runs)
            except ValueError as err:
                return inv.usage("--unit-runs %s is not a usable units.json: %s" % (a.unit_runs, err))
            cr.protected = protected_paths(capsule, decls)
    else:
        cr = CheckRun(inv, capsule, a.repo, engine, r.deadline, logs, r, phase=a.phase, roots=a.root)
    inv.details = cr.details
    try:
        return cr.run()
    except (KeyboardInterrupt, runner.Interrupted):
        inv.summary = "interrupted (context canceled): nothing is proven"
        return 2
    except runner.ExecutorError as err:
        inv.summary = "the checks could not run (an infrastructure failure, never a verdict): %s" % err
        return 2


def main(argv: Optional[List[str]] = None, environ: Optional[Dict[str, str]] = None,
         runtime: Optional[container.ContainerRuntime] = None) -> int:
    """The command: argv (default sys.argv[1:]) under environ (default the
    process's); runtime replaces the docker CLI (a test's fake engine)."""
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    raise_on_termination()
    try:
        code = run(inv, runtime)
    except (KeyboardInterrupt, runner.Interrupted):
        inv.summary = "interrupted (context canceled): nothing is proven"
        code = 2
    except Exception as err:  # a bug, never a verdict: said, and not judged
        traceback.print_exc()
        inv.summary = "the checks could not run (an infrastructure failure, never a verdict): %s" % err
        code = 2
    return inv.finish(code)


if __name__ == "__main__":
    sys.exit(main())
