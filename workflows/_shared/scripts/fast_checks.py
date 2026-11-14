"""fast_checks.py — build's written fast/scoped check: the scope engine's
light reports for one task repo, their fast- and scoped-phase commands run
as written (the full phase is never run), each command in its report's
``root``, the result file the artifact build judges red or green from.

Usage (from the capsule root, or with --capsule)::

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/fast_checks.py" --repo <name> \\
        [--capsule DIR] [--engine PATH] [--deadline D] [--log-dir DIR] [--json] [--out PATH]

The command answers what `workflows/_shared/fast_checks_build.md` used to
ask the model to do by hand (polyforge#24: a report's commands were copied
out and run at the repository root, so a nested root's `npm ci` hit the
wrong directory and the step blocked on a dependency problem that did not
exist):

- it runs the scope engine ``--json --light`` in the repo clone
  (``<capsule>/repos/<name>``; the engine in this lane's snapshot, or
  --engine), decodes its report and takes the commands whose ``phase`` is
  ``fast`` or ``scoped`` — never one whose ``phase`` is ``full`` — in the
  report's order;
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

Exit 0: every command that ran exited 0 (skips cost nothing). Exit 1: a
command exited non-zero or was killed at its timeout. Exit 2: not judged —
no such repo, the engine missing or failing, an unreadable report, a repo
the engine planned no command for, the deadline, an interrupt — or the
invocation unusable.
"""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import time  # noqa: E402
import traceback  # noqa: E402
from typing import Any, Dict, List, Optional, Tuple  # noqa: E402

from pfstep import duration, git, gostr, runner, stepenv  # noqa: E402
from pfstep.engine import TIER_LIGHT, EngineCommand, EngineReport, ReportError, choose, decode_report  # noqa: E402
from pfstep.engine import argv_of as engine_argv_of  # noqa: E402
from pfstep.invoke import Invocation, duration_flag  # noqa: E402
from pfstep.runner import Runner, bash_argv, raise_on_termination  # noqa: E402

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


def default_engine(environ: Optional[Dict[str, str]] = None) -> str:
    """The scope engine when --engine names none: the shared tree's
    ``scripts/verify-scope``."""
    env = os.environ if environ is None else environ
    return os.path.join(stepenv.read(env).shared_dir, "scripts", "verify-scope")


def parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(prog=NAME, description="Run a task repo's fast and scoped checks per the scope engine's report.")
    ap.add_argument("--repo", default="", help="the task repo (repos/<name>) whose fast/scoped checks run")
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
        """The engine's light report with directory as its root: (report,
        the invocation, why there is none)."""
        argv = engine_argv_of(self.engine) + ["--json", "--light"]
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

        rep, ref, why = self.ask_engine(clone)
        self.details["engine"] = ref
        if rep is None:
            self.inv.summary = "repos/%s: %s" % (self.repo, why)
            return 2
        chosen, mode = choose(rep, TIER_LIGHT)
        self.details["reports"].append({"root": self.cwd_rel(clone), "mode": rep.mode,
                                        "light": mode == TIER_LIGHT, "commands": len(chosen)})
        if not chosen:
            self.inv.summary = ("repos/%s: the engine planned no command (stack %s, conf %s)"
                                % (self.repo, gostr.quote(rep.stack), rep.conf))
            return 2
        commands: List[Tuple[EngineCommand, str, str]] = [(c, clone, self.cwd_rel(clone)) for c in chosen]
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
    if a.repo.strip() == "":
        return inv.usage("--repo <name> names the task repo whose fast/scoped checks run")
    if "/" in a.repo or a.repo.startswith("."):
        return inv.usage("--repo %s: a repo name, the directory under repos/" % gostr.quote(a.repo))
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
