"""One command run in a capsule's task repo — on the host, or inside the
repo's verification container — with its log, its deadline and its record.

Ported from the Go step commands' executor (command.go's RunCommand,
execcmd.go's execTo); the container half is
``pfstep.container``. ``run_command`` is the whole of one command:

- it runs ``bash -c <command>`` in ``<capsule>/repos/<repo>`` (or a
  working directory inside it), with a private TMPDIR, in its own process
  group, killed — the group whole — at the deadline (exit 124);
- ``isolation="container"``: the repo's HEAD must carry
  ``.polyforge/Dockerfile.verify``; the command runs in the repo's
  container (``pfstep.container``), started for it and removed when it
  ends; every process it left in the container is killed after it. Before
  starting one, the owner's containers whose deadline passed are removed
  (``container.sweep_expired``). ``isolation="host"``: the command runs on
  the host under the given environment (the step's command environment by
  default);
- its logs are ``<log_dir>/command.log`` (header ``$ <argv>`` and
  ``# cwd: <capsule-relative dir>``), and in a container also
  ``image-build.log`` and ``1.log`` (the mount probe: the container must
  see this clone at its own path);
- the result (``CommandResult``) carries the exit code, whether the
  deadline killed it or had passed before it could start, when it started
  and how long it took, its log (capsule-relative inside the capsule), the
  repo's HEAD and whether its tree was dirty, and the image and container
  it ran in (or ``host``).

A command that cannot be run as asked — no clone, no Dockerfile in a
container run, an image or container not had, a clone the container does
not see, a container lost while the command ran — raises
``ExecutorError``; the container engine missing or not answering raises
its subclass ``EnvironmentNotSatisfied``. Neither is ever an exit code or a
pass: a step script exits 2 on both. An interrupt (KeyboardInterrupt, or
``Interrupted`` once ``raise_on_termination`` is installed) propagates
after the container is removed.
"""

import datetime
import json
import os
import posixpath
import secrets
import signal
import tempfile
import time
from dataclasses import dataclass, field
from types import FrameType
from typing import IO, Any, Dict, List, Mapping, Optional, Sequence, Tuple

from . import container, duration, git, proc, tmpdir
from .container import ContainerOptions

TIMEOUT_EXIT = proc.TIMEOUT_EXIT

# The log names in a command's log directory.
COMMAND_LOG = "command.log"
IMAGE_BUILD_LOG = "image-build.log"

ISOLATION_CONTAINER = "container"
ISOLATION_HOST = "host"

# Bounds one git read of the run (seconds; Go's gitStepTimeout).
GIT_STEP_TIMEOUT = 600.0

# How a command with a private TMPDIR starts in a container: the directory
# is made first (the reap after the command removes it).
BOX_TMP_WRAP = ["sh", "-c", 'mkdir -p "$TMPDIR" && exec "$@"', "sh"]

BOX_LOST = "the verification container is no longer usable"


class ExecutorError(Exception):
    """The command could not be run as asked: never an exit code, never a
    pass. A step script reports the message and exits 2."""

    exit_code = 2


class EnvironmentNotSatisfied(ExecutorError):
    """The container engine (docker) is missing or does not answer: the
    machine does not meet the run's needs. A step script exits 2."""


class Interrupted(BaseException):
    """SIGTERM or SIGHUP arrived (raise_on_termination): the run unwinds,
    its containers removed, and the script exits 2."""

    def __init__(self, signum: int) -> None:
        super().__init__("interrupted by signal %d" % signum)
        self.signum = signum


def raise_on_termination() -> None:
    """Make SIGTERM and SIGHUP raise Interrupted, as SIGINT raises
    KeyboardInterrupt, so a script's ``finally`` removes the containers it
    started. An entry script that starts containers calls it first."""

    def handler(signum: int, frame: Optional[FrameType]) -> None:
        raise Interrupted(signum)

    signal.signal(signal.SIGTERM, handler)
    signal.signal(signal.SIGHUP, handler)


def bash_argv(command: str) -> List[str]:
    """How every declared command line runs."""
    return ["bash", "-c", command]


def env_last(env: Sequence[str], name: str) -> Tuple[str, bool]:
    """The value name carries last in env (NAME=value), as a process
    environment resolves duplicates: (value, whether it is set)."""
    for kv in reversed(env):
        k, eq, v = kv.partition("=")
        if eq and k == name:
            return v, True
    return "", False


def set_env(env: Sequence[str], name: str, value: str) -> List[str]:
    """env (NAME=value) with name replaced, or appended."""
    out = [kv for kv in env if kv.partition("=")[0] != name or "=" not in kv]
    return out + [name + "=" + value]


def go_time(t: Optional[datetime.datetime]) -> str:
    """t as Go's time.Time marshals in JSON (RFC 3339, the fraction's
    trailing zeros dropped); "0001-01-01T00:00:00Z" for None."""
    if t is None:
        return "0001-01-01T00:00:00Z"
    t = t.astimezone(datetime.timezone.utc)
    out = t.strftime("%Y-%m-%dT%H:%M:%S")
    if t.microsecond:
        out += "." + ("%06d" % t.microsecond).rstrip("0")
    return out + "Z"


@dataclass
class Evidence:
    """One command run's record."""

    argv: List[str]
    cwd: str
    exit_code: int
    started_at: datetime.datetime
    duration_ms: int
    log_path: str

    def as_dict(self) -> Dict[str, Any]:
        """The Go record's JSON (Evidence)."""
        return {
            "argv": list(self.argv),
            "cwd": self.cwd,
            "exit_code": self.exit_code,
            "started_at": go_time(self.started_at),
            "duration_ms": self.duration_ms,
            "log_path": self.log_path,
        }


@dataclass
class ExecSpec:
    """One command to run."""

    argv: List[str]
    # dir: the absolute working directory (a container sees it at the same path)
    dir: str
    # cwd: the record's cwd (capsule-relative when inside)
    cwd: str
    # timeout in seconds (0 = the runner's command_timeout; always capped
    # at the run's deadline)
    timeout: float = 0.0
    # private_tmp: the command gets its own TMPDIR, removed afterwards
    private_tmp: bool = False
    # capture: the command's stdout is also answered (ExecResult.stdout);
    # the log keeps it after the command's stderr
    capture: bool = False


@dataclass
class ExecResult:
    """One finished command."""

    exit: int = 0
    timed_out: bool = False
    # deadline_hit: the run's deadline, not the command's own timeout, killed it
    deadline_hit: bool = False
    # not_started: the run's deadline had passed; nothing ran, no log
    not_started: bool = False
    tmp_dir: str = ""
    log_path: str = ""
    evidence: Optional[Evidence] = None
    # error: why the command could not start at all ("" = it started)
    error: str = ""
    stdout: bytes = b""


def _write(f: IO[bytes], text: str) -> None:
    # a log write that fails costs only the log, never the command's run
    try:
        f.write(text.encode("utf-8", "replace"))
        f.flush()
    except OSError:
        pass


class Runner:
    """One run's commands: the capsule they are recorded against, the
    environment they start from and the deadline none runs past."""

    def __init__(
        self,
        capsule_dir: str,
        deadline: float,
        env: Optional[Mapping[str, str]] = None,
        deadline_what: str = "",
        command_timeout: float = 0.0,
    ) -> None:
        self.capsule_dir = capsule_dir
        # deadline: epoch seconds; no command runs past it
        self.deadline = deadline
        # env: a host command's environment, and what a container
        # command's allowlist is read from (default: the process's)
        self.env: Dict[str, str] = dict(os.environ if env is None else env)
        self.deadline_what = deadline_what
        self.command_timeout = command_timeout
        self._k: Dict[str, int] = {}

    def remaining(self) -> float:
        return self.deadline - time.time()

    def rel(self, p: str) -> str:
        """p relative to the capsule root (slash form), or p itself outside it."""
        try:
            r = os.path.relpath(p, self.capsule_dir)
        except ValueError:
            return p
        if r.startswith(".."):
            return p
        return r.replace(os.sep, "/")

    def deadline_note(self) -> str:
        return self.deadline_what or "the run's deadline"

    def box_env(self) -> List[str]:
        """The environment added to a container command: the allowlisted
        names of the run's environment."""
        return [name + "=" + self.env[name] for name in container.CONTAINER_ENV_ALLOW if name in self.env]

    def unmet(
        self, requires_env: Sequence[str], requires_cmd: Sequence[str], repo_dir: str, box: Optional[container.Box]
    ) -> str:
        """A planned command's unmet declared precondition ("" = none), judged
        where it runs: in box, its environment is what the run adds over the
        image's own and a bare command name resolves on the container's PATH;
        on the host, the run's environment and PATH. A name with a slash
        resolves against the repo. A container probe that fails marks the
        container lost (the run ends not judged) — never a skip."""
        missing: List[str] = []
        for v in requires_env:
            if box is not None:
                val, ok = env_last(self.box_env(), v)
                if not ok:
                    val, ok = env_last(box.env, v)
            else:
                ok = v in self.env
                val = self.env.get(v, "")
            if not ok or val == "":
                missing.append("env " + v)
        for b in requires_cmd:
            try:
                ok = self.resolves(b, repo_dir, box)
            except container.ContainerError as err:
                if box is not None and not box.is_removed():
                    box.mark_lost("probing its PATH for requires_cmd %s failed: %s" % (b, err))
                return ""
            if not ok:
                missing.append("command " + b)
        if not missing:
            return ""
        return "declared precondition unmet: missing " + ", ".join(missing)

    def resolves(self, b: str, repo_dir: str, box: Optional[container.Box]) -> bool:
        """Whether the command b resolves where the run's commands run (a
        failing container probe raises ContainerError)."""
        if box is None or "/" in b:
            return self.look_path(b, repo_dir)
        if box.lost_reason() or box.is_removed():
            raise container.ContainerError(BOX_LOST)
        return box.rt.has_command(box.id, b)

    def look_path(self, b: str, directory: str) -> bool:
        """Whether b is an executable: a path (relative to directory), or a
        name on the run's PATH."""
        if "/" in b:
            p = b if os.path.isabs(b) else os.path.join(directory, b)
            return os.path.exists(p) and not os.path.isdir(p) and os.access(p, os.X_OK)
        for d in self.env.get("PATH", "").split(os.pathsep):
            if d == "":
                continue
            p = os.path.join(d, b)
            if os.path.exists(p) and not os.path.isdir(p) and os.access(p, os.X_OK):
                return True
        return False

    def next_log(self, log_dir: str) -> str:
        """log_dir's next numbered log, <k>.log."""
        k = self._k.get(log_dir, 0) + 1
        self._k[log_dir] = k
        return os.path.join(log_dir, "%d.log" % k)

    def exec(self, log_dir: str, spec: ExecSpec, box: Optional[container.Box] = None) -> ExecResult:
        """exec_to with log_dir's next numbered log."""
        try:
            os.makedirs(log_dir, exist_ok=True)
        except OSError as err:
            return ExecResult(exit=-1, error=str(err))
        return self.exec_to(spec, self.next_log(log_dir), box)

    def exec_to(self, spec: ExecSpec, log_path: str, box: Optional[container.Box] = None) -> ExecResult:
        """Run spec with its combined output in log_path — on the host, or in
        box (``docker exec`` in the same directory; after the command every
        process it left in the container is killed and its TMPDIR removed)."""
        res = ExecResult(log_path=log_path)
        timeout = spec.timeout if spec.timeout > 0 else self.command_timeout
        capped = False
        rem = self.remaining()
        if rem < 1.0:
            res.exit, res.not_started = -1, True
            return res
        if timeout <= 0 or rem < timeout:
            timeout, capped = rem, True
        try:
            logf = container.open_log(log_path)
        except OSError as err:
            res.exit, res.error = -1, str(err)
            return res
        with logf:
            _write(logf, "$ %s\n# cwd: %s\n" % (" ".join(spec.argv), spec.cwd))
            argv = list(spec.argv)
            env: Optional[Dict[str, str]]
            private: Optional[tmpdir.PrivateTmp] = None
            if box is not None:
                why = box.lost_reason()
                if why or box.is_removed():
                    res.exit, res.error = 127, "%s: %s" % (BOX_LOST, why or "it was removed")
                    _write(logf, "\n# pf verify-run: could not start: %s\n" % res.error)
                    return res
                cenv = self.box_env()
                inner = list(spec.argv)
                if spec.private_tmp:
                    res.tmp_dir = box.tmp_path()
                    cenv = set_env(cenv, "TMPDIR", res.tmp_dir)
                    inner = BOX_TMP_WRAP + inner
                argv = box.rt.exec_argv(box.id, spec.dir, cenv, inner)
                env = box.rt.client_env()
            else:
                env = dict(self.env)
                if spec.private_tmp:
                    try:
                        private = tmpdir.new()
                    except OSError:
                        private = None
                    if private is not None:
                        res.tmp_dir = private.path
                        env["TMPDIR"] = private.path
            interrupted = False
            try:
                with tempfile.TemporaryFile() as cap:
                    start = time.time()
                    mono = time.monotonic()
                    try:
                        if spec.capture:
                            c = proc.run(argv, cwd=spec.dir, env=env, timeout=timeout, stdout=cap, stderr=logf)
                        else:
                            c = proc.run(argv, cwd=spec.dir, env=env, timeout=timeout, stdout=logf, merge_stderr=True)
                    except BaseException:
                        interrupted = True
                        raise
                    elapsed = time.monotonic() - mono
                    if spec.capture:
                        cap.seek(0)
                        res.stdout = cap.read()
                        try:
                            logf.write(res.stdout)
                            logf.flush()
                        except OSError:
                            pass
                if c.timed_out:
                    res.exit, res.timed_out, res.deadline_hit = TIMEOUT_EXIT, True, capped
                    if capped:
                        _write(logf, "\n# pf verify-run: killed at %s\n" % self.deadline_note())
                    else:
                        _write(logf, "\n# pf verify-run: killed at its %s timeout\n" % _dur(timeout))
                elif c.error:
                    res.exit, res.error = 127, c.error
                    _write(logf, "\n# pf verify-run: could not start: %s\n" % c.error)
                else:
                    res.exit = c.exit_code
                started = datetime.datetime.fromtimestamp(start, datetime.timezone.utc)
                res.evidence = Evidence(
                    argv=list(spec.argv),
                    cwd=spec.cwd,
                    exit_code=res.exit,
                    started_at=started.replace(microsecond=started.microsecond // 1000 * 1000),
                    duration_ms=max(int(elapsed * 1000), 1),
                    log_path=self.rel(log_path),
                )
            finally:
                if private is not None:
                    private.release()
                if box is not None and not interrupted:
                    self.reap(box, res.tmp_dir, logf)
        return res

    def reap(self, box: container.Box, tmp: str, logf: IO[bytes]) -> None:
        """Kill what a container command left running and remove its TMPDIR;
        a container that no longer answers is marked lost, unless the run
        itself removed it."""
        try:
            box.rt.reap(box.id, [tmp] if tmp else [])
        except container.ContainerError as err:
            if not box.is_removed():
                box.mark_lost("killing what a command left running failed: %s" % err)
                _write(logf, "\n# pf verify-run: the container did not answer after this command: %s\n" % err)

    def same_clone(self, box: container.Box, repo_dir: str, log_dir: str) -> str:
        """Why the container does not see this clone at its path ("" = it
        does): a nonce written into the clone's git directory (never part of
        its tree) must read back from inside."""
        nonce = secrets.token_hex(8)
        probe = os.path.join(repo_dir, ".git", "pf-gate-probe-" + nonce)
        try:
            with open(probe, "w", encoding="utf-8") as f:
                f.write(nonce + "\n")
        except OSError as err:
            return "the clone's git directory is not writable for the mount probe: %s" % err
        try:
            er = self.exec(
                log_dir,
                ExecSpec(argv=["cat", probe], dir=repo_dir, cwd=".", timeout=container.CONTAINER_OP_TIMEOUT, capture=True),
                box,
            )
        finally:
            try:
                os.remove(probe)
            except OSError:
                pass
        if er.not_started:
            return "the deadline passed before the mount probe could run in the container"
        if er.error:
            return "the mount probe could not start in the container: %s" % er.error
        out = er.stdout.decode("utf-8", "replace").strip()
        if er.exit != 0 or out != nonce:
            return (
                "the container does not see this clone at %s (the probe %s read back exit %d %s): the docker"
                " engine must mount this machine's paths at the same path"
                % (repo_dir, os.path.basename(probe), er.exit, json.dumps(out))
            )
        return ""


def _dur(seconds: float) -> str:
    return duration.format(int(round(seconds * duration.SECOND)))


@dataclass
class CommandOptions:
    """One command's run."""

    # capsule_dir: the capsule root; the clone is its repos/<repo>, and the
    # result's log is relative to it
    capsule_dir: str
    repo: str
    # command: the line, run as `bash -c <command>`
    command: str
    # deadline: epoch seconds — the image build and container start
    # included; the command is killed at it
    deadline: float
    # timeout: the step's declared timeout in seconds (the wording of the
    # deadline in the log)
    timeout: float
    # log_dir receives command.log (and image-build.log, 1.log); created
    log_dir: str
    # run numbers the container (its pf.verify.run label and name)
    run: int = 1
    # workdir: an absolute directory inside the clone ("" = its root)
    workdir: str = ""
    isolation: str = ISOLATION_CONTAINER
    # env: the host command's whole environment; a container command gets
    # only its allowlisted names (default: the step's command environment)
    env: Optional[Mapping[str, str]] = None
    container: ContainerOptions = field(default_factory=ContainerOptions)


@dataclass
class CommandResult:
    """One command that ran (or whose time ran out before it could start)."""

    # exit: 124 when the deadline killed it or had passed before it started
    exit: int = 0
    timed_out: bool = False
    # not_started: the deadline had passed before it could start — nothing
    # ran, no log
    not_started: bool = False
    started_at: Optional[datetime.datetime] = None
    duration_ms: int = 0
    # log: capsule-relative inside the capsule, else absolute ("" when it
    # never started)
    log: str = ""
    # head: the repo's HEAD when it started; dirty: its tree differed (git status)
    head: str = ""
    dirty: bool = False
    image: str = ""
    container: str = ""
    # host: it ran on the host (isolation host)
    host: bool = False

    def as_dict(self) -> Dict[str, Any]:
        """The fields in the Go struct's order (CommandResult), snake_case."""
        return {
            "exit": self.exit,
            "timed_out": self.timed_out,
            "not_started": self.not_started,
            "started_at": go_time(self.started_at),
            "duration_ms": self.duration_ms,
            "log": self.log,
            "head": self.head,
            "dirty": self.dirty,
            "image": self.image,
            "container": self.container,
            "host": self.host,
        }


def run_command(o: CommandOptions) -> CommandResult:
    """Run o.command in repo o.repo, as the module's doc says."""
    clone = os.path.join(o.capsule_dir, "repos", o.repo)
    if not os.path.isdir(clone):
        raise ExecutorError("repos/%s is not a task repo clone of the capsule" % o.repo)
    if o.isolation not in (ISOLATION_CONTAINER, ISOLATION_HOST):
        raise ExecutorError("isolation %s: want %s or %s" % (json.dumps(o.isolation), ISOLATION_CONTAINER, ISOLATION_HOST))
    host = o.isolation == ISOLATION_HOST
    if not host and not container.has_dockerfile_at(clone, "HEAD"):
        raise ExecutorError(
            "repos/%s carries no %s at HEAD: a container run runs only inside the repo's verification"
            " container, never on the host" % (o.repo, container.DOCKERFILE_VERIFY)
        )
    try:
        os.makedirs(o.log_dir, exist_ok=True)
    except OSError as err:
        raise ExecutorError("the log directory %s: %s" % (o.log_dir, err)) from None
    try:
        head = git.git(clone, "rev-parse", "--verify", "HEAD^{commit}", timeout=GIT_STEP_TIMEOUT)
    except git.GitError as err:
        raise ExecutorError("repos/%s: HEAD unreadable: %s" % (o.repo, err)) from None
    try:
        status = git.git(clone, "status", "--porcelain", "--untracked-files=normal", timeout=GIT_STEP_TIMEOUT)
    except git.GitError as err:
        raise ExecutorError("repos/%s: tree state unreadable: %s" % (o.repo, err)) from None
    r = Runner(o.capsule_dir, o.deadline, env=o.env, deadline_what="the run step's timeout (%s)" % _dur(o.timeout))
    res = CommandResult(head=head.strip(), dirty=status.strip() != "")
    if r.remaining() < 1.0:
        res.exit, res.timed_out, res.not_started = TIMEOUT_EXIT, True, True
        return res
    workdir, cwd_rel = clone, "repos/" + o.repo
    if o.workdir:
        if not os.path.isabs(o.workdir) or not container.within(clone, o.workdir):
            raise ExecutorError(
                "repos/%s: the working directory %s lies outside the clone, which is all the container mounts"
                % (o.repo, o.workdir)
            )
        workdir = o.workdir
        rel = os.path.relpath(o.workdir, clone)
        if rel != ".":
            cwd_rel = posixpath.join(cwd_rel, rel.replace(os.sep, "/"))
    log_path = os.path.join(o.log_dir, COMMAND_LOG)
    if host:
        res.host = True
        er = r.exec_to(ExecSpec(argv=bash_argv(o.command), dir=workdir, cwd=cwd_rel, timeout=r.remaining(), private_tmp=True), log_path)
        return _command_result(o, res, er, log_path, r)

    co = container.for_run(o.container, o.deadline)
    rt = co.engine()
    try:
        rt.available()
    except container.ContainerError as err:
        raise EnvironmentNotSatisfied("repos/%s: the container engine is unavailable: %s" % (o.repo, err)) from None
    container.sweep_run(co, co.say or (lambda _: None))
    box: Optional[container.Box] = None
    try:
        try:
            box = container.start_box(co, o.repo, clone, o.log_dir, o.deadline, o.run, r.rel)
        except container.BoxError as err:
            raise ExecutorError("repos/%s: the verification container could not be prepared: %s" % (o.repo, err)) from None
        res.image, res.container = box.report.image, box.report.name
        why = r.same_clone(box, clone, o.log_dir)
        if why:
            raise ExecutorError("repos/%s: %s" % (o.repo, why))
        er = r.exec_to(
            ExecSpec(argv=bash_argv(o.command), dir=workdir, cwd=cwd_rel, timeout=r.remaining(), private_tmp=True),
            log_path,
            box,
        )
        why = box.lost_reason()
        if why and not er.not_started and not er.error:
            raise ExecutorError("repos/%s: the verification container became unusable while the command ran: %s" % (o.repo, why))
        return _command_result(o, res, er, log_path, r)
    finally:
        if box is not None:
            box.remove()


def _command_result(o: CommandOptions, res: CommandResult, er: ExecResult, log_path: str, r: Runner) -> CommandResult:
    where = "on the host" if res.host else "in the verification container"
    if er.not_started:
        res.exit, res.timed_out, res.not_started = TIMEOUT_EXIT, True, True
        return res
    if er.error:
        raise ExecutorError("repos/%s: the command could not start %s: %s" % (o.repo, where, er.error))
    res.exit, res.timed_out = er.exit, er.timed_out
    if not er.timed_out and time.time() >= o.deadline and er.exit != 0:
        # the deadline and the command's end raced: a command still running
        # at the deadline was killed by it
        res.exit, res.timed_out = TIMEOUT_EXIT, True
    if er.evidence is not None:
        res.started_at, res.duration_ms = er.evidence.started_at, er.evidence.duration_ms
    res.log = r.rel(log_path)
    return res
