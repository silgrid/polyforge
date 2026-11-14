"""fast_checks.py — build's written fast/scoped check: the scope engine's
default reports for one task repo, their fast- and scoped-phase commands run
as written (the full phase is never run), each command in its report's
``root``, the result file the artifact build judges red or green from.

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo <name> \\
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
  ``full``, in the report's order;
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
  timeout) makes the whole run red.

Its logs go to ``<log-dir>/<repo>/<seq>/command.log``, the log dir being
--log-dir, else ``logs/`` beside --out, else the capsule's next
``runtime-state/fast-checks/<n>``; a .gitignore keeps them out of the
capsule chain. The result (``pf-step-result/1``) carries the repo's HEAD,
and in ``details`` the engine invocation (``engine``), one entry per report
(``reports[]``: its root, mode and command count) and one per command
(``commands[]``: name, phase, root, cmd, exit_code, timed_out, duration_ms,
log — or skipped and skip_reason).

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
or verification ever reads it. ``--unit <id>`` checks only that unit.
The result carries the repo HEADs the units ran at.

Exit 0: every command that ran exited 0 (skips cost nothing), or every
checked unit passed. Exit 1: a command exited non-zero or was killed at
its timeout, or a unit is red. Exit 2: not judged — no such repo, the
engine missing or failing, an unreadable report, a repo the engine planned
no command for, an unusable units.json, the deadline, an interrupt — or
the invocation unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import difflib  # noqa: E402
import time  # noqa: E402
import traceback  # noqa: E402
from typing import Any, Dict, List, Optional, Sequence, Tuple  # noqa: E402

from pfstep import duration, git, gostr, runner  # noqa: E402
from pfstep.engine import EngineCommand, EngineReport, ReportError, decode_report  # noqa: E402
from pfstep.engine import argv_of as engine_argv_of  # noqa: E402
from pfstep.engine import default_engine, nested_roots, reached_roots  # noqa: E402
from pfstep.invoke import Invocation, duration_flag  # noqa: E402
from pfstep.runner import Runner, bash_argv, raise_on_termination  # noqa: E402
from pfstep.unitruns import UnitRunDecl, clean_cwd, marker_matches, marker_problem, read_unit_runs  # noqa: E402
from pfstep.units import output_lines  # noqa: E402

NAME = "fast_checks.py"

# A command that declares no timeout runs under the run's own bound (s).
COMMAND_TIMEOUT = 20 * duration.MINUTE / duration.SECOND
GIT_TIMEOUT = 600.0

# A log directory's .gitignore: logs are local, never capsule chain content.
LOGS_GITIGNORE = "# pf step command logs are local, never capsule chain content\n*\n"

# The capsule's own log namespace when --out and --log-dir name none.
FAST_CHECKS_DIR = "runtime-state/fast-checks"


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

    def prepare(self, d: UnitRunDecl, entry: Dict[str, Any]) -> Tuple[Optional[str], Optional[str]]:
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
        directory, prepared = self.prepare(d, entry)
        why = prepared if directory is None else marker_problem(d.marker)
        if directory is None or why:
            entry["passed"] = False
            entry["why"] = why
            return entry
        self.seq += 1
        log_path = os.path.join(self.log_dir, d.repo, "unit-%d" % self.seq, "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        er = self.r.exec_to(runner.ExecSpec(argv=list(d.argv), dir=directory, cwd=prepared or "",
                                             timeout=COMMAND_TIMEOUT, private_tmp=True), log_path)
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
            entry["why"] = "exit %d: killed at its timeout" % er.exit
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
        if red:
            u = red[0]
            self.inv.summary = "%s: %s%s" % (u["check"], u["why"],
                                             (" (closest output line: %s)" % u["closest_line"]) if u.get("closest_line") else "")
            if len(red) > 1:
                self.inv.summary += "; %d unit(s) red in all" % len(red)
            return 1
        self.inv.summary = "%d unit(s) checked, every marker matched" % len(self.details["units"])
        return 0


class CheckRun:
    """One repo's fast/scoped checks: its engine reports, and the commands
    they contribute, run in their report's root."""

    def __init__(self, inv: Invocation, capsule: str, repo: str, engine: str,
                 deadline: float, log_dir: str, r: Runner) -> None:
        self.inv = inv
        self.capsule = capsule
        self.repo = repo
        self.engine = engine
        self.deadline = deadline
        self.log_dir = log_dir
        self.r = r
        self.seq = 0
        self.details: Dict[str, Any] = {"repo": repo, "reports": [], "commands": []}
        self.head = ""

    def cwd_rel(self, directory: str) -> str:
        """The directory as the record names it: capsule-relative inside the
        capsule ("repos/<repo>" at its root, plus the nested root)."""
        return self.r.rel(directory)

    def ask_engine(self, directory: str) -> Tuple[Optional[EngineReport], str, str]:
        """The engine's report with directory as its root — the default
        one, no tier flag, exactly the report the model ran by hand: (the
        report, the invocation, why there is none)."""
        argv = engine_argv_of(self.engine) + ["--json"]
        ref = " ".join(argv)
        self.seq += 1
        log_path = os.path.join(self.log_dir, self.repo, "engine-%d" % self.seq, "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        er = self.r.exec_to(runner.ExecSpec(argv=argv, dir=directory, cwd=self.cwd_rel(directory),
                                             timeout=GIT_TIMEOUT, capture=True), log_path)
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

    def run_command(self, c: EngineCommand, directory: str, root_rel: str) -> Dict[str, Any]:
        """One planned command in its report's root, skipped when its
        declared prerequisites are unmet."""
        entry: Dict[str, Any] = {"name": c.name, "phase": c.phase, "root": root_rel, "cmd": c.cmd}
        unmet = self.r.unmet(c.requires_env, c.requires_cmd, directory, None)
        if unmet:
            entry["skipped"] = True
            entry["skip_reason"] = unmet
            return entry
        self.seq += 1
        log_path = os.path.join(self.log_dir, self.repo, str(self.seq), "command.log")
        os.makedirs(os.path.dirname(log_path), exist_ok=True)
        er = self.r.exec_to(runner.ExecSpec(argv=bash_argv(c.cmd), dir=directory, cwd=root_rel,
                                             timeout=timeout_of(c), private_tmp=True), log_path)
        entry["exit_code"] = er.exit
        if er.timed_out:
            entry["timed_out"] = True
        if er.not_started:
            entry["not_started"] = True
        entry["duration_ms"] = er.evidence.duration_ms if er.evidence is not None else 0
        if er.log_path:
            entry["log"] = self.r.rel(er.log_path)
            self.inv.logs.append(entry["log"])
        return entry

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

        # every planned command: (the command, the directory it runs in —
        # its report's root —, that root as the record names it)
        commands: List[Tuple[EngineCommand, str, str]] = []
        rep, ref, why = self.ask_engine(clone)
        self.details["engine"] = ref
        if rep is None:
            self.inv.summary = "repos/%s: %s" % (self.repo, why)
            return 2
        # AC2/AC5: the report is the default one — the report the model
        # ran by hand — and only its fast and scoped phases ever run: a
        # change on a forced-full path behaves exactly as before (its fast
        # gates and nothing more)
        chosen = [c for c in rep.commands if c.phase in ("fast", "scoped")]
        self.details["reports"].append({"root": self.cwd_rel(clone), "mode": rep.mode,
                                        "commands": len(chosen)})
        commands += [(c, clone, self.cwd_rel(clone)) for c in chosen]
        # every nested engine root the changed paths reach is a repository
        # of its own whatever the root report answered — the #24 shape has
        # the root conf force the full tier for the nested root's paths, and
        # the nested root's own report still runs there, in its root, not
        # behind a cd the model writes
        for d in reached_roots(nested_roots(clone), rep.paths or []):
            directory = os.path.join(clone, d)
            root_rel = self.cwd_rel(directory)
            nrep, nref, nwhy = self.ask_engine(directory)
            if nrep is None:
                self.inv.summary = "repos/%s: the nested engine root %s: %s" % (self.repo, d, nwhy)
                return 2
            nchosen = [c for c in nrep.commands if c.phase in ("fast", "scoped")]
            self.details["reports"].append({"root": root_rel, "mode": nrep.mode,
                                            "commands": len(nchosen)})
            commands += [(nested_command(d, c), directory, root_rel) for c in nchosen]
        if not commands:
            self.inv.summary = ("repos/%s: the engine planned no command (stack %s, conf %s)"
                                % (self.repo, gostr.quote(rep.stack), rep.conf))
            return 2
        for c, directory, root_rel in commands:
            self.inv.say("repos/%s: `%s` (%s)" % (self.repo, c.cmd, c.phase))
            entry = self.run_command(c, directory, root_rel)
            self.details["commands"].append(entry)
        red = [e for e in self.details["commands"]
               if not e.get("skipped") and (e.get("timed_out") or e.get("exit_code") != 0)]
        skipped = len([e for e in self.details["commands"] if e.get("skipped")])
        ran = len(self.details["commands"]) - skipped
        if red:
            e = red[0]
            what = "was killed at its timeout" if e.get("timed_out") else "exited %d" % e["exit_code"]
            self.inv.summary = "repos/%s: `%s` (%s) %s" % (self.repo, e["cmd"], e["phase"], what)
            if e.get("log"):
                self.inv.summary += " (log %s)" % e["log"]
            if len(red) > 1:
                self.inv.summary += "; %d command(s) red in all" % len(red)
            return 1
        self.inv.summary = "repos/%s: %d command(s) ran and exited 0%s" % (
            self.repo, ran, (", %d skipped" % skipped) if skipped else "")
        return 0


def run(inv: Invocation) -> int:
    a = parser().parse_args(inv.argv)
    inv.out, inv.json = a.out, a.json
    if (a.repo.strip() == "") == (a.check_units.strip() == ""):
        return inv.usage("exactly one of --repo <name> and --check-units <file> is given")
    if a.unit and not a.check_units:
        return inv.usage("--unit %s names a unit of --check-units, which is missing" % gostr.quote(a.unit))
    capsule = os.path.abspath(a.capsule)
    engine = a.engine or default_engine(inv.env)
    logs = a.log_dir
    try:
        if logs == "":
            logs = os.path.join(os.path.dirname(a.out), "logs") if a.out else next_run_dir(os.path.join(capsule, FAST_CHECKS_DIR))
        logs = os.path.abspath(logs)
        ensure_log_dir(logs)
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
    cr = CheckRun(inv, capsule, a.repo, engine, r.deadline, logs, r)
    inv.details = cr.details
    try:
        return cr.run()
    except (KeyboardInterrupt, runner.Interrupted):
        inv.summary = "interrupted (context canceled): nothing is proven"
        return 2
    except runner.ExecutorError as err:
        inv.summary = "the checks could not run (an infrastructure failure, never a verdict): %s" % err
        return 2


def main(argv: Optional[List[str]] = None, environ: Optional[Dict[str, str]] = None) -> int:
    inv = Invocation(NAME, sys.argv[1:] if argv is None else argv, environ)
    raise_on_termination()
    try:
        code = run(inv)
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
