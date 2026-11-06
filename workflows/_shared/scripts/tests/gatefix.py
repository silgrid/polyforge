"""Test helpers of gate.py: a plain clone carrying the tree's real scope
engine (copied from this repository), its conf and (when asked) its
Dockerfile, all committed; one gate run on it through tests.fakebox's fake
container engine; the gate script run in-process as the skill runs it."""

import io
import json
import os
import shutil
import subprocess
import time
from contextlib import contextmanager, redirect_stderr, redirect_stdout
from dataclasses import dataclass
from typing import Any, Dict, Iterator, List, Mapping, Optional

import gate
from pfstep import container, duration

from .fakebox import FakeRuntime

HAVE_GIT = shutil.which("git") is not None
HAVE_BASH = shutil.which("bash") is not None
HAVE_PY = shutil.which("python3") is not None

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(SCRIPTS)))
# the tree's engine and gate id, as the gate finds them in a clone
ENGINE_SRC = os.path.join(REPO_ROOT, gate.GATE_ENGINE_REL)
GATE_ID_SRC = os.path.join(REPO_ROOT, "tools", "gate-id")

GATE_DOCKERFILE = "FROM gate-test-image\nRUN true\n"

IDENT = {
    "GIT_AUTHOR_NAME": "gt", "GIT_AUTHOR_EMAIL": "gt@example.invalid",
    "GIT_COMMITTER_NAME": "gt", "GIT_COMMITTER_EMAIL": "gt@example.invalid",
    "GIT_CONFIG_NOSYSTEM": "1",
}

# a step environment's secrets and host-only names the container never sees
HOST_ONLY = {"LANG": "C.UTF-8", "PF_SECRET": "s3", "TMUX": "/tmp/tmux-0/default,1,0", "GH_TOKEN": "gh-secret"}


def git_t(cwd: str, *args: str) -> str:
    """git in cwd with a fixed identity; raises on failure."""
    env = dict(os.environ, **IDENT)
    return subprocess.run(["git", *args], cwd=cwd, env=env, check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.PIPE).stdout.decode().strip()


def write_files(root: str, files: Mapping[str, str], mode: int = 0o644) -> None:
    for rel, body in files.items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write(body)
        os.chmod(p, mode)


def commit_all(directory: str, msg: str) -> str:
    git_t(directory, "add", "-A")
    git_t(directory, "commit", "-q", "-m", msg)
    return git_t(directory, "rev-parse", "HEAD")


def gate_tree(directory: str, conf: str, dockerfile: bool = True) -> None:
    """The gate's tree in directory: the real engine and gate id, the conf
    and (when dockerfile) the Dockerfile."""
    with open(ENGINE_SRC, encoding="utf-8") as f:
        write_files(directory, {gate.GATE_ENGINE_REL: f.read()}, 0o755)
    with open(GATE_ID_SRC, encoding="utf-8") as f:
        write_files(directory, {"tools/gate-id": f.read()}, 0o755)
    write_files(directory, {".polyforge/verify-scope.conf": conf, "README": "x\n"})
    if dockerfile:
        write_files(directory, {container.DOCKERFILE_VERIFY: GATE_DOCKERFILE})


def gate_clone(root: str, conf: str, dockerfile: bool = True, name: str = "app") -> str:
    """A plain clone named name under root carrying the gate tree, committed."""
    d = os.path.join(root, name)
    os.makedirs(d)
    git_t(d, "init", "-q", "-b", "main")
    gate_tree(d, conf, dockerfile)
    commit_all(d, "tree")
    return d


def green_clone(root: str, conf: str) -> str:
    """gate_clone with a bare origin it pushed main to (origin.git beside it)."""
    origin = os.path.join(root, "origin.git")
    git_t(root, "init", "-q", "--bare", "-b", "main", origin)
    d = gate_clone(root, conf)
    git_t(d, "remote", "add", "origin", origin)
    git_t(d, "push", "-q", "origin", "main")
    return d


def engine_plan(directory: str) -> List[str]:
    """The commands the tree's engine plans for the gate, asked on the host."""
    out = subprocess.run([os.path.join(directory, gate.GATE_ENGINE_REL), "--json", "--full", "--base", "HEAD"],
                         cwd=directory, check=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL).stdout
    from pfstep.engine import TIER_FULL, choose, decode_report
    return [c.cmd for c in choose(decode_report(out), TIER_FULL)[0]]


def boxed_bash(fb: FakeRuntime) -> List[str]:
    """The `bash -c` commands the fake engine ran, in order."""
    out = []
    for _, _, _, argv in fb.execs:
        if "bash" in argv:
            i = argv.index("bash")
            if i + 2 < len(argv) and argv[i + 1] == "-c":
                out.append(argv[-1])
    return out


@dataclass
class GateRun:
    res: gate.GateResult
    progress: str


def new_gate(fb: container.ContainerRuntime, env: Optional[Mapping[str, str]] = None, deadline: int = 0,
             keep_going: bool = False, cpus: float = 0.0, memory: str = "", pids: int = 0,
             command_timeout: int = gate.DEFAULT_COMMAND_TIMEOUT, lines: Optional[List[str]] = None,
             owner: str = container.GATE_OWNER) -> gate.Gate:
    """A Gate on fb (the options as gate.run builds them), its progress
    lines appended to lines."""
    environ = dict(os.environ)
    environ.update(HOST_ONLY)
    environ.update(env or {})
    window = deadline or gate.DEFAULT_GATE_DEADLINE
    until = time.time() + window / duration.SECOND
    out = lines if lines is not None else []
    co = container.for_run(container.ContainerOptions(runtime=fb, owner=owner, wi=gate.GATE_WI, cpus=cpus, memory=memory,
                                                      pids=pids, say=out.append), until, environ)
    return gate.Gate(co, until, window, command_timeout, keep_going, environ, out.append)


def run_gate(directory: str, fb: container.ContainerRuntime, log_dir: str, plan_only: bool = False, **kw: Any) -> GateRun:
    lines: List[str] = []
    g = new_gate(fb, lines=lines, **kw)
    res = g.run(directory, "", log_dir, plan_only=plan_only)
    return GateRun(res=res, progress="\n".join(lines))


@dataclass
class Main:
    exit: int
    stdout: str
    stderr: str


@contextmanager
def chdir(d: str) -> Iterator[None]:
    old = os.getcwd()
    os.chdir(d)
    try:
        yield
    finally:
        os.chdir(old)


def run_main(argv: List[str], cwd: str, env: Optional[Mapping[str, str]] = None,
             runtime: Optional[container.ContainerRuntime] = None) -> Main:
    """gate.py's main in-process from cwd, under the process environment plus env."""
    environ = dict(os.environ)
    for k in ("PF_WI_ID", "PF_TOOL_WINDOW_MS", "BASH_MAX_TIMEOUT_MS", "PF_ROUND"):
        environ.pop(k, None)
    environ.update(IDENT)
    environ.update(env or {})
    so, se = io.StringIO(), io.StringIO()
    with chdir(cwd), redirect_stdout(so), redirect_stderr(se):
        code = gate.main(argv, environ, runtime=runtime, stdout=so, stderr=se)
    return Main(exit=code, stdout=so.getvalue(), stderr=se.getvalue())


def read_json(path: str) -> Dict[str, Any]:
    with open(path, encoding="utf-8") as f:
        out: Dict[str, Any] = json.load(f)
    return out
