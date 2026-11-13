"""A repo's verification container: its image, labels, limits and lifecycle.

The workflow contract's container convention ("Scripts"): every script
that starts a container uses this module, so a container one script
started is found and removed by the next.

- The image is built from the repo's ``.polyforge/Dockerfile.verify`` at
  HEAD alone (an empty build context) and tagged
  ``pf-verify-<repo>:<first 16 bytes of the file's sha256, hex>``; its
  build log is ``image-build.log`` in the run's log directory.
- The container (``--init``, ``--log-driver none``) carries the labels
  ``pf.verify.daemon`` (the owner), ``.boot``, ``.wi``, ``.run``,
  ``.repo`` and, when it has a deadline, ``.expires`` (unix seconds). It
  mounts the clone read-write at its own path, the repo's named cache
  volume of the image's generation ``pf-verify-cache-<repo>-<the image
  tag's hash>`` at ``/cache`` and nothing else of the host; no docker
  socket. Its environment is the image's own plus an
  allowlist (``CONTAINER_ENV_ALLOW``): never PATH, HOME, ``TMUX*``,
  ``PF_*`` or a credential.
- Its limits are parameters (``ContainerOptions``): CPUs default to
  min(8, this machine's cores), memory to 16g (swap the same), pids 4096.
- Its main process is a keep-alive under ``--init`` that records its pid;
  ``reap`` kills every other process after each command. A container with
  a deadline gets a bounded keep-alive and ``--rm``: it ends by itself at
  its deadline plus ``KEEP_ALIVE_GRACE`` even when its owner was killed
  outright, and the engine removes it.
- ``sweep_expired`` removes an owner's containers whose ``.expires`` has
  passed (what a killed run left).
- The cache volume holds what the image's toolchain caches (Go's module
  and build caches, npm's cache; never ``node_modules``, which each
  ``npm ci`` installs into the clone) and lives as long as the image's
  generation: any byte changed in the Dockerfile gives a new, empty
  volume. Within a generation its contents check themselves (modules
  against go.sum, build actions keyed by the toolchain, npm's cache by
  integrity): a lockfile change only misses and downloads.
  ``prune_cache_volumes`` (each start) removes the repo's older
  generations no container references — a volume named exactly
  ``pf-verify-cache-<repo>-<32 hex>``, not the current one, that the
  engine lists as unused; another repo's volume or one of another shape is
  never touched. One a running verification on an older image still uses
  is not listed, and the engine refuses to remove it; it goes at a later
  start. Removed in the window between that verification's image and its
  container start, its ``docker run`` creates an empty volume of the same
  name: a cold cache, never wrong contents.

The engine is the docker CLI (``DockerCLI``); a test hands in a fake with
the same methods (``ContainerRuntime``). An engine call that fails raises
``ContainerError``.
"""

import dataclasses
import datetime
import hashlib
import json
import os
import re
import secrets
import shutil
import signal
import threading
import time
from dataclasses import dataclass, field
from decimal import Decimal
from typing import IO, Callable, Dict, List, Mapping, Optional, Protocol, Sequence, Tuple

from . import duration, git, proc, stepenv, tmpdir

DOCKERFILE_VERIFY = ".polyforge/Dockerfile.verify"

# The limits a container gets when none is given (measured on this
# repository's own full bar and its light tier's browser e2e, whose
# per-suite `next dev` servers peak near 12.5 GiB at 4 browsers —
#; Go's DefaultContainer*). The CPU default a
# container actually gets is default_cpus(): this ceiling, capped at the
# machine's cores — docker refuses a --cpus above them.
DEFAULT_CONTAINER_CPUS = 8.0
DEFAULT_CONTAINER_MEMORY = "16g"
DEFAULT_CONTAINER_PIDS = 4096

CONTAINER_CACHE_DIR = "/cache"

# The names of the run's environment a container command is given when
# they are set (beside TMPDIR): the locale, the proxies (both spellings),
# CA overrides, Go's module settings and the npm registry.
CONTAINER_ENV_ALLOW = (
    "LANG", "LC_ALL", "TZ",
    "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
    "SSL_CERT_FILE", "SSL_CERT_DIR",
    "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GOFLAGS",
    "npm_config_registry",
)

LABEL_DAEMON = "pf.verify.daemon"
LABEL_BOOT = "pf.verify.boot"
LABEL_WI = "pf.verify.wi"
LABEL_RUN = "pf.verify.run"
LABEL_REPO = "pf.verify.repo"
LABEL_EXPIRES = "pf.verify.expires"

# The owner label of a container a script starts, and of a test gate's.
STEP_OWNER = "pf-step"
GATE_OWNER = "pf-gate"

# How long a container may outlive its run's deadline (seconds).
KEEP_ALIVE_GRACE = 300.0

# Bounds one engine call other than a build or a command (seconds).
CONTAINER_OP_TIMEOUT = 120.0

KEEP_ALIVE_PID_FILE = "/run/pf-verify-keepalive.pid"
KEEP_ALIVE_SCRIPT = (
    "mkdir -p /run && echo $$ > " + KEEP_ALIVE_PID_FILE + " && while :; do sleep 3600; done"
)

# Kills every process but pid 1 (the init), the keep-alive and itself,
# twice over (a fork in flight), then removes its arguments.
REAP_SCRIPT = (
    "read k < " + KEEP_ALIVE_PID_FILE + " || exit 70\n"
    '[ -n "$k" ] || exit 70\n'
    "for pass in 1 2; do\n"
    "  for d in /proc/[0-9]*; do\n"
    "    p=${d#/proc/}\n"
    '    case "$p" in 1|"$$"|"$k") continue ;; esac\n'
    '    kill -9 "$p" 2>/dev/null\n'
    "  done\n"
    "done\n"
    'for t in "$@"; do rm -rf -- "$t"; done'
)

PROBE_MARK = "pf-probe:"
# Prints `command -v`'s status instead of exiting with it: the docker
# client exits 1 for its own failures too.
HAS_COMMAND_SCRIPT = 'command -v "$1" >/dev/null 2>&1; echo "' + PROBE_MARK + '$?"'

_WAIT_KEEP_ALIVE = (
    "i=0; while [ ! -s " + KEEP_ALIVE_PID_FILE + " ]; do i=$((i+1)); [ $i -gt 30 ] && exit 1; sleep 1; done"
)

_TAG_UNSAFE = re.compile(r"[^a-z0-9._-]+", re.ASCII)
_NAME_UNSAFE = re.compile(r"[^a-zA-Z0-9_.-]+", re.ASCII)
_INT = re.compile(r"[+-]?[0-9]+", re.ASCII)


class ContainerError(Exception):
    """A container engine call that failed."""


class BoxError(Exception):
    """Why a repo's container could not be had (the image, the start)."""


Say = Callable[[str], None]


def _host_cpus() -> int:
    """The cores this process may run on (Go's runtime.NumCPU)."""
    try:
        return len(os.sched_getaffinity(0))  # type: ignore[attr-defined,unused-ignore]
    except (AttributeError, OSError):
        return os.cpu_count() or 0


def default_cpus(host_cpus: Callable[[], int] = _host_cpus) -> float:
    """The CPU limit a container gets when none is given: 8, capped at the
    machine's cores (an unknown count leaves 8). A given limit is never
    capped: docker refuses one above the cores, and says so."""
    n = host_cpus()
    if 0 < n < DEFAULT_CONTAINER_CPUS:
        return float(n)
    return DEFAULT_CONTAINER_CPUS


def format_cpus(cpus: float) -> str:
    """cpus as Go's strconv.FormatFloat(cpus, 'f', -1, 64) writes it: the
    shortest decimal that reads back, never an exponent ("8", "2.5")."""
    s = repr(float(cpus))
    if "e" in s or "E" in s:
        s = format(Decimal(s), "f")
    if "." in s:
        s = s.rstrip("0").rstrip(".")
    return s


@dataclass
class Bind:
    """A host directory mounted at its own path."""

    path: str
    read_only: bool = False


@dataclass
class ContainerSpec:
    """One container to start."""

    name: str
    image: str
    labels: Dict[str, str] = field(default_factory=dict)
    binds: List[Bind] = field(default_factory=list)
    # named volume -> in-container path
    volumes: Dict[str, str] = field(default_factory=dict)
    cpus: float = DEFAULT_CONTAINER_CPUS
    memory: str = DEFAULT_CONTAINER_MEMORY
    pids: int = DEFAULT_CONTAINER_PIDS
    # lifetime bounds the keep-alive (seconds; 0 = up until removed): the
    # container then ends and the engine removes it
    lifetime: float = 0.0


@dataclass
class ContainerReport:
    """One repo's container as a run records it."""

    image: str
    built: bool
    build_log: str
    name: str
    cpus: str
    memory: str
    pids: int
    # lost: why the container became unusable while commands ran
    lost: str = ""
    # left: why the run could not remove it
    left: str = ""

    def as_dict(self) -> Dict[str, object]:
        """The Go record's JSON (ContainerReport): empty optional keys left out."""
        out: Dict[str, object] = {"image": self.image, "built": self.built}
        if self.build_log:
            out["build_log"] = self.build_log
        out.update({"name": self.name, "cpus": self.cpus, "memory": self.memory, "pids": self.pids})
        if self.lost:
            out["lost"] = self.lost
        if self.left:
            out["left"] = self.left
        return out


class ContainerRuntime(Protocol):
    """The container engine as a run uses it (DockerCLI, or a test's fake).
    Every method but exec_argv and client_env raises ContainerError when
    the engine call fails."""

    def available(self) -> None:
        """Return when the engine can run containers."""

    def image_exists(self, tag: str) -> bool:
        """Whether tag is present locally."""

    def build_image(self, tag: str, dockerfile: bytes, labels: Mapping[str, str], log: IO[bytes], timeout: float) -> None:
        """Build tag from dockerfile with an empty build context, its output in log."""

    def image_env(self, tag: str) -> List[str]:
        """The image's own environment (NAME=value)."""

    def start(self, spec: ContainerSpec) -> str:
        """Start a container that stays up until removed; answer its id."""

    def exec_argv(self, cid: str, workdir: str, env: Sequence[str], argv: Sequence[str]) -> List[str]:
        """The host command line that runs argv in the container, in
        workdir, with env (NAME=value) added to the image's environment."""

    def client_env(self) -> Optional[Dict[str, str]]:
        """The environment exec_argv's command runs with (None = the process's)."""

    def has_command(self, cid: str, name: str) -> bool:
        """Whether name resolves on the container's PATH."""

    def reap(self, cid: str, remove: Sequence[str]) -> None:
        """Kill every process of the container but its keep-alive, then
        remove the given in-container paths; failing = the container is
        no longer usable."""

    def remove(self, cid: str) -> None:
        """Remove the container, killing whatever runs in it."""

    def list_labelled(self, labels: Mapping[str, str], key: str) -> Dict[str, str]:
        """Every container (running or not) carrying all labels: its id ->
        its value of the label key ("" = it has none)."""

    def unused_volumes(self, name: str) -> List[str]:
        """Every volume whose name contains name and that no container
        (running or not) references."""

    def remove_volume(self, name: str) -> None:
        """Remove the volume; the engine refuses (ContainerError) one a
        container references."""


def bounded_keep_alive(lifetime: float) -> str:
    """The main process of a container with a lifetime (seconds): it records
    its pid like KEEP_ALIVE_SCRIPT, then exits once the lifetime has passed.
    It reads the clock between sleeps: a reap kills its sleep, never itself."""
    secs = int(lifetime)
    if secs < 1:
        secs = 1
    step = min(max(secs // 10, 1), 30)
    return (
        "mkdir -p /run && echo $$ > %s && end=$(( $(date +%%s) + %d )) && while :; do sleep %d; "
        "now=$(date +%%s) || continue; case \"$now\" in ''|*[!0-9]*) continue ;; esac; "
        "[ \"$now\" -lt \"$end\" ] || exit 0; done"
    ) % (KEEP_ALIVE_PID_FILE, secs, step)


def probe_status(out: str) -> bool:
    """Read HAS_COMMAND_SCRIPT's output: whether the name resolved."""
    s = out.strip()
    i = s.rfind(PROBE_MARK)
    if i < 0:
        raise ContainerError("docker exec: the command probe printed no status (output %s)" % json.dumps(s))
    rest = s[i + len(PROBE_MARK):]
    if not _INT.fullmatch(rest):
        raise ContainerError("docker exec: the command probe printed an unreadable status (output %s)" % json.dumps(s))
    return int(rest) == 0


def _signal_text(sig: int) -> str:
    try:
        name = signal.strsignal(sig)
    except ValueError:
        name = None
    return "signal: " + (name.lower() if name else str(sig))


def _failure(c: proc.Completed) -> str:
    """How a command failed, as Go's exec error reads ("exit status 1")."""
    if c.error:
        return c.error
    if c.timed_out:
        return "signal: killed"
    if c.signal:
        return _signal_text(c.signal)
    return "exit status %d" % c.exit_code


def _seconds_text(seconds: float) -> str:
    return duration.format(int(round(seconds * duration.SECOND)))


def look_path_in(name: str, path: str) -> str:
    """Where name resolves on the PATH value path ("" = nowhere)."""
    for d in path.split(os.pathsep):
        if not d:
            continue
        p = os.path.join(d, name)
        if os.path.isfile(p) and os.access(p, os.X_OK):
            return p
    return ""


class DockerCLI:
    """The docker command-line client. Every call is one ``docker`` process
    in its own process group (pfstep.proc) under the process's own
    environment (DOCKER_HOST and friends apply), bounded by op_timeout —
    but a build (the run's deadline) and a command's ``docker exec`` (its
    timeout)."""

    def __init__(self, bin: str = "docker", op_timeout: float = CONTAINER_OP_TIMEOUT) -> None:
        self.bin = bin or "docker"
        self.op_timeout = op_timeout if op_timeout > 0 else CONTAINER_OP_TIMEOUT

    @classmethod
    def for_env(cls, environ: Optional[Mapping[str, str]] = None) -> "DockerCLI":
        """The client on environ's PATH (default the process's), else on the
        process's PATH, else the bare name (available() then says it is
        missing)."""
        p = look_path_in("docker", (os.environ if environ is None else environ).get("PATH", ""))
        if not p:
            p = shutil.which("docker") or "docker"
        return cls(bin=p)

    def client_env(self) -> Optional[Dict[str, str]]:
        return None

    def _env(self, extra: Optional[Mapping[str, str]] = None) -> Dict[str, str]:
        env = dict(os.environ)
        if extra:
            env.update(extra)
        return env

    def _run(self, args: Sequence[str], input: Optional[bytes] = None) -> str:
        """Run one docker command bounded by op_timeout; answer its stdout. A
        failure carries the last line of its stderr."""
        c = proc.run([self.bin, *args], env=self._env(), timeout=self.op_timeout, input=input)
        if c.exit_code == 0 and not c.error:
            return c.stdout.decode("utf-8", "replace")
        why = _failure(c)
        if c.timed_out:
            why += " (cut at its deadline; a docker call gets at most %s)" % _seconds_text(self.op_timeout)
        msg = c.stderr.decode("utf-8", "replace").strip()
        if "\n" in msg:
            msg = msg[msg.rfind("\n") + 1:]
        if not msg:
            raise ContainerError("docker %s: %s" % (args[0], why))
        raise ContainerError("docker %s: %s: %s" % (args[0], why, msg))

    def available(self) -> None:
        if shutil.which(self.bin) is None:
            raise ContainerError('the docker client "%s" is not found' % self.bin)
        out = self._run(["version", "--format", "{{.Server.Version}}"])
        if not out.strip():
            raise ContainerError("docker version: the daemon reported no server version")

    def image_exists(self, tag: str) -> bool:
        return self._run(["image", "ls", "--quiet", "--filter", "reference=" + tag]).strip() != ""

    def build_image(self, tag: str, dockerfile: bytes, labels: Mapping[str, str], log: IO[bytes], timeout: float) -> None:
        """``docker build -``: the Dockerfile on stdin, no build context."""
        args = ["build", "--tag", tag]
        for k in sorted(labels):
            args += ["--label", k + "=" + labels[k]]
        args.append("-")
        log.flush()
        c = proc.run(
            [self.bin, *args],
            env=self._env({"BUILDKIT_PROGRESS": "plain"}),
            timeout=max(timeout, 0.001),
            input=dockerfile,
            stdout=log,
            merge_stderr=True,
        )
        if c.exit_code != 0 or c.error:
            raise ContainerError("docker build: %s" % _failure(c))

    def image_env(self, tag: str) -> List[str]:
        out = self._run(["image", "inspect", "--format", "{{json .Config.Env}}", tag])
        try:
            env = json.loads(out.strip())
        except ValueError as err:
            raise ContainerError("docker image inspect: unreadable environment: %s" % err) from None
        if env is None:
            return []
        if not isinstance(env, list) or not all(isinstance(e, str) for e in env):
            raise ContainerError("docker image inspect: unreadable environment: %s" % json.dumps(out.strip()))
        return list(env)

    def start_args(self, spec: ContainerSpec) -> List[str]:
        """The ``docker run`` arguments of spec (Go's DockerCLI.StartArgs)."""
        args = ["run", "--detach", "--init", "--name", spec.name, "--log-driver", "none"]
        keep_alive = KEEP_ALIVE_SCRIPT
        if spec.lifetime > 0:
            # the keep-alive ends the container at its lifetime; the engine
            # then removes it (its owner may be gone: killed outright)
            args.append("--rm")
            keep_alive = bounded_keep_alive(spec.lifetime)
        for k in sorted(spec.labels):
            args += ["--label", k + "=" + spec.labels[k]]
        args += [
            "--cpus", format_cpus(spec.cpus),
            "--memory", spec.memory, "--memory-swap", spec.memory,
            "--pids-limit", str(spec.pids),
        ]
        for b in spec.binds:
            m = "type=bind,source=" + b.path + ",target=" + b.path
            if b.read_only:
                m += ",readonly"
            args += ["--mount", m]
        for name in sorted(spec.volumes):
            args += ["--mount", "type=volume,source=" + name + ",target=" + spec.volumes[name]]
        return args + ["--entrypoint", "sh", spec.image, "-c", keep_alive]

    def start(self, spec: ContainerSpec) -> str:
        try:
            out = self._run(self.start_args(spec))
        except ContainerError:
            # a container created but not started still holds its name
            try:
                self._run(["rm", "--force", spec.name])
            except ContainerError:
                pass
            raise
        cid = out.strip()
        if not cid:
            raise ContainerError("docker run answered no container id")
        # the keep-alive's pid file is what reap spares: wait until it exists
        try:
            self._run(["exec", cid, "sh", "-c", _WAIT_KEEP_ALIVE])
        except ContainerError as err:
            try:
                self.remove(cid)
            except ContainerError as rerr:
                raise ContainerError(
                    "the container's keep-alive did not start: %s (and removing it failed: %s"
                    " — the owner's next sweep removes it)" % (err, rerr)
                ) from None
            raise ContainerError("the container's keep-alive did not start: %s" % err) from None
        return cid

    def exec_argv(self, cid: str, workdir: str, env: Sequence[str], argv: Sequence[str]) -> List[str]:
        out = [self.bin, "exec", "--workdir", workdir]
        for kv in env:
            out += ["--env", kv]
        return out + [cid, *argv]

    def has_command(self, cid: str, name: str) -> bool:
        return probe_status(self._run(["exec", cid, "sh", "-c", HAS_COMMAND_SCRIPT, "sh", name]))

    def reap(self, cid: str, remove: Sequence[str]) -> None:
        self._run(["exec", cid, "sh", "-c", REAP_SCRIPT, "sh", *remove])

    def remove(self, cid: str) -> None:
        self._run(["rm", "--force", cid])

    def list_labelled(self, labels: Mapping[str, str], key: str) -> Dict[str, str]:
        args = ["ps", "--all", "--no-trunc", "--format", "{{.ID}}\\t{{.Label \"" + key + "\"}}"]
        for k in sorted(labels):
            args += ["--filter", "label=" + k + "=" + labels[k]]
        found: Dict[str, str] = {}
        for line in self._run(args).split("\n"):
            cid, _, val = line.rstrip("\r").partition("\t")
            cid = cid.strip()
            if cid:
                found[cid] = val.strip()
        return found

    def unused_volumes(self, name: str) -> List[str]:
        out = self._run(["volume", "ls", "--quiet", "--filter", "dangling=true", "--filter", "name=" + name])
        return sorted(v.strip() for v in out.split("\n") if v.strip())

    def remove_volume(self, name: str) -> None:
        # never --force: the engine's refusal of a volume in use is the
        # second guard of prune_cache_volumes
        self._run(["volume", "rm", name])


def tag_safe(s: str) -> str:
    """s lower-cased and reduced to what an image repository allows ("repo"
    when nothing is left). A non-ASCII letter whose Go lower case is ASCII
    (U+0130) may differ from Go; repo names are ASCII."""
    s = _TAG_UNSAFE.sub("-", s.lower()).strip(".-_")
    return s or "repo"


def _generation(dockerfile: bytes) -> str:
    """The image generation of a Dockerfile: its sha256's first 16 bytes, hex."""
    return hashlib.sha256(dockerfile).digest()[:16].hex()


def image_tag(repo: str, dockerfile: bytes) -> str:
    """The image of a repo's Dockerfile: pf-verify-<repo>:<generation>."""
    return "pf-verify-" + tag_safe(repo) + ":" + _generation(dockerfile)


def _cache_volume_prefix(repo: str) -> str:
    return "pf-verify-cache-" + tag_safe(repo) + "-"


def cache_volume(repo: str, dockerfile: bytes) -> str:
    """The repo's named cache volume for the image of dockerfile:
    pf-verify-cache-<repo>-<generation>, the image tag's own hash."""
    return _cache_volume_prefix(repo) + _generation(dockerfile)


def prune_cache_volumes(rt: ContainerRuntime, repo: str, keep: str, say: Say) -> List[str]:
    """Remove repo's cache volumes of older image generations that no
    container references (keep is the current one); answer the removed
    names. Only a name of exactly the generation shape counts: another
    repo's volume (one whose name starts with this repo's) or one of
    another shape is never touched. A failing listing or removal costs
    only the pruning."""
    prefix = _cache_volume_prefix(repo)
    shape = re.compile(re.escape(prefix) + r"[0-9a-f]{32}", re.ASCII)
    removed: List[str] = []
    try:
        found = rt.unused_volumes(prefix)
    except ContainerError as err:
        say("could not list older cache volumes (%s): none removed" % err)
        return removed
    for name in found:
        if name == keep or not shape.fullmatch(name):
            continue
        try:
            rt.remove_volume(name)
        except ContainerError as err:
            say("could not remove cache volume %s (an older image's): %s" % (name, err))
            continue
        removed.append(name)
        say("removed cache volume %s (an older image's)" % name)
    return removed


def container_name(wi: str, run: int, repo: str) -> str:
    """A container name unique to the run and repo."""
    w = _NAME_UNSAFE.sub("-", wi).strip("-._") or "manual"
    return "pf-verify-%s-%d-%s-%s" % (w, run, tag_safe(repo), secrets.token_hex(4))


@dataclass
class ContainerOptions:
    """How a run's containers are started: the engine, the labels, the
    limits, the bounded keep-alive. Zero values take ``with_defaults``'s."""

    runtime: Optional[ContainerRuntime] = None
    # owner: the pf.verify.daemon label (default STEP_OWNER)
    owner: str = ""
    # boot: the pf.verify.boot label (default a fresh nonce)
    boot: str = ""
    # wi: the pf.verify.wi label (default PF_WI_ID)
    wi: str = ""
    cpus: float = 0.0
    memory: str = ""
    pids: int = 0
    # say receives one line for a container that could not be removed and
    # one for each older cache volume removed or not (prune_cache_volumes)
    say: Optional[Say] = None
    # until ends the keep-alive at that time (epoch seconds; 0 = unbounded)
    until: float = 0.0
    labels: Dict[str, str] = field(default_factory=dict)

    def with_defaults(self, environ: Optional[Mapping[str, str]] = None) -> "ContainerOptions":
        """A copy with every zero value filled: the limits' defaults, the
        labels from the step environment environ (default the process's),
        the docker client on its PATH."""
        env = os.environ if environ is None else environ
        c = dataclasses.replace(self, labels=dict(self.labels))
        if c.cpus <= 0:
            c.cpus = default_cpus()
        if not c.memory.strip():
            c.memory = DEFAULT_CONTAINER_MEMORY
        if c.pids <= 0:
            c.pids = DEFAULT_CONTAINER_PIDS
        if not c.owner:
            c.owner = STEP_OWNER
        if not c.boot:
            c.boot = secrets.token_hex(8)
        if not c.wi:
            c.wi = env.get(stepenv.ENV_WI_ID, "")
        if c.runtime is None:
            c.runtime = DockerCLI.for_env(env)
        return c

    def engine(self) -> ContainerRuntime:
        """The runtime (with_defaults sets it)."""
        if self.runtime is None:
            raise ContainerError("no container engine")
        return self.runtime


def for_run(co: ContainerOptions, deadline: float, environ: Optional[Mapping[str, str]] = None) -> ContainerOptions:
    """co with its defaults (``with_defaults``) and one run's lifetime:
    every container it starts carries the run's deadline (epoch seconds)
    as ``pf.verify.expires`` and a keep-alive ending KEEP_ALIVE_GRACE after
    it. A run killed outright cannot remove its containers: they end by
    themselves (the engine removes them), and the owner's next run's sweep
    (``sweep_run``) removes one still there once the deadline has passed.
    Every script that starts containers builds its options with it once."""
    c = co.with_defaults(environ)
    c.until = deadline + KEEP_ALIVE_GRACE
    c.labels.setdefault(LABEL_EXPIRES, str(int(deadline)))
    return c


def sweep_run(co: ContainerOptions, say: Say) -> List[str]:
    """A run's sweep before it starts a container (co from ``for_run``; its
    engine available): the owner's containers whose deadline has passed
    removed — strictly for a gate's own owner, whose containers all carry
    a deadline (``sweep_expired``)."""
    return sweep_expired(co.engine(), co.owner, time.time(), co.owner == GATE_OWNER, say)


class Box:
    """One repo's started container."""

    def __init__(
        self,
        rt: ContainerRuntime,
        cid: str,
        env: List[str],
        report: ContainerReport,
        say: Optional[Say] = None,
        engine: str = "",
        base_root: str = "",
    ) -> None:
        self.rt = rt
        self.id = cid
        # env: the image's environment (NAME=value)
        self.env = env
        self.report = report
        self.engine = engine
        self.base_root = base_root
        self._say = say
        self._mu = threading.Lock()
        self._lost = ""
        self._seq = 0
        self._removed = False
        self._gone: Optional[threading.Event] = None

    def lost_reason(self) -> str:
        """Why the container is unusable ("" = it is not)."""
        with self._mu:
            return self._lost

    def mark_lost(self, why: str) -> None:
        with self._mu:
            if not self._lost:
                self._lost = why
                self.report.lost = why

    def tmp_path(self) -> str:
        """A fresh private TMPDIR inside the container: the host's root and
        prefix (tmpdir), so a unix socket under Go's t.TempDir() fits."""
        with self._mu:
            self._seq += 1
            return os.path.join(tmpdir.ROOT, tmpdir.DIR_PREFIX + str(self._seq))

    def is_removed(self) -> bool:
        with self._mu:
            return self._removed

    def remove(self) -> None:
        """Remove the container once; a call while another removes it waits
        for that one. A failing removal is recorded (report.left) and said
        once: the owner's next sweep removes the container."""
        with self._mu:
            first = not self._removed
            if first:
                self._removed = True
                self._gone = threading.Event()
            gone = self._gone
        if gone is None:
            return
        if not first:
            gone.wait()
            return
        try:
            self.rt.remove(self.id)
        except ContainerError as err:
            why = "removing it failed (%s): the owner's next sweep removes it" % err
            with self._mu:
                self.report.left = why
                name = self.report.name
            if self._say is not None:
                self._say("verify-run: verification container %s (%s) was left behind: %s" % (name, self.id, why))
        finally:
            gone.set()


def within(directory: str, p: str) -> bool:
    """Whether p is directory or lies under it (lexically, as Go's filepath.Rel)."""
    if os.path.isabs(directory) != os.path.isabs(p):
        return False
    rel = os.path.relpath(p, directory)
    return rel != ".." and not rel.startswith(".." + os.sep)


def has_dockerfile_at(directory: str, rev: str = "HEAD") -> bool:
    """Whether rev's tree in the clone carries DOCKERFILE_VERIFY."""
    return git.run(directory, "cat-file", "-e", rev + ":" + DOCKERFILE_VERIFY).exit_code == 0


def container_usable_clone(directory: str) -> str:
    """Why a container mounting only the clone cannot use it ("" = it can):
    its .git and object store must lie inside it."""
    if not os.path.isdir(os.path.join(directory, ".git")):
        return (
            "its clone's .git is not a directory inside the clone (a linked worktree or a separate"
            " git dir), which a container mounting only the clone cannot read"
        )
    if os.path.exists(os.path.join(directory, ".git", "objects", "info", "alternates")):
        return (
            "its clone borrows objects from another store (objects/info/alternates), which a"
            " container mounting only the clone cannot read"
        )
    return ""


def open_log(path: str) -> IO[bytes]:
    """A fresh log file (truncated) whose every write appends — ours and
    those of the commands writing into it — so nothing overwrites."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_APPEND, 0o644)
    return os.fdopen(fd, "ab")


def _note(path: str, text: str) -> None:
    try:
        with open(path, "w", encoding="utf-8") as f:
            f.write(text + "\n")
    except OSError:
        pass


def ensure_image(
    rt: ContainerRuntime, tag: str, dockerfile: bytes, labels: Mapping[str, str], log_path: str, deadline: float
) -> bool:
    """Make tag present — already there, or built here (its output in
    log_path, cut at deadline, epoch seconds); answer whether this call
    built it."""
    try:
        present = rt.image_exists(tag)
    except ContainerError as err:
        why = "inspecting the image: %s" % err
        _note(log_path, "# pf verify-run: " + why)
        raise ContainerError(why) from None
    if present:
        _note(log_path, "# pf verify-run: %s is present (built by an earlier run)" % tag)
        return False
    with open_log(log_path) as logf:
        logf.write(("# pf verify-run: building %s from %s (empty build context)\n" % (tag, DOCKERFILE_VERIFY)).encode())
        logf.flush()
        start = time.monotonic()
        try:
            rt.build_image(tag, dockerfile, labels, logf, deadline - time.time())
        except ContainerError as err:
            why = str(err)
            if time.time() >= deadline:
                why += " (the build was cut at the run's deadline)"
            took = _seconds_text(round(time.monotonic() - start))
            logf.write(("\n# pf verify-run: the build failed after %s: %s\n" % (took, why)).encode())
            raise ContainerError(why) from None
        took = _seconds_text(round(time.monotonic() - start))
        logf.write(("\n# pf verify-run: built in %s\n" % took).encode())
    return True


def read_dockerfile(directory: str, rev: str = "HEAD") -> bytes:
    """DOCKERFILE_VERIFY's bytes at rev (raises git.GitError)."""
    c = git.run(directory, "show", rev + ":" + DOCKERFILE_VERIFY)
    if c.exit_code != 0:
        raise git.GitError(
            "git show %s:%s: %s" % (rev, DOCKERFILE_VERIFY, c.error or c.stderr.decode("utf-8", "replace").strip()), c
        )
    return c.stdout


def start_box(
    co: ContainerOptions,
    repo: str,
    repo_dir: str,
    log_dir: str,
    deadline: float,
    run: int,
    rel: Callable[[str], str],
    engine: str = "",
    base_root: str = "",
) -> Box:
    """Build (or find) repo's image and start its container (co must carry
    its defaults). It mounts the clone, base_root when given (a base
    worktree directory) and the directory of engine when given and outside
    the clone (read-only). rel renders a log path for the messages.
    Raises BoxError naming why."""
    rt = co.engine()
    why = container_usable_clone(repo_dir)
    if why:
        raise BoxError(why)
    try:
        dockerfile = read_dockerfile(repo_dir)
    except git.GitError as err:
        raise BoxError("%s unreadable at HEAD: %s" % (DOCKERFILE_VERIFY, err)) from None
    tag = image_tag(repo, dockerfile)
    try:
        os.makedirs(log_dir, exist_ok=True)
    except OSError as err:
        raise BoxError(str(err)) from None
    build_log = os.path.join(log_dir, "image-build.log")
    try:
        built = ensure_image(rt, tag, dockerfile, {LABEL_REPO: repo}, build_log, deadline)
    except (ContainerError, OSError) as err:
        raise BoxError("image %s from %s: %s (build log %s)" % (tag, DOCKERFILE_VERIFY, err, rel(build_log))) from None
    try:
        env = rt.image_env(tag)
    except ContainerError as err:
        raise BoxError("image %s: its environment is unreadable: %s" % (tag, err)) from None
    binds = [Bind(repo_dir)]
    if base_root:
        try:
            os.makedirs(base_root, exist_ok=True)
        except OSError as err:
            raise BoxError(str(err)) from None
        binds.append(Bind(base_root))
    tools_dir = os.path.dirname(engine)
    if engine and not within(repo_dir, tools_dir):
        binds.append(Bind(tools_dir, read_only=True))
    for b in binds:
        if "," in b.path:
            raise BoxError("the path %s holds a comma, which a container mount cannot name" % b.path)
    labels = {LABEL_DAEMON: co.owner, LABEL_BOOT: co.boot, LABEL_WI: co.wi, LABEL_RUN: str(run), LABEL_REPO: repo}
    labels.update(co.labels)
    volume = cache_volume(repo, dockerfile)
    spec = ContainerSpec(
        name=container_name(co.wi, run, repo),
        image=tag,
        labels=labels,
        binds=binds,
        volumes={volume: CONTAINER_CACHE_DIR},
        cpus=co.cpus,
        memory=co.memory,
        pids=co.pids,
    )
    if co.until > 0:
        # counted from now, not from the run's start: a build may have taken a while
        spec.lifetime = max(co.until - time.time(), 1.0)
    try:
        cid = rt.start(spec)
    except ContainerError as err:
        raise BoxError("starting %s from %s: %s" % (spec.name, tag, err)) from None
    prune_cache_volumes(rt, repo, volume, co.say or (lambda _: None))
    report = ContainerReport(
        image=tag, built=built, build_log=rel(build_log), name=spec.name,
        cpus=format_cpus(co.cpus), memory=co.memory, pids=co.pids,
    )
    return Box(rt, cid, env, report, say=co.say, engine=engine, base_root=base_root)


def _utc(ts: int) -> str:
    return datetime.datetime.fromtimestamp(ts, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def sweep_expired(rt: ContainerRuntime, owner: str, now: float, strict: bool, say: Say) -> List[str]:
    """Remove every container of owner whose pf.verify.expires has passed
    (what a run killed outright left); answer the removed ids. One still
    within its deadline may be running now and is never touched; one
    without a readable deadline is said when strict and passed over
    otherwise (a daemon's own runs carry none). A failing listing or
    removal costs only the sweep."""
    removed: List[str] = []
    try:
        found = rt.list_labelled({LABEL_DAEMON: owner}, LABEL_EXPIRES)
    except ContainerError as err:
        say("could not list earlier runs' containers (%s): none removed" % err)
        return removed
    for cid in sorted(found):
        raw = found[cid]
        if not _INT.fullmatch(raw):
            if strict:
                say(
                    "container %s is labelled %s=%s but carries no readable %s: not removed (docker rm -f %s"
                    " once no gate runs)" % (cid, LABEL_DAEMON, owner, LABEL_EXPIRES, cid)
                )
            continue
        exp = int(raw)
        if not now > exp:
            continue  # a run that may be going on now
        try:
            rt.remove(cid)
        except ContainerError as err:
            say("could not remove container %s, left by a run whose deadline passed at %s: %s" % (cid, _utc(exp), err))
            continue
        removed.append(cid)
        say("removed container %s, left by a run whose deadline passed at %s" % (cid, _utc(exp)))
    return removed
