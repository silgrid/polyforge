"""gate.py — the full test gate: the only judge of a releasable commit, the
only writer of a green tag, and the integrate lane's triage of a red gate.

    "$PF_PYTHON" "$PF_SHARED_DIR/scripts/gate.py" --isolation container \\
        [--keep-going] [--timeout D] [--deadline D] [--cpus N] [--memory M] [--pids N]
        [--log-dir DIR] [--repo NAME] [--json] [--out PATH]
        [--mark-green --summary TEXT [--remote R]]
    gate.py --isolation container --plan-only [--log-dir DIR] [--repo NAME] [the limit flags]
    gate.py --isolation container --capsule DIR --pins FILE
        [--mark-green --summary TEXT [--remote R]] [--json] [--out PATH] [the run flags]
    gate.py --isolation container --triage --capsule DIR --pins FILE --gate FILE --batch FILE
        [--pool FILE] [--mark-green --summary TEXT [--remote R]] [--json] [--out PATH] [the run flags]

The gate is the tree's own full verification plan — its scope engine,
``workflows/_shared/scripts/verify-scope --json --full --base HEAD``: the fast gates, the full
commands and the engine-appended dependency audit, in the engine's order —
with every command, the engine included, run inside the repository's
verification container (``--isolation container``, the only isolation a
gate has: a tree without ``.polyforge/Dockerfile.verify`` at HEAD is not
judged, exit 2; nothing of the gate runs on the host but git and the docker
client). The verdict is strict: the gate passes only when every planned
command ran and exited 0; a declared prerequisite the container lacks fails
its command; there are no flaky re-runs and no pre-existing proofs. It stops
at the first failing command unless ``--keep-going``. A gate that cannot be
judged — no Dockerfile or engine at HEAD, the container engine missing or
not answering, the image or the container not had, a container lost
mid-run, an engine that plans nothing, the deadline, an interrupt, HEAD
moving while it ran — is never a pass and never a failure.

The container (``pfstep.container``): the image ``pf-verify-<repo>:<sha16>``
built from HEAD's Dockerfile alone, the clone mounted at its own path, the
cache volume, the environment allowlist, a private TMPDIR per command and
every process a command left killed after it. Its limits are this
script's: ``--cpus`` (default min(8, this machine's cores)), ``--memory``
(default 16g; docker's syntax, at least 6m) and ``--pids`` (default 4096).
Its labels are the gate's own (``pf.verify.daemon=pf-gate`` and a fresh
boot), the work item (``PF_WI_ID``, else ``gate``), with the run's
deadline as ``pf.verify.expires`` and a
keep-alive ending five minutes after it; the gate removes it when it ends,
and removes every container of its owner whose deadline has passed before
it starts one.

Without ``--capsule`` the gate judges the clone at the working directory
(the release's and an operator's run): progress on stdout, the green tag's
name on the last stdout line (both on stderr with ``--json``). Exit 0 pass,
1 a command failed, 2 not judged or the invocation unusable, 3 the gate
passed but its green tag was not written. ``--plan-only`` runs no command
of the plan: it prints the plan the gate would run, its digest and the
runner identity as one JSON object (tools/green-tag check's side of the
comparison) — no result file.

``--capsule DIR --pins FILE`` (the integrate lane's full-gate step) gates
every gated repo's pinned head in the batch capsule's clones
(``repos/<name>``): the pins are a result file (its ``heads`` and its
details' ``gated``) or a bare ``{heads, gated}`` object. Each clone's
default branch and the green tags are fetched and the pinned head checked
out first; every gated repo is gated (never stopping at the first red one),
and ``--mark-green`` tags each head once every repo passed, as the lane's
own identity. Exit 0 every gated repo passed (or none is gated), 1 one
failed, 2 one could not be judged, 3 every repo passed but a tag was not
written. Details: ``{mode, pins, gated, repos: {repo: {head, branch,
result, tag, tag_error}}, tags, failed, incomplete, log_dir}``.

``--triage`` (owner ruling O4: the command produces the facts, the agent
decides) triages the red repos of ``--gate`` (this script's result for the
same pins) in the gate's container: every failing unit re-runs once at the
red head (a pass is flaky); a unit still red runs at the baseline — the
newest green tag merged into the head, else the parent of the batch's
oldest merge (``--batch``: collect's result) — and is pre-existing only by
the verification's rule; every other red unit is attributed to the landed
items of the integration pool (``--pool FILE``, else read now through the
step's hub: ``pf --json step hub list``) by its file, its ``carries:``, its
Go package and the engine's selection, else bisected over the default
branch's first-parent commits since the baseline (a first red commit that is
no item's merge is outside the batch). When every unit re-ran green the full
gate runs once more on the same pins. Exit 0 every failure was flaky and
the gate passed again, 1 the red stands (or the regate failed), 2 the
triage could not run, 3 the regate passed but a tag was not written.
Details: ``{pins, gate, gate_run, repos, baselines, units: [{repo, command,
unit, kind, file, class, item, root, why, base, outside, rerun, log,
signature, excerpt}], classes, all_flaky, regate, log_dir}``. The fix tickets
and attributions are the agent's: it writes them as hub writes and
registers them with ``pf step hub batch``.

A green tag (``--mark-green``, after a pass only) is an annotated tag
``green/<YYYYMMDD>-<n>`` on the judged commit, pushed without force (a
name another writer took first: the next number, at most five tries; a
commit already carrying a tag of the same record: that one, nothing
pushed), its message one line of JSON ``{commit, gate_id, runner,
runner_version, plan_digest, verified_at, summary}`` (``gate_id``: the
tree's ``tools/gate-id``). The result files' ``command`` and a green tag's
``runner`` are ``gate.py``, this script's file name.

The deadline is ``--deadline``, else derived from the step's tool window,
else three hours; a command's timeout is its declared one, else
``--timeout`` (20 minutes).
"""

import argparse
import datetime
import hashlib
import json
import os
import re
import sys
import tempfile
import time
import traceback
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Mapping, Optional, Protocol, Sequence, Tuple

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import pfstep  # noqa: E402
from pfstep import container, duration, git, gostr, hub, jsonout, proc, result, runner, stepenv  # noqa: E402
from pfstep.carries import file_carries, glob_match  # noqa: E402
from pfstep.deadline import default_deadline_for  # noqa: E402
from pfstep.engine import TIER_FULL, EngineCommand, EngineReport, ReportError, choose, decode_report  # noqa: E402
from pfstep.engine import argv_of as engine_argv_of  # noqa: E402
from pfstep.preexisting import ABSENT, PRESENT, BaseObservation, classify_base, unit_presence  # noqa: E402
from pfstep.rerun import CommandSpec, UnitState, locate  # noqa: E402
from pfstep.shellwords import has_flag, has_go_test, shell_words  # noqa: E402
from pfstep.units import (  # noqa: E402
    KIND_COMMAND,
    KIND_GO,
    KIND_GO_PKG,
    Unit,
    cap_text,
    find_unit,
    log_tail,
    normalize_output,
    output_lines,
    parse_units,
    race_seen,
    read_lines,
    unit_passed,
)

# The result file's command and the green tag's runner: the script's file
# name, as its readers (integrate.py close, tools/green-tag) check it.
COMMAND = "gate.py"
VERSION = pfstep.LIBRARY
GATE_RUNNER = "gate.py"
# GATE_RUNNER_VERSION names the verdict rules and the plan digest's input;
# bump it whenever either changes, so a green tag written under the old
# rules is never reused under the new ones. 1 was the Go pf-gate's; 2 is
# this script's (its digest is of another encoding, its runner gate.py).
GATE_RUNNER_VERSION = "2"

# The scope engine the gate plans with: the tree's own (the workflow's).
GATE_ENGINE_REL = "workflows/_shared/scripts/verify-scope"

DEFAULT_GATE_DEADLINE = 3 * duration.HOUR
DEFAULT_COMMAND_TIMEOUT = 20 * duration.MINUTE

# Bounds one git call and one engine call (seconds).
GIT_TIMEOUT = 600.0

# How much of a failing command's log the progress shows.
TAIL_LINES = 40

# The retries when concurrent writers take the computed tag name first.
GREEN_PUSH_TRIES = 5

# The lane's identity: the integrate lane's green tags carry it as their
# tagger, never the host's (the daemon's commit identity).
LANE_TAGGER = "polyforge daemon <daemon@polyforge.invalid>"

# The pf.verify.wi label of a gate's container outside a step.
GATE_WI = "gate"

# The command whose result file --batch is (integrate.py collect's).
INTEGRATE_COMMAND = "integrate.py"

ISOLATION_CONTAINER = runner.ISOLATION_CONTAINER

# The unit classes of a triage, and the verdicts they come from.
CLASS_FLAKY = "flaky"
CLASS_PRE_EXISTING = "pre_existing"
CLASS_ATTRIBUTED = "attributed"
CLASS_UNATTRIBUTED = "unattributed"
VERDICT_FLAKY = "flaky"
VERDICT_PRE_EXISTING = "pre-existing"
VERDICT_RED = "red"

LOGS_GITIGNORE = "# pf step command logs are local, never capsule chain content\n*\n"

_GREEN_NAME = re.compile(r"green/([0-9]{8})-([0-9]+)\Z", re.ASCII)
_GATE_ID = re.compile(r"[0-9a-f]{64}\Z", re.ASCII)
_MEMORY = re.compile(r"([0-9]+)([bkmgBKMG]?)\Z", re.ASCII)
_RUN_DIR = re.compile(r"[+-]?[0-9]+\Z", re.ASCII)

Say = Callable[[str], None]


class Usage(Exception):
    """An unusable invocation: its message on stderr, exit 2."""


class TriageError(Exception):
    """Why a triage step could not be judged."""


# ---- small helpers ---------------------------------------------------------------


def short(sha: str) -> str:
    """A commit's first 12 characters."""
    return sha[:12] if len(sha) > 12 else sha


def or_text(s: str, alt: str) -> str:
    return alt if gostr.trim_space(s) == "" else s


def or_id(slug: str, wi: str) -> str:
    return slug if slug else wi


def clip_runes(s: str, limit: int) -> str:
    """s cut to limit characters, the cut said (the driver's rule)."""
    if len(s) > limit:
        return s[:limit] + "… (truncated; the full output is in the step's archived attempt)"
    return s


def clip(s: str, n: int) -> str:
    return s if len(s) <= n else s[:n] + "…"


def round_seconds(ns: int) -> int:
    """ns rounded to whole seconds, half away from zero (Go's Round)."""
    if ns < 0:
        return -round_seconds(-ns)
    return (ns + duration.SECOND // 2) // duration.SECOND * duration.SECOND


def dur(ns: int) -> str:
    return duration.format(ns)


def ensure_log_dir(d: str) -> None:
    """d created, with the .gitignore that keeps it out of the capsule chain."""
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
    """<root>/<n>, n one past the highest existing run (root kept out of the chain)."""
    ensure_log_dir(root)
    n = 0
    for name in os.listdir(root):
        if _RUN_DIR.fullmatch(name):
            n = max(n, int(name))
    while True:
        n += 1
        d = os.path.join(root, str(n))
        try:
            os.mkdir(d)
            return d
        except FileExistsError:
            continue


def check_memory(v: str) -> str:
    """Why v is no container memory limit ("" = it is): an integer with an
    optional unit b, k, m or g (docker's --memory syntax), at least docker's
    6m floor."""
    m = _MEMORY.fullmatch(gostr.trim_space(v))
    if m is None:
        return ("%s is not a size: an integer with an optional unit b, k, m or g (docker's --memory syntax,"
                " e.g. \"8g\")" % gostr.quote(v))
    n = int(m.group(1))
    shift = {"": 0, "b": 0, "k": 10, "m": 20, "g": 30}[m.group(2).lower()]
    if n > (1 << 62) >> shift:
        return "%s is out of range" % gostr.quote(v)
    if n << shift < 6 << 20:
        return "%s is below docker's 6m floor" % gostr.quote(v)
    return ""


# ---- the gate's git ---------------------------------------------------------------


class Git:
    """The hardened git calls of the gate (pfstep.git), under one environment."""

    def __init__(self, environ: Optional[Mapping[str, str]] = None) -> None:
        self.environ = dict(os.environ if environ is None else environ)

    def run(self, directory: str, *args: str, input: Optional[bytes] = None) -> proc.Completed:
        return git.run(directory, *args, timeout=GIT_TIMEOUT, environ=self.environ, input=input)

    def out(self, directory: str, *args: str, input: Optional[bytes] = None) -> str:
        return git.git(directory, *args, timeout=GIT_TIMEOUT, environ=self.environ, input=input)

    def ok(self, directory: str, *args: str) -> bool:
        return self.run(directory, *args).exit_code == 0

    def rev(self, directory: str, rev: str) -> str:
        return gostr.trim_space(self.out(directory, "rev-parse", "--verify", "--quiet", rev + "^{commit}"))

    def head(self, directory: str) -> str:
        return gostr.trim_space(self.out(directory, "rev-parse", "--verify", "HEAD^{commit}"))

    def is_ancestor(self, directory: str, a: str, b: str) -> bool:
        return self.ok(directory, "merge-base", "--is-ancestor", a, b)

    def default_branch(self, directory: str) -> str:
        """origin's default branch (origin/HEAD, else main)."""
        c = self.run(directory, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
        if c.exit_code == 0:
            b = gostr.trim_space(c.stdout.decode("utf-8", "replace"))
            if b.startswith("origin/"):
                b = b[len("origin/"):]
            if b:
                return b
        return "main"

    def fetch(self, directory: str, *branches: str) -> None:
        """origin's named branches into refs/remotes/origin/*, and the green tags."""
        args = ["fetch", "-q", "origin", "+refs/tags/green/*:refs/tags/green/*"]
        args += ["+refs/heads/" + b + ":refs/remotes/origin/" + b for b in branches]
        self.out(directory, *args)

    def checkout(self, directory: str, rev: str) -> None:
        """The worktree on a detached, clean rev."""
        self.run(directory, "merge", "--abort")
        self.out(directory, "checkout", "-q", "-f", "--detach", rev)
        self.out(directory, "reset", "-q", "--hard", rev)
        self.out(directory, "clean", "-q", "-fd")

    def first_parents(self, directory: str, frm: str, to: str) -> List[str]:
        """The first-parent commits of to since frm, oldest first (frm
        excluded); no frm: the newest 500."""
        rng = [frm + ".." + to] if frm else ["--max-count=500", to]
        return gostr.fields(self.out(directory, "rev-list", "--first-parent", "--reverse", *rng))

    def changed_paths(self, directory: str, merge: str) -> List[str]:
        """The paths a merge brought onto its first parent."""
        out: List[str] = []
        for p in self.out(directory, "diff", "--name-only", "-z", merge + "^1", merge).split("\x00"):
            if p and p not in out:
                out.append(p)
        return out

    def tags(self, directory: str, *args: str) -> List[str]:
        c = self.run(directory, "tag", "--list", "green/*", *args)
        if c.exit_code != 0:
            return []
        return gostr.fields(c.stdout.decode("utf-8", "replace"))


def newest_green_of(names: Sequence[str]) -> str:
    """The newest well-formed green tag of names: its date, then its number."""
    best, bd, bn = "", "", -1
    for t in names:
        m = _GREEN_NAME.fullmatch(t)
        if m is None:
            continue
        k = int(m.group(2))
        if m.group(1) > bd or (m.group(1) == bd and k > bn):
            best, bd, bn = t, m.group(1), k
    return best


def newest_green(g: Git, directory: str, ref: str) -> str:
    """The newest well-formed green tag merged into ref ("" = none)."""
    return newest_green_of(g.tags(directory, "--merged", ref))


# ---- the plan and the result ------------------------------------------------------


@dataclass
class GateCommand:
    """One planned command of a gate run."""

    name: str
    phase: str
    cmd: str
    exit: int = 0
    timed_out: bool = False
    duration_ns: int = 0
    log: str = ""
    # unmet: the declared prerequisite the container lacks (the command
    # fails without running)
    unmet: str = ""
    # not_run: an earlier failure stopped the gate, or it ended incomplete
    not_run: bool = False

    def failed(self) -> bool:
        return self.not_run or self.unmet != "" or self.exit != 0

    def as_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {"name": self.name, "phase": self.phase, "cmd": self.cmd, "exit": self.exit}
        if self.timed_out:
            d["timed_out"] = True
        d["duration_ns"] = self.duration_ns
        if self.log:
            d["log"] = self.log
        if self.unmet:
            d["unmet"] = self.unmet
        if self.not_run:
            d["not_run"] = True
        return d

    @classmethod
    def of(cls, d: Mapping[str, Any]) -> "GateCommand":
        return cls(
            name=str(d.get("name") or ""), phase=str(d.get("phase") or ""), cmd=str(d.get("cmd") or ""),
            exit=int(d.get("exit") or 0), timed_out=bool(d.get("timed_out")), duration_ns=int(d.get("duration_ns") or 0),
            log=str(d.get("log") or ""), unmet=str(d.get("unmet") or ""), not_run=bool(d.get("not_run")),
        )


def plan_digest(plan: Mapping[str, Any]) -> str:
    """The plan's identity: the sha256 of its compact JSON encoding (its
    keys in the order built, non-ASCII as UTF-8)."""
    raw = json.dumps(plan, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


def plan_of(directory: str, report: container.ContainerReport, cmds: Sequence[EngineCommand],
            timeout_of: Callable[[EngineCommand], int]) -> Dict[str, Any]:
    """The plan as a gate executes it — what its digest, the identity a green
    tag records and the release compares, is made of: the image's content
    half (the Dockerfile's hash), the container's limits, and every command
    in order with its phase, its declared prerequisites and the timeout it
    runs under. A command naming the clone's path names it as <clone>, so
    where a clone lives never splits the digest."""
    olds = [directory]
    resolved = os.path.realpath(directory)
    if resolved != directory:
        olds.append(resolved)
    pat = re.compile("|".join(re.escape(o) for o in olds))
    commands = []
    for c in cmds:
        commands.append({
            "name": c.name, "phase": c.phase, "cmd": pat.sub("<clone>", c.cmd),
            "requires_env": list(c.requires_env), "requires_cmd": list(c.requires_cmd),
            "timeout": duration.format(timeout_of(c)),
        })
    return {"image": report.image[report.image.rfind(":") + 1:], "cpus": report.cpus, "memory": report.memory,
            "pids": report.pids, "commands": commands}


@dataclass
class GateResult:
    """One gate run's outcome."""

    # commit: the HEAD it judged; clean: whether the tree was clean when it
    # started (a green tag needs both)
    commit: str = ""
    clean: bool = False
    runner: str = GATE_RUNNER
    runner_version: str = GATE_RUNNER_VERSION
    plan: Optional[Dict[str, Any]] = None
    plan_digest: str = ""
    image: str = ""
    built: bool = False
    container: str = ""
    # engine: the engine invocation the plan came from
    engine: str = ""
    log_dir: str = ""
    commands: List[GateCommand] = field(default_factory=list)
    # incomplete: why the gate could not be judged ("" = it ran)
    incomplete: str = ""
    passed: bool = False
    duration_ns: int = 0

    def as_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {}
        if self.commit:
            d["commit"] = self.commit
        d.update({"clean": self.clean, "runner": self.runner, "runner_version": self.runner_version})
        if self.plan is not None:
            d["plan"] = self.plan
        if self.plan_digest:
            d["plan_digest"] = self.plan_digest
        if self.image:
            d["image"] = self.image
        d["built"] = self.built
        if self.container:
            d["container"] = self.container
        if self.engine:
            d["engine"] = self.engine
        d["log_dir"] = self.log_dir
        d["commands"] = [c.as_dict() for c in self.commands]
        if self.incomplete:
            d["incomplete"] = self.incomplete
        d["passed"] = self.passed
        d["duration_ns"] = self.duration_ns
        return d

    @classmethod
    def of(cls, d: Mapping[str, Any]) -> "GateResult":
        plan = d.get("plan")
        return cls(
            commit=str(d.get("commit") or ""), clean=bool(d.get("clean")), runner=str(d.get("runner") or ""),
            runner_version=str(d.get("runner_version") or ""), plan=dict(plan) if isinstance(plan, dict) else None,
            plan_digest=str(d.get("plan_digest") or ""), image=str(d.get("image") or ""), built=bool(d.get("built")),
            container=str(d.get("container") or ""), engine=str(d.get("engine") or ""), log_dir=str(d.get("log_dir") or ""),
            commands=[GateCommand.of(c) for c in d.get("commands") or [] if isinstance(c, dict)],
            incomplete=str(d.get("incomplete") or ""), passed=bool(d.get("passed")),
            duration_ns=int(d.get("duration_ns") or 0),
        )


def fail_summary(res: GateResult) -> str:
    """One line on a red gate: its failed commands."""
    failed = []
    for c in res.commands:
        if c.not_run:
            continue
        if c.unmet:
            failed.append("%s (unmet: %s)" % (c.name, c.unmet))
        elif c.timed_out:
            failed.append("%s (timed out)" % c.name)
        elif c.exit != 0:
            failed.append("%s (exit %d)" % (c.name, c.exit))
    return "%d of %d commands failed: %s" % (len(failed), len(res.commands), ", ".join(failed))


def gate_logs(res: GateResult) -> List[str]:
    """A gate run's logs: one per command that ran."""
    return [c.log for c in res.commands if c.log and not c.not_run]


# ---- running the gate -------------------------------------------------------------


@dataclass
class Opened:
    """A gate's container held on one clone."""

    r: runner.Runner
    box: container.Box
    directory: str
    log_dir: str


class Gate:
    """What every gate of one invocation runs with: one container option set
    (one boot label, the invocation's deadline), the deadline itself and the
    progress stream."""

    def __init__(
        self,
        co: container.ContainerOptions,
        until: float,
        window: int,
        command_timeout: int,
        keep_going: bool,
        cmd_env: Dict[str, str],
        say: Say,
    ) -> None:
        # co: for_run's (the defaults, the deadline label, the keep-alive)
        self.co = co
        # until: epoch seconds; no command of the invocation runs past it
        self.until = until
        self.window = window
        self.command_timeout = command_timeout
        self.keep_going = keep_going
        self.cmd_env = cmd_env
        self.say_line = say
        self.swept = False
        # interrupted: the signal that ended the run ("" = none)
        self.interrupted = ""

    def say(self, msg: str) -> None:
        self.say_line("gate: " + msg)

    def deadline_note(self) -> str:
        return "the gate's deadline (%s)" % dur(self.window)

    def timeout_of(self, c: EngineCommand) -> int:
        try:
            d = duration.parse(gostr.trim_space(c.timeout))
        except ValueError:
            return self.command_timeout
        return d if d > 0 else self.command_timeout

    def runner_for(self, directory: str) -> runner.Runner:
        return runner.Runner(directory, self.until, env=self.cmd_env, deadline_what=self.deadline_note(),
                             command_timeout=self.command_timeout / duration.SECOND)

    def open(self, directory: str, repo: str, log_dir: str) -> Tuple[Optional[Opened], str]:
        """The gate's container on the clone at directory — the engine's
        availability, the owner's expired containers swept (once per
        invocation), the image and the container, the mount probe — or why
        it could not be had."""
        rt = self.co.engine()
        try:
            rt.available()
        except container.ContainerError as err:
            return None, ("the gate runs inside the %s container, but the container engine is unavailable: %s"
                          % (container.DOCKERFILE_VERIFY, err))
        if not self.swept:
            self.swept = True
            container.sweep_run(self.co, self.say)
        r = self.runner_for(directory)
        engine = os.path.realpath(os.path.join(directory, GATE_ENGINE_REL))
        self.say("building or finding the image of %s (log %s)"
                 % (container.DOCKERFILE_VERIFY, os.path.join(log_dir, runner.IMAGE_BUILD_LOG)))
        try:
            box = container.start_box(self.co, repo, directory, log_dir, self.until, 0, r.rel, engine=engine)
        except container.BoxError as err:
            return None, "the gate's container could not be prepared: %s" % err
        rep = box.report
        self.say("image %s (%s); container %s (cpus %s, memory %s, pids %d); logs %s"
                 % (rep.image, "built" if rep.built else "present", rep.name, rep.cpus, rep.memory, rep.pids, log_dir))
        try:
            why = r.same_clone(box, directory, log_dir)
        except BaseException:
            box.remove()
            raise
        if why:
            box.remove()
            return None, why
        return Opened(r=r, box=box, directory=directory, log_dir=log_dir), ""

    def ask_engine(self, o: Opened, base: str, full: bool) -> Tuple[Optional[EngineReport], str, str]:
        """The tree's engine asked in the container (--full, or a scoped
        report for base..HEAD): (report, the invocation, why there is none)."""
        argv = engine_argv_of(o.box.engine) + ["--json"]
        if full:
            argv.append("--full")
        if base:
            argv += ["--base", base]
        ref = " ".join(argv)
        er = o.r.exec(o.log_dir, runner.ExecSpec(argv=argv, dir=o.directory, cwd=".", timeout=GIT_TIMEOUT, capture=True), o.box)
        if er.not_started:
            return None, ref, "%s passed before the engine could run" % self.deadline_note()
        if er.error:
            return None, ref, "the engine %s could not start: %s" % (o.box.engine, er.error)
        if er.exit != 0:
            return None, ref, "the engine exited %d: %s" % (er.exit, log_tail(er.log_path))
        try:
            return decode_report(er.stdout), ref, ""
        except ReportError as err:
            return None, ref, "the engine's report is unreadable: %s" % err

    def run(self, directory: str, repo: str = "", log_dir: str = "", plan_only: bool = False) -> GateResult:
        """The gate on the clone at directory. A red or incomplete gate is
        data (passed False)."""
        directory = os.path.abspath(directory)
        repo = repo or os.path.basename(directory)
        if log_dir:
            os.makedirs(log_dir, exist_ok=True)
        else:
            log_dir = tempfile.mkdtemp(prefix="pf-gate-")
        log_dir = os.path.abspath(log_dir)
        res = GateResult(log_dir=log_dir)
        mono = time.monotonic()
        try:
            self._run(res, directory, repo, log_dir, plan_only)
        except (KeyboardInterrupt, runner.Interrupted) as err:
            self.interrupted = interrupt_name(err)
            if not res.incomplete:
                res.incomplete = "the gate was interrupted (%s)" % self.interrupted
        return self._finish(res, directory, plan_only, mono)

    def _finish(self, res: GateResult, directory: str, plan_only: bool, mono: float) -> GateResult:
        res.duration_ns = int((time.monotonic() - mono) * duration.SECOND)
        if not res.incomplete and res.commit:
            # the gate judged the commit it started on, or nothing
            c = git.run(directory, "rev-parse", "--verify", "HEAD^{commit}", timeout=GIT_TIMEOUT)
            if c.exit_code != 0 or gostr.trim_space(c.stdout.decode("utf-8", "replace")) != res.commit:
                res.incomplete = "HEAD moved from %s while the gate ran: the result judges no one commit" % res.commit
        res.passed = not plan_only and not res.incomplete and len(res.commands) > 0 and not any(c.failed() for c in res.commands)
        took = dur(round_seconds(res.duration_ns))
        if res.incomplete:
            self.say("INCOMPLETE - the gate could not be judged: %s (logs %s)" % (res.incomplete, res.log_dir))
        elif plan_only and res.plan is not None:
            self.say("PLAN - %d commands, plan digest %s (runner %s %s); none was run"
                     % (len(res.plan["commands"]), res.plan_digest, res.runner, res.runner_version))
        elif res.passed:
            self.say("PASS - %d commands passed in %s, every one inside %s (logs %s)" % (len(res.commands), took, res.image, res.log_dir))
        else:
            n = sum(1 for c in res.commands if c.failed() and not c.not_run)
            self.say("FAIL - %d of %d commands failed in %s (logs %s)" % (n, len(res.commands), took, res.log_dir))
        return res

    def _run(self, res: GateResult, directory: str, repo: str, log_dir: str, plan_only: bool) -> None:
        if self.interrupted:
            res.incomplete = "the gate was interrupted (%s)" % self.interrupted
            return
        try:
            res.commit = git.git(directory, "rev-parse", "--verify", "HEAD^{commit}", timeout=GIT_TIMEOUT).strip()
        except git.GitError as err:
            res.incomplete = "the clone has no HEAD commit: %s" % err
            return
        st = git.run(directory, "status", "--porcelain", timeout=GIT_TIMEOUT)
        if st.exit_code == 0:
            res.clean = gostr.trim_space(st.stdout.decode("utf-8", "replace")) == ""
        if not os.path.isfile(os.path.join(directory, GATE_ENGINE_REL)):
            res.incomplete = "the tree carries no scope engine %s: the gate's plan is that engine's" % GATE_ENGINE_REL
            return
        if not container.has_dockerfile_at(directory, "HEAD"):
            res.incomplete = ("HEAD carries no %s: the gate runs only inside that container (--isolation %s), never on"
                              " the host" % (container.DOCKERFILE_VERIFY, ISOLATION_CONTAINER))
            return
        o, why = self.open(directory, repo, log_dir)
        if o is None:
            res.incomplete = why
            return
        try:
            self._judge(res, o, plan_only)
        finally:
            o.box.remove()

    def _judge(self, res: GateResult, o: Opened, plan_only: bool) -> None:
        box, r = o.box, o.r
        res.image, res.built, res.container = box.report.image, box.report.built, box.report.name
        rep, ref, why = self.ask_engine(o, "HEAD", True)
        res.engine = ref
        if rep is None:
            res.incomplete = "the scope engine gave no plan: " + why
            return
        if rep.mode != TIER_FULL:
            res.incomplete = "the scope engine answered --full with mode %s" % gostr.quote(rep.mode)
            return
        for d in rep.diagnostics or []:
            self.say("engine: %s" % d)
        cmds, _ = choose(rep, TIER_FULL)
        if not cmds:
            res.incomplete = "the scope engine planned no command (stack %s, conf %s)" % (gostr.quote(rep.stack), rep.conf)
            return
        res.plan = plan_of(o.directory, box.report, cmds, self.timeout_of)
        res.plan_digest = plan_digest(res.plan)
        if plan_only:
            return
        self.say("plan: %s -> %d commands (plan digest %s), run in order inside the container" % (ref, len(cmds), res.plan_digest))

        def not_judged(when: str) -> str:
            # a command's status after any of these says nothing about the tree
            if self.interrupted:
                return "the gate was interrupted %s (%s)" % (when, self.interrupted)
            if box.is_removed():
                return "the gate removed its container (at its deadline or an interrupt) " + when
            if time.time() >= r.deadline:
                return "%s passed %s" % (self.deadline_note(), when)
            return ""

        stopped = False
        for i, c in enumerate(cmds):
            gc = GateCommand(name=c.name, phase=c.phase, cmd=c.cmd)
            if not stopped and not res.incomplete:
                why = not_judged("before %s could start" % c.name)
                if why:
                    res.incomplete = why
            if stopped or res.incomplete:
                gc.not_run = True
                res.commands.append(gc)
                continue
            at = "[%d/%d] %s" % (i + 1, len(cmds), c.name)
            self.say("%s (%s): %s" % (at, c.phase, c.cmd))
            try:
                self._command(res, o, c, gc, at, not_judged)
            except (KeyboardInterrupt, runner.Interrupted) as err:
                self.interrupted = interrupt_name(err)
                gc.exit = -1
                res.incomplete = "the gate was interrupted while %s ran (%s)" % (c.name, self.interrupted)
            res.commands.append(gc)
            if gc.failed() and not self.keep_going:
                stopped = True

    def _command(self, res: GateResult, o: Opened, c: EngineCommand, gc: GateCommand, at: str,
                 not_judged: Callable[[str], str]) -> None:
        box, r = o.box, o.r
        why = r.unmet(c.requires_env, c.requires_cmd, o.directory, box)
        if why:
            nj = not_judged("while probing %s's prerequisites" % c.name)
            if nj:
                gc.not_run, res.incomplete = True, nj
            else:
                gc.exit, gc.unmet = -1, why + " in the gate's container"
                self.say("%s FAIL - %s" % (at, gc.unmet))
            return
        lost = box.lost_reason()
        if lost:
            gc.not_run, res.incomplete = True, "the gate's container became unusable: " + lost
            return
        timeout = self.timeout_of(c)
        er = r.exec(o.log_dir, runner.ExecSpec(argv=runner.bash_argv(c.cmd), dir=o.directory, cwd=".",
                                               timeout=timeout / duration.SECOND, private_tmp=True), box)
        gc.exit, gc.timed_out, gc.log = er.exit, er.timed_out, er.log_path
        gc.duration_ns = er.evidence.duration_ms * duration.MILLISECOND if er.evidence is not None else 0
        nj = not_judged("while %s ran" % c.name)
        if er.not_started:
            gc.not_run, gc.log = True, ""
            res.incomplete = "%s passed before %s could start" % (self.deadline_note(), c.name)
        elif nj:
            res.incomplete = nj
        elif er.error:
            gc.not_run = True
            res.incomplete = "%s could not start in the gate's container: %s" % (c.name, er.error)
        elif box.lost_reason():
            res.incomplete = "the gate's container became unusable while %s ran: %s" % (c.name, box.lost_reason())
        elif er.deadline_hit:
            res.incomplete = "%s cut %s short" % (self.deadline_note(), c.name)
        elif er.exit == 0:
            self.say("%s PASS (%s)" % (at, dur(round_seconds(gc.duration_ns))))
        else:
            what = "exit %d" % er.exit
            if er.timed_out:
                what = "killed at its %s timeout" % dur(timeout)
            self.say("%s FAIL - %s (%s), log %s; its tail:" % (at, what, dur(round_seconds(gc.duration_ns)), er.log_path))
            for ln in read_lines(er.log_path)[-TAIL_LINES:]:
                self.say_line("    " + ln)


def interrupt_name(err: BaseException) -> str:
    if isinstance(err, runner.Interrupted):
        return "signal %d" % err.signum
    return "an interrupt"


# ---- the green tag ----------------------------------------------------------------

# A green ref on the remote: name -> (its object, the commit it peels to; "" = lightweight).
GreenRefs = Dict[str, Tuple[str, str]]


class GreenError(Exception):
    """Why a green tag could not be written."""


def ident_env(environ: Mapping[str, str]) -> Dict[str, str]:
    """The hardened git environment with the caller's identity variables
    kept: the tagger is the person (or the lane) running the gate."""
    e = git.env(environ)
    for k in ("GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"):
        if k in environ:
            e[k] = environ[k]
    return e


def committer_ident(directory: str, environ: Mapping[str, str]) -> str:
    """git's committer identity in the clone (raises GreenError)."""
    c = proc.run(git.argv("var", "GIT_COMMITTER_IDENT"), cwd=directory, env=ident_env(environ), timeout=GIT_TIMEOUT)
    if c.exit_code != 0:
        why = c.error or c.stderr.decode("utf-8", "replace").strip() or "exit %d" % c.exit_code
        raise GreenError("no committer identity for the tag (git config user.name / user.email): %s" % why)
    return gostr.trim_space(c.stdout.decode("utf-8", "replace"))


def remote_green_refs(g: Git, directory: str, remote: str) -> GreenRefs:
    """Every ref under refs/tags/green/ on the remote, by name (green/...)."""
    try:
        out = g.out(directory, "ls-remote", "--tags", remote, "refs/tags/green/*")
    except git.GitError as err:
        raise GreenError("cannot read the green tags of %s: %s" % (remote, err)) from None
    objs: Dict[str, str] = {}
    peeled: Dict[str, str] = {}
    for line in out.split("\n"):
        f = gostr.fields(line)
        if len(f) != 2:
            continue
        name = f[1][len("refs/tags/"):] if f[1].startswith("refs/tags/") else f[1]
        if name.endswith("^{}"):
            peeled[name[:-3]] = f[0]
        else:
            objs[name] = f[0]
    return {n: (objs.get(n, ""), peeled.get(n, "")) for n in set(objs) | set(peeled)}


def next_green_number(refs: GreenRefs, day: str) -> int:
    """One more than the highest n of every name green/<day>-<n> on the
    remote, whatever its shape (a lightweight tag still holds its name)."""
    hi = 0
    for name in refs:
        m = _GREEN_NAME.fullmatch(name)
        if m is not None and m.group(1) == day:
            hi = max(hi, int(m.group(2)))
    return hi + 1


def same_green(g: Git, directory: str, remote: str, refs: GreenRefs, rec: Mapping[str, str]) -> str:
    """The name of a green tag on the remote that points at rec's commit and
    records the same commit, gate id, runner, runner version and plan digest
    ("" = none)."""
    for name in sorted(refs):
        obj, peeled = refs[name]
        if peeled != rec["commit"] or not _GREEN_NAME.fullmatch(name):
            continue
        if not g.ok(directory, "cat-file", "-e", obj):
            if not g.ok(directory, "fetch", "--quiet", "--no-tags", remote, "refs/tags/" + name):
                continue
        c = g.run(directory, "cat-file", "tag", obj)
        if c.exit_code != 0:
            continue
        _, sep, body = c.stdout.decode("utf-8", "replace").partition("\n\n")
        if not sep:
            continue
        try:
            got = json.loads(body.strip())
        except ValueError:
            continue
        if isinstance(got, dict) and all(got.get(k) == rec[k] for k in ("commit", "gate_id", "runner", "runner_version", "plan_digest")):
            return name
    return ""


def gate_id(directory: str, commit: str, environ: Mapping[str, str]) -> str:
    """tools/gate-id of commit, run from the clone's tree (raises GreenError)."""
    c = proc.run(["sh", "tools/gate-id", commit], cwd=directory, env=dict(environ), timeout=GIT_TIMEOUT)
    if c.exit_code != 0 or c.error:
        why = c.error or "exit %d" % c.exit_code
        raise GreenError("tools/gate-id %s: %s: %s" % (commit, why, c.stderr.decode("utf-8", "replace").strip()))
    gid = gostr.trim_space(c.stdout.decode("utf-8", "replace"))
    if not _GATE_ID.fullmatch(gid):
        raise GreenError("tools/gate-id %s printed %s, not a gate id" % (commit, gostr.quote(gid)))
    return gid


def green_preflight(g: Git, directory: str, remote: str, tagger: str) -> str:
    """Why a green tag could not be written from the clone after a pass ("" =
    it can): the worktree clean (the gate judges the working tree, the tag
    names HEAD), a committer identity (unless the tagger is named) and a
    readable remote. Asked before the gate spends its time."""
    try:
        st = g.out(directory, "status", "--porcelain")
    except git.GitError as err:
        return str(err)
    if gostr.trim_space(st):
        return ("the worktree is not clean (git status --porcelain lists %d entries): the gate judges the working tree,"
                " a green tag names HEAD" % len(gostr.trim_space(st).split("\n")))
    if not gostr.trim_space(tagger):
        try:
            committer_ident(directory, g.environ)
        except GreenError as err:
            return str(err)
    try:
        remote_green_refs(g, directory, remote)
    except GreenError as err:
        return str(err)
    return ""


def mark_green(g: Git, directory: str, remote: str, summary: str, res: GateResult, tagger: str = "",
               now: Optional[datetime.datetime] = None) -> Tuple[str, bool]:
    """Write the green tag of res, a passing gate of this runner on the clone
    at directory: (its name, whether it was written now — False: the commit
    already carries a tag of the same record, answered instead). A result
    that is no pass of this runner, on a tree clean when it started, at the
    commit HEAD still is, is refused (GreenError)."""
    remote = remote or "origin"
    if not res.passed or res.incomplete:
        raise GreenError("the gate did not pass: only a passing gate is marked green")
    if res.runner != GATE_RUNNER or res.runner_version != GATE_RUNNER_VERSION:
        raise GreenError("the result is runner %s %s's, not %s %s's" % (gostr.quote(res.runner), gostr.quote(res.runner_version),
                                                                       GATE_RUNNER, GATE_RUNNER_VERSION))
    if len(res.plan_digest) != 64 or res.plan is None:
        raise GreenError("the result carries no plan digest")
    if not res.clean:
        raise GreenError("the worktree was not clean when the gate started: the run judged more than HEAD")
    try:
        head = g.head(directory)
    except git.GitError as err:
        raise GreenError(str(err)) from None
    if head != res.commit:
        raise GreenError("HEAD is %s, the gate judged %s" % (head, res.commit))
    gid = gate_id(directory, res.commit, g.environ)
    t = (now or datetime.datetime.now(datetime.timezone.utc)).astimezone(datetime.timezone.utc)
    if gostr.trim_space(tagger):
        ident = "%s %d +0000" % (gostr.trim_space(tagger), int(t.timestamp()))
    else:
        ident = committer_ident(directory, g.environ)
    line = "%s: %d commands passed in %s" % (GATE_RUNNER, len(res.commands), dur(round_seconds(res.duration_ns)))
    if gostr.trim_space(summary):
        line += "; " + gostr.trim_space(summary)
    rec = {"commit": res.commit, "gate_id": gid, "runner": res.runner, "runner_version": res.runner_version,
           "plan_digest": res.plan_digest, "verified_at": t.strftime("%Y-%m-%dT%H:%M:%SZ"), "summary": line}
    msg = json.dumps(rec, ensure_ascii=False, separators=(",", ":"))
    day = t.strftime("%Y%m%d")
    refs = remote_green_refs(g, directory, remote)
    have = same_green(g, directory, remote, refs, rec)
    if have:
        return have, False
    for _ in range(GREEN_PUSH_TRIES):
        name = "green/%s-%d" % (day, next_green_number(refs, day))
        body = "object %s\ntype commit\ntag %s\ntagger %s\n\n%s\n" % (res.commit, name, ident, msg)
        c = g.run(directory, "mktag", input=body.encode("utf-8"))
        if c.exit_code != 0:
            raise GreenError("git mktag refused the tag object for %s: %s" % (name, c.stderr.decode("utf-8", "replace").strip()))
        obj = gostr.trim_space(c.stdout.decode("utf-8", "replace"))
        push = g.run(directory, "push", "--quiet", remote, obj + ":refs/tags/" + name)
        if push.exit_code == 0:
            # the local ref too, when that name is free here
            g.run(directory, "update-ref", "refs/tags/" + name, obj, "")
            return name, True
        perr = push.error or push.stderr.decode("utf-8", "replace").strip() or "exit %d" % push.exit_code
        try:
            refs = remote_green_refs(g, directory, remote)
        except GreenError as err:
            raise GreenError("pushing %s to %s failed (%s) and the remote can no longer be read: %s" % (name, remote, perr, err)) from None
        if name not in refs:
            raise GreenError("pushing %s to %s failed: %s" % (name, remote, perr))
        # another writer took the name first: the next number
    raise GreenError("%d names in a row were taken by concurrent writers on %s; nothing pushed" % (GREEN_PUSH_TRIES, remote))


# ---- the triage: the gate's container held open -----------------------------------


@dataclass
class GateUnit:
    """One failing unit of a red gate command."""

    command: str
    kind: str
    name: str
    pkg: str = ""
    # file: the repository-relative test file when located; carries: its
    # opening carries: globs
    file: str = ""
    carries: List[str] = field(default_factory=list)
    # rerun: its targeted re-run ("" = none: note says why)
    rerun: str = ""
    note: str = ""
    log: str = ""
    # signature: its failure signature in the log ("" = none shown);
    # excerpt: the signature, else the log's tail
    signature: str = ""
    excerpt: str = ""
    # timeout: its command's timeout (nanoseconds; 0 = the gate's default)
    timeout: int = 0


@dataclass
class UnitProbe:
    """One targeted re-run of a unit at the clone's checkout."""

    # presence: whether the checkout's tree has the unit (present / absent /
    # unknown), presence_why why it is not present
    presence: str = ""
    presence_why: str = ""
    # passed: the run shows the unit passing; failed: it reports the unit
    # failing, signature its failure signature. Neither: the run did not get
    # as far as the unit (or it is absent)
    passed: bool = False
    failed: bool = False
    signature: str = ""
    log: str = ""

    def absent(self) -> bool:
        return self.presence == ABSENT


def gate_pre_existing(u: GateUnit, head: UnitProbe, base: UnitProbe) -> Tuple[bool, str]:
    """The pre-existing judgment of a red gate's unit, the verification's own
    rule (classify_base): it exists at the baseline, its isolated re-run at
    the red head fails with the signature it failed with in the gate, and
    its run at the baseline fails with that same signature. A unit the
    baseline does not have — a test the batch added — never is."""
    head_sig = head.signature if head.failed else ""
    obs = BaseObservation(exit=0 if base.passed else 1, found=base.failed or base.passed, failed=base.failed,
                          signature=base.signature)
    return classify_base(u.signature, head_sig, base.presence == PRESENT, base.presence_why, [obs], 1)


def excerpt_of(u: Unit, lines: Sequence[str]) -> str:
    """A unit's failure signature, else the log's last lines."""
    if gostr.trim_space(u.signature):
        return cap_text(u.signature)
    return cap_text("\n".join(lines[-TAIL_LINES:]))


class SessionLike(Protocol):
    """What the triage needs of an open gate container (Session, or a test's fake)."""

    def units(self, res: GateResult) -> List[GateUnit]: ...

    def probe(self, u: GateUnit) -> UnitProbe: ...

    def select(self, base: str) -> EngineReport: ...


class Session:
    """A gate's container held open on one clone for the triage: the red
    gate's failing units located in their logs (the conf's rerun= template
    first), probed one at a time at whatever commit the clone is checked out
    at, and the engine's scoped selection for a diff. A session never passes
    a gate."""

    def __init__(self, gate: Gate, o: Opened, repo: str, cmds: Dict[str, EngineCommand]) -> None:
        self.gate = gate
        self.o = o
        self.repo = repo
        self.cmds = cmds

    @classmethod
    def open(cls, gate: Gate, directory: str, repo: str, log_dir: str) -> "Session":
        """The gate's container on the clone and its full plan (the re-run
        templates); raises TriageError naming why it could not be had."""
        directory = os.path.abspath(directory)
        os.makedirs(log_dir, exist_ok=True)
        if not os.path.isfile(os.path.join(directory, GATE_ENGINE_REL)):
            raise TriageError("the tree carries no scope engine %s" % GATE_ENGINE_REL)
        if not container.has_dockerfile_at(directory, "HEAD"):
            raise TriageError("HEAD carries no %s: the gate runs only inside that container" % container.DOCKERFILE_VERIFY)
        o, why = gate.open(directory, repo, log_dir)
        if o is None:
            raise TriageError(why)
        cmds: Dict[str, EngineCommand] = {}
        try:
            rep, _, why = gate.ask_engine(o, "HEAD", True)
        except BaseException:
            o.box.remove()
            raise
        if rep is not None:
            for c in choose(rep, TIER_FULL)[0]:
                cmds[c.name] = c
        else:
            gate.say("the engine gave no plan (%s): no re-run template applies" % why)
        return cls(gate, o, repo, cmds)

    def close(self) -> None:
        self.o.box.remove()

    def units(self, res: GateResult) -> List[GateUnit]:
        """The failing units of res's failed commands, each located in its
        log with its targeted re-run; a failed command whose output locates
        none is one unit of kind command, re-run whole. A command that never
        ran, or failed on an unmet prerequisite, has none."""
        out: List[GateUnit] = []
        d = self.o.directory
        for gc in res.commands:
            if gc.not_run or gc.unmet or gc.exit == 0:
                continue
            ec = self.cmds.get(gc.name) or EngineCommand(name="", phase="", cmd="")
            timeout = self.gate.timeout_of(ec)
            cs = CommandSpec(cmd=gc.cmd, timeout=timeout, rerun_tmpl=ec.rerun)
            lines = normalize_output(output_lines(gc.log), d, "")
            words, _ = shell_words(gc.cmd)
            race = race_seen(lines)
            failed: List[UnitState] = []
            for u in parse_units(lines, failfast=has_go_test(words) and has_flag(words, "-failfast")):
                if not u.failed:
                    continue
                us = UnitState(unit=u)
                locate(d, cs, us, race)
                failed.append(us)
            if not failed:
                failed.append(UnitState(unit=Unit(kind=KIND_COMMAND, name=self.repo + ":" + gc.name, failed=True,
                                                  marker="exit %d" % gc.exit), rerun=gc.cmd))
            for us in failed:
                gu = GateUnit(command=gc.name, kind=us.kind, name=us.name, pkg=us.unit.pkg, file=us.file, rerun=us.rerun,
                              note=us.note, log=gc.log, signature=us.unit.signature, excerpt=excerpt_of(us.unit, lines),
                              timeout=timeout)
                if gu.file:
                    gu.carries = file_carries(d, gu.file)
                out.append(gu)
        return out

    def probe(self, u: GateUnit) -> UnitProbe:
        """u's targeted re-run, once, in the container at the current
        checkout: its presence there, whether it passed (positive evidence),
        whether it was reported failing and with which signature. A unit
        proven absent is not run."""
        if gostr.trim_space(u.rerun) == "":
            raise TriageError("%s has no targeted re-run: %s" % (u.name, or_text(u.note, "none could be derived")))
        o = self.o
        p = UnitProbe()
        p.presence, p.presence_why = unit_presence(o.directory, "HEAD", u.kind, u.name, u.file)
        if p.presence == ABSENT:
            return p
        lost = o.box.lost_reason()
        if lost:
            raise TriageError("the gate's container became unusable: %s" % lost)
        to = u.timeout if u.timeout > 0 else self.gate.command_timeout
        er = o.r.exec(o.log_dir, runner.ExecSpec(argv=runner.bash_argv(u.rerun), dir=o.directory, cwd=".",
                                                 timeout=to / duration.SECOND, private_tmp=True), o.box)
        p.log = er.log_path
        if er.not_started:
            raise TriageError("the session's deadline passed before the re-run of %s could start" % u.name)
        if er.error:
            raise TriageError("the re-run of %s could not start: %s" % (u.name, er.error))
        if u.kind == KIND_COMMAND:
            p.passed, p.failed = er.exit == 0, er.exit != 0
            return p
        lines = normalize_output(output_lines(er.log_path), o.directory, er.tmp_dir)
        us = parse_units(lines)
        p.passed = unit_passed(u.kind, u.name, er.exit, us, lines)
        got = find_unit(us, u.kind, u.name)
        if got is not None and got.failed:
            p.failed, p.signature = True, got.signature
        return p

    def select(self, base: str) -> EngineReport:
        """The engine's scoped report for base..HEAD of the current checkout."""
        rep, _, why = self.gate.ask_engine(self.o, base, False)
        if rep is None:
            raise TriageError(why)
        return rep


# ---- the triage: attribution -------------------------------------------------------


@dataclass
class Selection:
    """What the engine planned for one change's diff."""

    full: bool = False
    commands: List[str] = field(default_factory=list)
    suites: List[str] = field(default_factory=list)


@dataclass
class Change:
    """One landed item's merge on the default branch."""

    item: str
    slug: str
    repo: str
    merge: str
    paths: List[str]
    selection: Optional[Selection] = None


@dataclass
class Candidate:
    """One item a unit may belong to: every merge of it whose change's area
    holds the unit (in change order, each with why), why the first one's."""

    item: str
    why: str
    within: List[Tuple[str, str]] = field(default_factory=list)

    def why_at(self, merge: str) -> str:
        for m, why in self.within:
            if m == merge:
                return why
        return self.why + " (at another merge of it)"


def unit_dir(kind: str, name: str, pkg: str, file: str) -> str:
    """A Go unit's repository-relative package directory ("" = none known)."""
    if kind == KIND_GO and file:
        return gostr.path_dir(gostr.path_clean(file))
    if kind == KIND_GO_PKG:
        return pkg_dir(name)
    if pkg:
        return pkg_dir(pkg)
    return ""


def pkg_dir(p: str) -> str:
    """A Go import path as a repository-relative directory: a path whose first
    element holds a dot is a module path, its tail after three elements."""
    p = gostr.trim_space(p)
    if p.startswith("./"):
        p = p[2:]
    if p in ("", "."):
        return ""
    parts = p.split("/")
    if "." in parts[0] and len(parts) > 3:
        return "/".join(parts[3:])
    return p


def names_package(cmd: str, d: str) -> bool:
    """Whether a planned command names the package dir: a ./<dir> word, a
    ./<dir>/... pattern, or a ./<prefix>/... pattern covering it."""
    for w in gostr.fields(cmd):
        w = w.strip("\"'")
        if not w.startswith("./"):
            continue
        w = w[2:]
        if w.endswith("/..."):
            prefix = w[:-4]
            if prefix in ("", ".") or d == prefix or d.startswith(prefix + "/"):
                return True
            continue
        if w == "...":
            return True
        if w.rstrip("/") == d:
            return True
    return False


def in_change(u: GateUnit, c: Change) -> str:
    """Why u lies in c's area ("" = it does not)."""
    f = gostr.path_clean(u.file) if u.file else ""
    for p in c.paths:
        if f and gostr.path_clean(p) == f:
            return "its file %s is touched by the change" % f
    for gl in u.carries:
        for p in c.paths:
            if glob_match(gl, p):
                return "its carries: glob %s matches the touched path %s" % (gl, p)
    d = unit_dir(u.kind, u.name, u.pkg, u.file)
    if d:
        for p in c.paths:
            if gostr.path_dir(gostr.path_clean(p)) == d:
                return "its package %s holds the touched file %s" % (d, p)
    s = c.selection
    if s is not None:
        if s.full:
            return "the scope engine planned the full tier for the change"
        if d:
            for cmd in s.commands:
                if names_package(cmd, d):
                    return "the scope engine selects its package %s for the change (%s)" % (d, clip(cmd, 160))
        if f:
            for suite in s.suites:
                if gostr.path_clean(suite) == f:
                    return "the scope engine carries its suite %s for the change" % f
            for cmd in s.commands:
                if f in cmd:
                    return "the scope engine runs its suite %s for the change (%s)" % (f, clip(cmd, 160))
    return ""


def candidates(u: GateUnit, repo: str, changes: Sequence[Change]) -> List[Candidate]:
    """The items whose changes the unit lies in (one per item, in change
    order, naming each of its merges the unit lies in)."""
    out: List[Candidate] = []
    at: Dict[str, int] = {}
    for c in changes:
        if c.repo != repo:
            continue
        why = in_change(u, c)
        if not why:
            continue
        if c.item not in at:
            at[c.item] = len(out)
            out.append(Candidate(item=c.item, why=why))
        out[at[c.item]].within.append((c.merge, why))
    return out


def bisect(base: str, commits: Sequence[str], fails: Callable[[str], bool]) -> int:
    """The first of commits — the first-parent history after base, oldest
    first, the last one the red head — at which fails reports the unit
    failing, by binary search. Both ends are tested: base failing answers
    -1 (the failure predates the window); the red head not failing raises
    TriageError (the failure does not reproduce)."""
    if not commits:
        raise TriageError("no commits to bisect")
    if fails(base):
        return -1
    last = len(commits) - 1
    if not fails(commits[last]):
        raise TriageError("the unit does not fail at the red head %s: its failure does not reproduce" % commits[last])
    lo, hi = -1, last
    while hi - lo > 1:
        mid = (lo + hi) // 2
        if fails(commits[mid]):
            hi = mid
        else:
            lo = mid
    return hi


def merge_commits_of(integration: Mapping[str, Any]) -> Dict[str, List[str]]:
    """A hand-off's merge_commits (repo -> its merges, oldest first); any
    other shape names none."""
    mc = integration.get("merge_commits")
    out: Dict[str, List[str]] = {}
    if not isinstance(mc, dict):
        return out
    for repo, cs in mc.items():
        if not isinstance(cs, list):
            return {}
        for c in cs:
            if not isinstance(c, str):
                return {}
            if repo and c:
                out.setdefault(repo, []).append(c)
    return out


def strings_of(v: Any) -> List[str]:
    return [s for s in v if isinstance(s, str) and s] if isinstance(v, list) else []


def integration_of(item: Mapping[str, Any]) -> Dict[str, Any]:
    v = item.get("integration")
    return dict(v) if isinstance(v, dict) else {}


def root_of(item: str, items: Sequence[Mapping[str, Any]]) -> str:
    """The source item a change belongs to: a fix ticket's source, else the item."""
    slug = ""
    for it in items:
        if it.get("id") == item:
            slug = str(it.get("slug") or "")
    for it in items:
        for f in strings_of(integration_of(it).get("fix_tickets")):
            if f == item or (slug and f == slug):
                return str(it.get("id") or "")
    return item


@dataclass
class TriagedUnit:
    """One failing unit of a red gate and its verdict."""

    repo: str
    unit: GateUnit
    verdict: str = VERDICT_RED
    item: str = ""
    root: str = ""
    why: str = ""
    # base: the baseline's verdict when it was not pre-existing; outside:
    # the first red commit the bisection found that is no item's merge
    base: str = ""
    outside: str = ""

    def klass(self) -> str:
        if self.verdict == VERDICT_FLAKY:
            return CLASS_FLAKY
        if self.verdict == VERDICT_PRE_EXISTING:
            return CLASS_PRE_EXISTING
        if self.item:
            return CLASS_ATTRIBUTED
        return CLASS_UNATTRIBUTED

    def as_dict(self) -> Dict[str, Any]:
        u = self.unit
        d: Dict[str, Any] = {"repo": self.repo, "command": u.command, "unit": u.name, "kind": u.kind}
        for k, v in (("file", u.file),):
            if v:
                d[k] = v
        d["class"] = self.klass()
        for k, v in (("item", self.item), ("root", self.root), ("why", self.why), ("base", self.base),
                     ("outside", self.outside), ("rerun", u.rerun), ("log", u.log), ("signature", u.signature),
                     ("excerpt", u.excerpt)):
            if v:
                d[k] = v
        return d


def all_flaky(units: Sequence[TriagedUnit]) -> bool:
    """Whether every unit re-ran green (none = False): the gate runs once more."""
    return bool(units) and all(u.verdict == VERDICT_FLAKY for u in units)


@dataclass
class RedRepo:
    """One repo whose gate failed, as the triage takes it."""

    repo: str
    dir: str
    head: str
    result: GateResult
    # batch_merges: the batch items' merges in the repo (the baseline, when
    # no green tag is merged into head, is the parent of the oldest)
    batch_merges: List[str]
    # items: the landed pool's items (attribution, the fix tickets' sources)
    items: List[Dict[str, Any]]


def triage_baseline(g: Git, directory: str, head: str, batch_merges: Sequence[str]) -> Tuple[str, str]:
    """A repo's triage baseline (rev, name): the newest green tag merged into
    the head, else the first parent of the batch's oldest merge in the repo;
    ("", "") = neither (the recent history is bisected)."""
    t = newest_green(g, directory, head)
    if t:
        return "refs/tags/" + t, t
    first = ""
    for c in batch_merges:
        if first == "" or g.is_ancestor(directory, c, first):
            first = c
    if first == "":
        return "", ""
    try:
        parent = g.rev(directory, first + "^1")
    except git.GitError:
        return "", ""
    if not parent:
        return "", ""
    return parent, "(the parent %s of the batch's first merge %s)" % (short(parent), short(first))


def changes_in(g: Git, sess: SessionLike, repo: str, directory: str, window: Sequence[str],
               items: Sequence[Mapping[str, Any]]) -> List[Change]:
    """The landed items' merges among window, each with its touched paths and
    the engine's selection for its diff (every merge of an item that merged
    the repo more than once), in window order."""
    pos = {c: i for i, c in enumerate(window)}
    out: List[Change] = []
    for it in items:
        integ = integration_of(it)
        if integ.get("state") != "landed":
            continue
        for c in merge_commits_of(integ).get(repo, []):
            if c not in pos:
                continue
            ch = Change(item=str(it.get("id") or ""), slug=str(it.get("slug") or ""), repo=repo, merge=c,
                        paths=g.changed_paths(directory, c))
            try:
                g.checkout(directory, c)
                rep = sess.select(c + "^1")
            except (git.GitError, TriageError):
                pass
            else:
                sel = Selection(full=rep.mode == TIER_FULL, commands=[x.cmd for x in rep.commands])
                if rep.e2e_selection is not None:
                    sel.suites = list(rep.e2e_selection.get("suites") or [])
                ch.selection = sel
            out.append(ch)
    out.sort(key=lambda ch: pos[ch.merge])
    return out


def triage(g: Git, sess: SessionLike, red: RedRepo) -> Tuple[List[TriagedUnit], str]:
    """One repo's red gate triaged: the units with their verdicts, and the
    baseline's name ("" = none: the recent history was bisected). The clone
    is checked out at whatever commit each probe needs and left at the red
    head."""
    repo, d = red.repo, red.dir
    try:
        return _triage(g, sess, red, repo, d)
    finally:
        try:
            g.checkout(d, red.head)
        except git.GitError:
            pass


def _triage(g: Git, sess: SessionLike, red: RedRepo, repo: str, d: str) -> Tuple[List[TriagedUnit], str]:
    g.checkout(d, red.head)
    out: List[TriagedUnit] = []
    heads: List[UnitProbe] = []
    for u in sess.units(red.result):
        t = TriagedUnit(repo=repo, unit=u)
        hp = UnitProbe()
        if u.rerun:
            try:
                hp = sess.probe(u)
            except TriageError as err:
                t.why = "its re-run at the red head could not be judged: %s" % err
            else:
                if hp.passed:
                    t.verdict, t.why = VERDICT_FLAKY, "its re-run passed at the same commit"
        else:
            t.why = or_text(u.note, "no targeted re-run")
        out.append(t)
        heads.append(hp)
    baseline, base_name = triage_baseline(g, d, red.head, red.batch_merges)
    if baseline:
        g.checkout(d, baseline)
        for i, t in enumerate(out):
            if t.verdict != VERDICT_RED or not t.unit.rerun:
                continue
            try:
                bp = sess.probe(t.unit)
            except TriageError as err:
                t.base = "its run at the baseline %s could not be judged: %s" % (base_name, err)
                continue
            pre, why = gate_pre_existing(t.unit, heads[i], bp)
            if pre:
                t.verdict, t.why = VERDICT_PRE_EXISTING, "at the baseline %s: %s" % (base_name, why)
            else:
                t.base = "at the baseline %s: %s" % (base_name, why)
    window = g.first_parents(d, baseline, red.head)  # no baseline: the recent history
    changes = changes_in(g, sess, repo, d, window, red.items)
    g.checkout(d, red.head)
    by_merge = {c.merge: c for c in changes}
    for t in out:
        if t.verdict != VERDICT_RED:
            continue
        _attribute(g, sess, red, t, changes, by_merge, window, baseline, base_name)
        if t.item:
            t.root = root_of(t.item, red.items)
    return out, base_name


def _attribute(g: Git, sess: SessionLike, red: RedRepo, t: TriagedUnit, changes: Sequence[Change],
               by_merge: Dict[str, Change], window: Sequence[str], baseline: str, base_name: str) -> None:
    d = red.dir
    cands = candidates(t.unit, red.repo, changes)

    def red_at(c: str) -> bool:
        # whether the unit fails at commit c (a unit the commit does not
        # have is not failing there)
        try:
            g.checkout(d, c)
        except git.GitError as err:
            raise TriageError(str(err)) from None
        p = sess.probe(t.unit)
        return not p.absent() and not p.passed

    # a single candidate by area is blamed only on the history's say: red
    # at a merge of it and not at that merge's first parent (an area match
    # alone blames the wrong item when the break came from elsewhere) —
    # every merge of it in the window checked, oldest first
    single = ""
    if len(cands) == 1 and t.unit.rerun:
        c0 = cands[0]
        seen: List[str] = []
        for ch in changes:
            if ch.item != c0.item:
                continue
            errs = []
            at_merge = at_parent = False
            try:
                at_merge = red_at(ch.merge)
            except TriageError as err:
                errs.append(str(err))
            try:
                at_parent = red_at(ch.merge + "^1")
            except TriageError as err:
                errs.append(str(err))
            if errs:
                single = "its only candidate by area, %s, could not be checked at its merge %s: %s" % (
                    or_id(ch.slug, ch.item), short(ch.merge), "\n".join(errs))
                break
            if at_merge and not at_parent:
                t.item = ch.item
                t.why = c0.why_at(ch.merge) + "; red at its merge %s and not at its first parent" % short(ch.merge)
                break
            seen.append("red at its merge %s: %s, at its first parent: %s" % (short(ch.merge), go_bool(at_merge), go_bool(at_parent)))
        if not t.item and not single:
            first = by_merge[c0.within[0][0]]
            single = "its only candidate by area, %s, is not where it turned red (%s)" % (or_id(first.slug, c0.item), "; ".join(seen))
    if t.item:
        return
    if t.unit.rerun and baseline and window:
        try:
            idx = bisect(baseline, window, red_at)
        except TriageError as err:
            t.why = "the bisection could not run: %s" % err
        else:
            if idx < 0:
                t.why = ("red at the baseline %s already (bisection), yet not pre-existing (%s): no item of the window is"
                         " to blame" % (base_name, or_text(t.base, "no proof")))
            else:
                c = window[idx]
                hit = by_merge.get(c)
                if hit is not None:
                    t.item = hit.item
                    t.why = "bisection: first red at the merge %s of %s (candidates by area: %d)" % (short(c), or_id(hit.slug, hit.item), len(cands))
                else:
                    t.outside = c
                    lg = g.run(d, "log", "-1", "--format=%s", c)
                    subj = gostr.trim_space(lg.stdout.decode("utf-8", "replace")) if lg.exit_code == 0 else ""
                    t.why = "bisection: first red at %s (%s), which is no landed item's merge: outside the batch" % (short(c), clip_runes(subj, 120))
        if single:
            t.why = single + "; " + t.why
    elif len(changes) == 1 and len(window) == 1 and window[0] == changes[0].merge:
        t.item = changes[0].item
        t.why = "the only commit on the default branch since the baseline %s (no targeted re-run to bisect with)" % base_name
    else:
        t.why = "%d candidate(s) by area and no bisection (%s)" % (len(cands), or_text(single, or_text(t.why, "no targeted re-run")))


def go_bool(b: bool) -> str:
    return "true" if b else "false"


# ---- the pool ----------------------------------------------------------------------

# The landed items of a project the pool lists (integrate.py's ITEMS_LIMIT).
POOL_ITEMS_LIMIT = 1000


def read_pool_file(path: str) -> List[Dict[str, Any]]:
    """A pool snapshot's landed items (pool.json: {items: [{id, slug,
    integration, …}], …})."""
    try:
        with open(path, "rb") as f:
            snap = json.loads(f.read().decode("utf-8"))
    except (OSError, ValueError) as err:
        raise TriageError("the pool snapshot %s: %s" % (path, err)) from None
    if not isinstance(snap, dict) or not isinstance(snap.get("items") or [], list):
        raise TriageError("the pool snapshot %s: not a pool snapshot" % path)
    return [dict(it) for it in snap.get("items") or [] if isinstance(it, dict)]


def hub_landed_items(environ: Mapping[str, str]) -> List[Dict[str, Any]]:
    """The project's landed work items read now through the step's hub —
    ``pf --json step hub list --status wrapped --attr integration.state=landed`` (the
    daemon scopes it to the step's project and its hub whitelist; the
    script holds no credential) — as pool items {id, slug, landed,
    integration}. A refusal raises TriageError naming it."""
    args = ["list", "--status", "wrapped", "--attr", "integration.state=landed", "--limit", str(POOL_ITEMS_LIMIT)]
    try:
        raw = hub.run(args, environ)
    except (hub.HubError, RuntimeError) as err:
        raise TriageError("reading the integration pool through the step's hub: %s" % err) from None
    try:
        page = json.loads(raw.decode("utf-8"))
    except ValueError as err:
        raise TriageError("pf step hub list: the answer is not a work item list: %s" % err) from None
    if not isinstance(page, dict) or not isinstance(page.get("items") or [], list):
        raise TriageError("pf step hub list: the answer is not a work item list")
    out: List[Dict[str, Any]] = []
    for wi in page.get("items") or []:
        if not isinstance(wi, dict):
            continue
        attrs = wi.get("attrs") if isinstance(wi.get("attrs"), dict) else {}
        assert isinstance(attrs, dict)
        integ = attrs.get("integration") if isinstance(attrs.get("integration"), dict) else {}
        out.append({"id": wi.get("id") or "", "slug": wi.get("slug") or "", "landed": wi.get("closed_at") or wi.get("updated_at") or "",
                    "integration": integ})
    return out


# ---- the command ------------------------------------------------------------------


def read_json(path: str) -> Any:
    with open(path, "rb") as f:
        return json.loads(f.read().decode("utf-8"))


@dataclass
class Pins:
    heads: Dict[str, str]
    gated: List[str]


def read_pins(path: str) -> Pins:
    """--pins: a step result file — its own `heads` and its details' `gated`
    — or a bare {heads, gated} object; every gated repo needs a head
    (raises ValueError)."""
    try:
        doc = read_json(path)
    except OSError as err:
        raise ValueError(str(err)) from None
    except ValueError as err:
        raise ValueError("the pins do not decode: %s" % err) from None
    if not isinstance(doc, dict):
        raise ValueError("the pins do not decode: not a JSON object")
    heads = doc.get("heads")
    gated = doc.get("gated")
    if doc.get("schema") == result.SCHEMA:
        det = doc.get("details")
        gated = det.get("gated") if isinstance(det, dict) else None
    if heads is None:
        heads = {}
    if not isinstance(heads, dict) or not all(isinstance(v, str) for v in heads.values()):
        raise ValueError("the pins do not decode: heads is not an object of commits")
    if gated is None:
        raise ValueError("the pins name no `gated` list (the repos the gate judges; empty = nothing to gate)")
    if not isinstance(gated, list) or not all(isinstance(r, str) for r in gated):
        raise ValueError("the pins do not decode: gated is not a list of repos")
    for repo in gated:
        if gostr.trim_space(heads.get(repo, "")) == "":
            raise ValueError("the gated repo %s has no head in `heads`" % repo)
    return Pins(heads=dict(heads), gated=list(gated))


def read_step_details(path: str, command: str = "") -> Tuple[Dict[str, Any], bytes]:
    """A step result file's details (and the file's bytes); command, when
    given, is the command it must be of (raises ValueError)."""
    try:
        with open(path, "rb") as f:
            raw = f.read()
    except OSError as err:
        raise ValueError(str(err)) from None
    try:
        doc = json.loads(raw.decode("utf-8"))
    except ValueError as err:
        raise ValueError("not JSON: %s" % err) from None
    if not isinstance(doc, dict) or doc.get("schema") != result.SCHEMA or (command and doc.get("command") != command):
        got = doc if isinstance(doc, dict) else {}
        what = command or "step"
        raise ValueError("not %s %s result file (schema %s, command %s)" % ("an" if what[0] in "aeiou" else "a", what,
                                                                          gostr.quote(str(got.get("schema", ""))),
                                                                          gostr.quote(str(got.get("command", "")))))
    det = doc.get("details")
    if not isinstance(det, dict):
        raise ValueError("its details do not decode: not an object")
    return det, raw


def parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="gate.py", description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter,
                                allow_abbrev=False)
    p.add_argument("--isolation", default="", help="where the gate runs: container, the only isolation a gate has (required)")
    p.add_argument("--keep-going", action="store_true", help="run every command after a failing one instead of stopping at the first")
    p.add_argument("--timeout", default="", help="a command's timeout when the plan declares none, a Go duration (default 20m)")
    p.add_argument("--deadline", default="", help="the whole run's deadline, a Go duration (default: derived from the step's tool"
                   " window, two minutes under it; outside a step 3h)")
    p.add_argument("--cpus", default="", help="the container's cpu limit (default min(8, this machine's cores))")
    p.add_argument("--memory", default="", help="the container's memory limit, docker syntax (default 16g; at least 6m)")
    p.add_argument("--pids", default="", help="the container's pids limit (default 4096)")
    p.add_argument("--log-dir", default="", help="where the image build log and one log per command go (default: a fresh temp dir,"
                   " kept; with --capsule: logs/ beside --out, else the capsule's runtime-state/gate/<n>)")
    p.add_argument("--repo", default="", help="the name the image and the cache volume are tagged with (default: the clone"
                   " directory's name)")
    p.add_argument("--json", action="store_true", help="print the result as one JSON object on stdout (progress goes to stderr)")
    p.add_argument("--out", default="", help="write the result file there (atomically)")
    p.add_argument("--mark-green", action="store_true", help="after a pass, push the green tag of each judged head to --remote"
                   " (needs --summary, a clean tree and a committer identity)")
    p.add_argument("--summary", default=None, help="with --mark-green: a line on the run for the tag's record (what ran it, why)")
    p.add_argument("--remote", default=None, help="with --mark-green: the remote the green tag is pushed to (default origin)")
    p.add_argument("--plan-only", action="store_true", help="run no command: print the plan the gate would run, its digest and"
                   " the runner identity as JSON")
    p.add_argument("--capsule", default="", help="the batch capsule root (repos/<name> clones): gate the pinned heads of --pins")
    p.add_argument("--pins", default="", help="with --capsule: the pins (a result file: its heads and its details' gated; or"
                   " {heads, gated})")
    p.add_argument("--triage", action="store_true", help="with --capsule: triage the red gate of --gate instead of running one")
    p.add_argument("--gate", default="", help="with --triage: the red gate's result file (this script's, the same pins)")
    p.add_argument("--batch", default="", help="with --triage: the batch (collect's result file)")
    p.add_argument("--pool", default="", help="with --triage: a pool snapshot file (default: the pool read now through the step's"
                   " hub, pf step hub list)")
    return p


@dataclass
class Invocation:
    """One run's outputs: the result file's content and how it is printed."""

    res: result.Result
    stdout: Any
    stderr: Any
    as_json: bool = False
    # quiet: the clone mode wrote its own stdout: no summary line after it
    quiet: bool = False


def parse_limits(a: argparse.Namespace) -> Tuple[float, str, int]:
    cpus = 0.0
    if a.cpus != "":
        try:
            cpus = float(a.cpus)
        except ValueError:
            raise Usage("--cpus %s: not a number" % gostr.quote(a.cpus)) from None
        if cpus != cpus or cpus in (float("inf"), float("-inf")):
            raise Usage("--cpus %s: not a finite number" % gostr.quote(a.cpus))
    memory = ""
    if a.memory != "":
        why = check_memory(a.memory)
        if why:
            raise Usage("--memory: %s" % why)
        memory = gostr.trim_space(a.memory).lower()
    pids = 0
    if a.pids != "":
        try:
            pids = int(a.pids)
        except ValueError:
            raise Usage("--pids %s: not an integer" % gostr.quote(a.pids)) from None
    return cpus, memory, pids


def parse_duration_flag(name: str, v: str) -> int:
    if v == "":
        return 0
    try:
        return duration.parse(v)
    except ValueError as err:
        raise Usage("%s: %s" % (name, err)) from None


def check_invocation(a: argparse.Namespace) -> None:
    if a.isolation == "":
        raise Usage("--isolation container is required: the gate runs only inside the repository's verification container")
    if a.isolation != ISOLATION_CONTAINER:
        raise Usage("--isolation %s: the gate runs only inside the repository's verification container (--isolation %s), never"
                    " on the host" % (gostr.quote(a.isolation), ISOLATION_CONTAINER))
    if a.plan_only and (a.mark_green or a.json or a.out or a.capsule or a.triage):
        raise Usage("--plan-only prints the plan alone (a plan is never a result): it takes no --json, --out, --mark-green,"
                    " --capsule or --triage")
    if a.mark_green and not a.summary:
        raise Usage("--mark-green needs --summary <text> (what ran the gate, for the tag's record)")
    if not a.mark_green and a.summary:
        raise Usage("--summary is the green tag's: it needs --mark-green")
    if a.remote is not None and not a.mark_green:
        raise Usage("--remote is where the green tag goes: it needs --mark-green")
    if not a.capsule and (a.pins or a.triage):
        raise Usage("--pins and --triage gate a batch capsule's clones: they need --capsule <dir>")
    if a.capsule and not a.pins:
        raise Usage("--capsule needs --pins <file> (the heads to gate and the repos to judge)")
    if a.capsule and a.repo:
        raise Usage("--repo names a lone clone's image: with --capsule each repo is named by its pins")
    if a.triage and (not a.gate or not a.batch):
        raise Usage("--triage needs --gate <file> (the red gate's result) and --batch <file> (collect's result)")
    if not a.triage and (a.gate or a.batch or a.pool):
        raise Usage("--gate, --batch and --pool are the triage's inputs: they need --triage")


def main(
    argv: Optional[List[str]] = None,
    environ: Optional[Dict[str, str]] = None,
    runtime: Optional[container.ContainerRuntime] = None,
    stdout: Any = None,
    stderr: Any = None,
) -> int:
    """The command: argv (default sys.argv[1:]) under environ (default the
    process's); runtime replaces the docker CLI (a test's fake engine)."""
    args_list = list(sys.argv[1:] if argv is None else argv)
    env = dict(os.environ if environ is None else environ)
    out = sys.stdout if stdout is None else stdout
    err = sys.stderr if stderr is None else stderr
    inv = Invocation(res=result.begin(COMMAND, args_list, VERSION, env), stdout=out, stderr=err)
    try:
        a = parser().parse_args(args_list)
    except SystemExit as e:
        return int(e.code or 0)
    inv.as_json = bool(a.json)

    def say(msg: str) -> None:
        print("%s: %s" % (COMMAND, msg), file=err)

    try:
        code = run(a, env, inv, runtime)
    except Usage as e:
        say(str(e))
        inv.res.summary = str(e)
        code = 2
    except (KeyboardInterrupt, runner.Interrupted) as e:
        inv.res.summary = "not judged: the gate was interrupted (%s)" % interrupt_name(e)
        code = 2
    except Exception as e:  # a fault of the script is never a verdict
        traceback.print_exc(file=err)
        inv.res.summary = "the gate could not run: %s: %s" % (type(e).__name__, e)
        code = 2
    if a.plan_only:
        return code
    try:
        result.finish(inv.res, code, a.out or None)
    except OSError as e:
        say("the result could not be written to %s: %s" % (a.out, e))
        code = 2
        inv.res.exit_code = code
    if a.json:
        print(jsonout.line(inv.res.to_dict()), file=out)
    elif inv.res.summary and not inv.quiet:
        print("%s: %s (%s)" % (COMMAND, inv.res.summary, inv.res.verdict), file=out)
    return code


def run(a: argparse.Namespace, env: Dict[str, str], inv: Invocation, runtime: Optional[container.ContainerRuntime]) -> int:
    check_invocation(a)
    cpus, memory, pids = parse_limits(a)
    timeout = parse_duration_flag("--timeout", a.timeout)
    if timeout <= 0:
        timeout = DEFAULT_COMMAND_TIMEOUT
    window = parse_duration_flag("--deadline", a.deadline)
    if window <= 0:
        if env.get(stepenv.ENV_TOOL_WINDOW, "") or env.get(stepenv.ENV_BASH_MAX_TIMEOUT, ""):
            window = default_deadline_for(env)
        else:
            window = DEFAULT_GATE_DEADLINE
    progress = inv.stderr if (a.json or a.plan_only) else inv.stdout

    def say(msg: str) -> None:
        print(msg, file=progress, flush=True)

    until = time.time() + window / duration.SECOND
    # one option set for every container of the invocation: the gate's own
    # owner and a fresh boot (a kill -9's leftover ends at its keep-alive,
    # and the next gate's start sweeps the expired ones)
    co = container.for_run(container.ContainerOptions(
        runtime=runtime, owner=container.GATE_OWNER,
        wi=env.get(stepenv.ENV_WI_ID, "") or GATE_WI, cpus=cpus, memory=memory, pids=pids,
        say=lambda m: say("gate: " + m),
    ), until, env)
    gate = Gate(co, until, window, timeout, a.keep_going, dict(env), say)
    remote = a.remote or "origin"
    if not a.capsule:
        return clone_mode(a, env, inv, gate, remote)
    capsule = os.path.abspath(a.capsule)
    if not os.path.isdir(os.path.join(capsule, "repos")):
        raise Usage("--capsule %s: no repos/ directory (the batch capsule's clones)" % capsule)
    try:
        pins = read_pins(a.pins)
    except ValueError as e:
        raise Usage("--pins %s: %s" % (a.pins, e)) from None
    logs = a.log_dir or (os.path.join(os.path.dirname(a.out), "logs") if a.out else "")
    try:
        if logs:
            ensure_log_dir(logs)
        else:
            logs = next_run_dir(os.path.join(capsule, "runtime-state", "gate"))
    except OSError as e:
        raise Usage("the log directory %s: %s" % (logs, e)) from None
    logs = os.path.abspath(logs)
    g = Git(env)
    if a.triage:
        return triage_mode(a, env, inv, gate, g, capsule, pins, logs, remote)
    d, line, code = capsule_mode(gate, g, capsule, pins, a.pins, logs, a.mark_green, a.summary or "", remote, inv)
    inv.res.details, inv.res.summary = d, line
    inv.res.heads = {repo: rd["head"] for repo, rd in d["repos"].items()}
    return code


def clone_mode(a: argparse.Namespace, env: Dict[str, str], inv: Invocation, gate: Gate, remote: str) -> int:
    """The gate of the clone at the working directory (the release's and an
    operator's run)."""
    code = _clone_mode(a, env, inv, gate, remote)
    if inv.res.details:
        inv.res.details = gate_details(inv.res.details)
    return code


def _clone_mode(a: argparse.Namespace, env: Dict[str, str], inv: Invocation, gate: Gate, remote: str) -> int:
    inv.quiet = not a.json
    directory = os.getcwd()
    g = Git(env)
    if a.mark_green:
        # what the tag needs is checked before the gate spends its time
        why = green_preflight(g, directory, remote, "")
        if why:
            raise Usage("--mark-green: %s" % why)
    res = gate.run(directory, a.repo, a.log_dir, plan_only=a.plan_only)
    if a.plan_only:
        if res.incomplete or res.plan is None:
            return 2
        print(json.dumps({"commit": res.commit, "clean": res.clean, "runner": res.runner, "runner_version": res.runner_version,
                          "plan_digest": res.plan_digest, "plan": res.plan}, ensure_ascii=False, separators=(",", ":")),
              file=inv.stdout)
        return 0
    name = a.repo or os.path.basename(os.path.abspath(directory))
    rd: Dict[str, Any] = {"head": res.commit, "result": res.as_dict()}
    d: Dict[str, Any] = {"mode": "clone", "gated": [name], "repos": {name: rd}, "log_dir": res.log_dir}
    inv.res.details = d
    inv.res.heads = {name: res.commit} if res.commit else {}
    inv.res.logs = gate_logs(res)
    if res.incomplete:
        d["incomplete"] = {name: res.incomplete}
        inv.res.summary = "not judged: " + res.incomplete
        return 2
    if not res.passed:
        d["failed"] = [name]
        inv.res.summary = fail_summary(res)
        return 1
    inv.res.summary = "the gate passed: %d commands in %s" % (len(res.commands), dur(round_seconds(res.duration_ns)))
    if not a.mark_green:
        return 0
    try:
        tag, created = mark_green(g, directory, remote, a.summary or "", res)
    except GreenError as e:
        print("%s: the gate passed, but its green tag was not written: %s" % (COMMAND, e), file=inv.stderr)
        rd["tag_error"] = str(e)
        inv.res.summary += "; its green tag was not written: %s" % e
        return 3
    rd["tag"] = tag
    d["tags"] = {name: tag}
    say_tag(gate, a.repo, res, tag, created, remote)
    print(tag, file=inv.stderr if a.json else inv.stdout)
    inv.res.summary += "; green tag " + tag
    return 0


def say_tag(gate: Gate, repo: str, res: GateResult, tag: str, created: bool, remote: str) -> None:
    at = repo + " " if repo else ""
    if created:
        gate.say("%sgreen tag %s pushed to %s for %s (plan digest %s)" % (at, tag, remote, res.commit, res.plan_digest))
    else:
        gate.say("%s%s already carries the green tag %s on %s for this gate - not tagged again" % (at, res.commit, tag, remote))


def repo_detail(head: str, branch: str) -> Dict[str, Any]:
    return {"head": head, "branch": branch, "result": None}


GATE_KEYS = ("mode", "pins", "gated", "repos", "tags", "failed", "incomplete", "log_dir")
REPO_KEYS = ("head", "branch", "result", "tag", "tag_error")
TRIAGE_KEYS = ("pins", "gate", "repos", "baselines", "units", "classes", "all_flaky", "regate", "log_dir", "gate_run")


def ordered(d: Dict[str, Any], keys: Sequence[str]) -> Dict[str, Any]:
    """d with its keys in the result file's order (the Go record's)."""
    return {k: d[k] for k in keys if k in d}


def gate_details(d: Dict[str, Any]) -> Dict[str, Any]:
    """A gate's details in the result file's order."""
    out = ordered(d, GATE_KEYS)
    out["repos"] = {repo: ordered(rd, REPO_KEYS) for repo, rd in d["repos"].items()}
    return out


def capsule_mode(gate: Gate, g: Git, capsule: str, pins: Pins, pins_path: str, log_root: str, mark: bool, summary: str,
                 remote: str, inv: Invocation) -> Tuple[Dict[str, Any], str, int]:
    """Every gated repo's pinned head gated in the batch capsule's clones:
    the details, the summary line and the exit code (the invocation's logs
    appended)."""
    d, line, code = _capsule_mode(gate, g, capsule, pins, pins_path, log_root, mark, summary, remote, inv)
    return gate_details(d), line, code


def _capsule_mode(gate: Gate, g: Git, capsule: str, pins: Pins, pins_path: str, log_root: str, mark: bool, summary: str,
                  remote: str, inv: Invocation) -> Tuple[Dict[str, Any], str, int]:
    repos: Dict[str, Dict[str, Any]] = {}
    d: Dict[str, Any] = {"mode": "capsule", "pins": pins_path, "gated": list(pins.gated), "repos": repos, "log_dir": log_root}
    if not pins.gated:
        return d, "nothing gated: the pins name no repo to judge", 0
    # every clone fetched and checked out first: a clone or head that
    # cannot be had fails the invocation before any gate spends its time
    for repo in pins.gated:
        directory = os.path.join(capsule, "repos", repo)
        if not os.path.isdir(directory):
            return d, "%s: no clone at %s" % (repo, directory), 2
        head = pins.heads[repo]
        base = g.default_branch(directory)
        try:
            g.fetch(directory, base)
        except git.GitError as e:
            return d, "%s: fetching %s: %s" % (repo, base, e), 2
        try:
            g.checkout(directory, head)
        except git.GitError as e:
            return d, "%s: the pinned head %s: %s" % (repo, short(head), e), 2
        repos[repo] = repo_detail(head, base)
        if mark:
            why = green_preflight(g, directory, remote, LANE_TAGGER)
            if why:
                return d, "--mark-green: %s: %s" % (repo, why), 2
    results: Dict[str, GateResult] = {}
    incomplete: Dict[str, str] = {}
    failed: List[str] = []
    for repo in pins.gated:
        rd = repos[repo]
        directory = os.path.join(capsule, "repos", repo)
        gate.say("the full gate on %s %s (%s)" % (repo, rd["branch"], short(rd["head"])))
        res = gate.run(directory, repo, os.path.join(log_root, repo))
        results[repo] = res
        rd["result"] = res.as_dict()
        inv.res.logs.extend(gate_logs(res))
        if res.incomplete:
            incomplete[repo] = res.incomplete
        elif not res.passed:
            failed.append(repo)
    if failed:
        d["failed"] = failed
    if incomplete:
        d["incomplete"] = incomplete
        return d, "not judged: " + "; ".join("%s: %s" % (r, incomplete[r]) for r in sorted(incomplete)), 2
    if failed:
        return d, "the gate failed in " + "; ".join("%s: %s" % (r, fail_summary(results[r])) for r in failed), 1
    line = "the gate passed in " + ", ".join(pins.gated)
    if not mark:
        return d, line, 0
    tags: Dict[str, str] = {}
    d["tags"] = tags
    errors: List[str] = []
    for repo in pins.gated:
        rd = repos[repo]
        directory = os.path.join(capsule, "repos", repo)
        try:
            tag, created = mark_green(g, directory, remote, summary, results[repo], tagger=LANE_TAGGER)
        except GreenError as e:
            print("%s: %s: the gate passed, but its green tag was not written: %s" % (COMMAND, repo, e), file=inv.stderr)
            rd["tag_error"] = str(e)
            errors.append("%s: %s" % (repo, e))
            continue
        rd["tag"], tags[repo] = tag, tag
        say_tag(gate, repo, results[repo], tag, created, remote)
    if errors:
        return d, line + "; a green tag was not written: " + "; ".join(errors), 3
    return d, line + "; green tags " + ", ".join("%s %s" % (r, tags[r]) for r in pins.gated), 0


def triage_mode(a: argparse.Namespace, env: Dict[str, str], inv: Invocation, gate: Gate, g: Git, capsule: str, pins: Pins,
                log_root: str, remote: str) -> int:
    """The red repos of --gate triaged; when every failure was flaky, the
    same pins gated once more."""
    try:
        gd, gate_raw = read_step_details(a.gate, COMMAND)
    except ValueError as e:
        raise Usage("--gate %s: %s" % (a.gate, e)) from None
    try:
        bd, _ = read_step_details(a.batch, INTEGRATE_COMMAND)
    except ValueError as e:
        raise Usage("--batch %s: %s" % (a.batch, e)) from None
    if not isinstance(bd.get("items") or [], list):
        raise Usage("--batch %s: its details carry no items list (collect's result)" % a.batch)
    try:
        items = read_pool_file(a.pool) if a.pool else hub_landed_items(env)
    except TriageError as e:
        inv.res.summary = "not judged: %s" % e
        return 2
    td: Dict[str, Any] = {"pins": a.pins, "gate": a.gate, "repos": [], "baselines": {}, "units": [], "classes": {},
                          "all_flaky": False, "log_dir": log_root, "gate_run": hashlib.sha256(gate_raw).hexdigest()}
    inv.res.details = td
    gate_repos = gd.get("repos") if isinstance(gd.get("repos"), dict) else {}
    assert isinstance(gate_repos, dict)
    reds: List[str] = []
    red_results: Dict[str, GateResult] = {}
    for repo in pins.gated:
        rd = gate_repos.get(repo)
        res = GateResult.of(rd["result"]) if isinstance(rd, dict) and isinstance(rd.get("result"), dict) else None
        if res is None:
            raise Usage("the gate result %s judges no %s, which the pins gate: triage the gate of these pins" % (a.gate, repo))
        if res.commit != pins.heads[repo]:
            raise Usage("the gate result %s judges %s at %s, the pins name %s: triage the gate of the pinned heads"
                        % (a.gate, repo, short(res.commit), short(pins.heads[repo])))
        inv.res.heads[repo] = pins.heads[repo]
        if not res.incomplete and not res.passed:
            reds.append(repo)
            red_results[repo] = res
    if not reds:
        inv.res.summary = "nothing to triage: no gated repo's gate failed in " + a.gate
        return 2
    every_flaky = True
    units_out: List[Dict[str, Any]] = td["units"]
    classes: Dict[str, int] = td["classes"]
    batch_items = [it for it in bd.get("items") or [] if isinstance(it, dict)]
    for repo in reds:
        res = red_results[repo]
        directory = os.path.join(capsule, "repos", repo)
        if not os.path.isdir(directory):
            inv.res.summary = "%s: no clone at %s" % (repo, directory)
            return 2
        head = pins.heads[repo]
        try:
            g.checkout(directory, head)
        except git.GitError as e:
            inv.res.summary = "%s: the red head %s: %s" % (repo, short(head), e)
            return 2
        gate.say("triaging the red gate of %s (%s)" % (repo, short(head)))
        try:
            sess = Session.open(gate, directory, repo, os.path.join(log_root, "triage-" + repo))
        except TriageError as e:
            inv.res.summary = "%s: the gate's container for the triage: %s" % (repo, e)
            return 2
        merges: List[str] = []
        for it in batch_items:
            mc = it.get("merge_commits")
            if isinstance(mc, dict):
                merges.extend(strings_of(mc.get(repo)))
        try:
            units, base_name = triage(g, sess, RedRepo(repo=repo, dir=directory, head=head, result=res, batch_merges=merges, items=items))
        except (TriageError, git.GitError) as e:
            inv.res.summary = "%s: the triage could not run: %s" % (repo, e)
            return 2
        finally:
            sess.close()
        td["repos"].append(repo)
        td["baselines"][repo] = base_name
        every_flaky = every_flaky and all_flaky(units)
        for u in units:
            units_out.append(u.as_dict())
            classes[u.klass()] = classes.get(u.klass(), 0) + 1
            if u.unit.log:
                inv.res.logs.append(u.unit.log)
    td["all_flaky"] = every_flaky
    tally = ", ".join("%d %s" % (classes[c], c) for c in sorted(classes))
    if not every_flaky:
        inv.res.summary = "the red stands: %d unit(s) in %s (%s)" % (len(units_out), ", ".join(td["repos"]), tally)
        return 1
    gate.say("every failure re-ran green (flaky): the full gate runs once more")
    d, line, code = capsule_mode(gate, g, capsule, pins, a.pins, os.path.join(log_root, "regate"), a.mark_green,
                                 a.summary or "", remote, inv)
    td["regate"] = d
    inv.res.details = ordered(td, TRIAGE_KEYS)
    inv.res.summary = "every failure re-ran green (%s); the full gate ran once more: %s" % (tally, line)
    return code


if __name__ == "__main__":
    # SIGTERM / SIGHUP unwind like an interrupt: the containers are removed
    # and the result is written, not judged
    runner.raise_on_termination()
    sys.exit(main())
