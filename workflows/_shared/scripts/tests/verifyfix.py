"""Test helpers of verify.py: a capsule whose task repos are clones of bare
origins (the origin's main holds the base files, the clone sits on a
feature branch), a fake scope engine answering a canned report per working
directory, and one verify.py run in-process."""

import io
import json
import os
import shutil
import stat
import subprocess
import tempfile
from contextlib import redirect_stderr, redirect_stdout
from dataclasses import dataclass
from typing import Any, Dict, List, Mapping, Optional, Sequence

import verify
from pfstep.container import ContainerRuntime

from .fakebox import FakeRuntime

HAVE_GIT = shutil.which("git") is not None
HAVE_BASH = shutil.which("bash") is not None
HAVE_GO = shutil.which("go") is not None

_IDENT = {
    "GIT_AUTHOR_NAME": "vr", "GIT_AUTHOR_EMAIL": "vr@example.invalid",
    "GIT_COMMITTER_NAME": "vr", "GIT_COMMITTER_EMAIL": "vr@example.invalid",
    "GIT_CONFIG_NOSYSTEM": "1",
}


def git_t(cwd: str, *args: str) -> str:
    """git in cwd with a fixed identity; raises on failure."""
    env = dict(os.environ, **_IDENT)
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


class Fixture:
    """A capsule under root with task repos cloned from bare origins."""

    def __init__(self, root: str, repos: Mapping[str, Mapping[str, str]]) -> None:
        self.root = root
        self.capsule = os.path.join(root, "capsule")
        self.origins: Dict[str, str] = {}
        self.seeds: Dict[str, str] = {}
        ids: Dict[str, str] = {}
        for name, files in repos.items():
            origin = os.path.join(root, "origin-" + name + ".git")
            git_t(root, "init", "-q", "--bare", "-b", "main", origin)
            seed = os.path.join(root, "seed-" + name)
            git_t(root, "clone", "-q", origin, seed)
            git_t(seed, "checkout", "-q", "-B", "main")
            write_files(seed, files)
            commit_all(seed, "base")
            git_t(seed, "push", "-q", "origin", "main")
            clone = self.repo(name)
            git_t(root, "clone", "-q", origin, clone)
            git_t(clone, "config", "user.email", "vr@example.invalid")
            git_t(clone, "config", "user.name", "vr")
            git_t(clone, "checkout", "-q", "-b", "feature")
            ids[name] = git_t(clone, "rev-parse", "HEAD")
            self.origins[name], self.seeds[name] = origin, seed
        write_files(self.capsule, {"capsule.json": json.dumps({"work_item_id": "wi_x", "bundle_name": "develop", "repos": ids})})

    def repo(self, name: str) -> str:
        return os.path.join(self.capsule, "repos", name)

    def commit_in_repo(self, name: str, files: Mapping[str, str], msg: str = "change") -> str:
        """Commit files on the capsule repo's feature branch; answers HEAD."""
        write_files(self.repo(name), files)
        return commit_all(self.repo(name), msg)

    def advance_origin(self, name: str, files: Mapping[str, str], msg: str = "upstream") -> str:
        """Push a new commit on origin's main."""
        seed = self.seeds[name]
        write_files(seed, files)
        sha = commit_all(seed, msg)
        git_t(seed, "push", "-q", "origin", "main")
        return sha

    def head(self, name: str) -> str:
        return git_t(self.repo(name), "rev-parse", "HEAD")


def fake_engine(root: str, reports: Mapping[str, Any]) -> str:
    """A scope engine script answering reports[<working directory's base
    name>] (reports[<name>.full] under --full when given), logging its
    arguments to args.log beside it; answers its path."""
    d = tempfile.mkdtemp(prefix="engine-", dir=root)
    for repo, rep in reports.items():
        write_files(d, {"reports/" + repo + ".json": json.dumps(rep)})
    script = """#!/bin/bash
dir=$(cd "$(dirname "$0")" && pwd)
echo "$(basename "$PWD") $*" >>"$dir/args.log"
full=0
while [ $# -gt 0 ]; do case "$1" in --base) shift 2;; --full) full=1; shift;; *) shift;; esac; done
repo=$(basename "$PWD")
f="$dir/reports/$repo.json"
[ "$full" = 1 ] && [ -f "$dir/reports/$repo.full.json" ] && f="$dir/reports/$repo.full.json"
[ -f "$f" ] || { echo "no report for $repo" >&2; exit 3; }
cat "$f"
"""
    p = os.path.join(d, "verify-scope")
    write_files(d, {"verify-scope": script}, mode=0o755)
    return p


def engine_args(engine: str) -> List[str]:
    """The lines the fake engine logged ("<dir> <args>")."""
    try:
        with open(os.path.join(os.path.dirname(engine), "args.log"), encoding="utf-8") as f:
            return f.read().splitlines()
    except OSError:
        return []


def cmd_of(name: str, phase: str, cmd: str, **extra: Any) -> Dict[str, Any]:
    """One planned command of an engine report."""
    c: Dict[str, Any] = {"name": name, "phase": phase, "cmd": cmd, "requires_env": [], "requires_cmd": [],
                         "timeout": "20m", "source": "conf"}
    c.update(extra)
    return c


def report(mode: str, *cmds: Dict[str, Any], **extra: Any) -> Dict[str, Any]:
    """An engine report of mode (full | scoped) with the commands."""
    r: Dict[str, Any] = {
        "schema": 1, "root": "/x", "stack": "none", "analyzer": None, "conf": "present",
        "conf_changed_in_diff": False, "base": "b", "head": "h", "default_branch": "main",
        "mode": mode, "paths": ["a.txt"], "commands": list(cmds),
        "test_glob": [], "base_setup": [], "runner": None, "diagnostics": [],
    }
    r.update(extra)
    return r


@dataclass
class Run:
    """One verify.py run's answers."""

    exit: int
    stdout: str
    stderr: str
    # result: the result file (pf-step-result/1); doc: the run's result.json
    result: Dict[str, Any]
    doc: Optional[Dict[str, Any]]
    report: str

    @property
    def details(self) -> Dict[str, Any]:
        d = self.result.get("details") or {}
        assert isinstance(d, dict)
        return d

    def check(self, name: str) -> Dict[str, Any]:
        """result.json's check of that name (AssertionError when absent)."""
        assert self.doc is not None
        for c in self.doc["checks"]:
            if c["name"] == name:
                return dict(c)
        raise AssertionError("no check %r in %s" % (name, json.dumps(self.doc["checks"], indent=1)))

    def units(self, name: Optional[str] = None) -> List[Dict[str, Any]]:
        """result.json's units (of that name)."""
        assert self.doc is not None
        return [u for u in self.doc["units"] if name is None or u["name"] == name]

    def unit(self, name: str) -> Dict[str, Any]:
        us = self.units(name)
        if not us:
            raise AssertionError("no unit %r in %s" % (name, json.dumps(self.doc["units"] if self.doc else None, indent=1)))
        return us[0]


def step_env(extra: Optional[Mapping[str, str]] = None) -> Dict[str, str]:
    """A step environment: PATH and HOME of this process, the outer
    harness's selection knobs blanked, plus extra."""
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": os.environ.get("HOME", "/"),
           "E2E_RUN": "", "E2E_SKIP": ""}
    for k in ("TMPDIR", "GOCACHE", "GOPATH", "GOMODCACHE", "LANG"):
        if k in os.environ:
            env[k] = os.environ[k]
    env.update({"GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=mod"})
    if extra:
        env.update(extra)
    return env


def run_verify(
    capsule: str,
    engine: str,
    *extra: str,
    env: Optional[Mapping[str, str]] = None,
    runtime: Optional[ContainerRuntime] = None,
    out: Optional[str] = None,
) -> Run:
    """verify.py --tier light over capsule, in-process; the result file at
    out (default <capsule>/artifacts/verify/result.json). A repo carrying
    .polyforge/Dockerfile.verify runs in its container on runtime (default
    a fake engine running every container command on the host:
    tests/fakebox.py)."""
    out = out or os.path.join(capsule, "artifacts", "verify", "result.json")
    if runtime is None:
        # never the host's docker: a test's run neither probes nor sweeps it
        runtime = FakeRuntime(os.path.join(os.path.dirname(capsule), "box-tmp"))
    argv: List[str] = ["--tier", "light", "--capsule", capsule, "--engine", engine, "--out", out, *extra]
    so, se = io.StringIO(), io.StringIO()
    with redirect_stdout(so), redirect_stderr(se):
        code = verify.main(argv, step_env(env), runtime=runtime)
    with open(out, encoding="utf-8") as f:
        res = json.load(f)
    doc: Optional[Dict[str, Any]] = None
    rep = ""
    d = res.get("details") or {}
    if d.get("result"):
        p = d["result"] if os.path.isabs(d["result"]) else os.path.join(capsule, d["result"])
        if os.path.exists(p):
            with open(p, encoding="utf-8") as f:
                doc = json.load(f)
            rp = os.path.join(os.path.dirname(p), "report-mechanical.md")
            if os.path.exists(rp):
                with open(rp, encoding="utf-8") as f:
                    rep = f.read()
    return Run(exit=code, stdout=so.getvalue(), stderr=se.getvalue(), result=res, doc=doc, report=rep)


def executable(path: str, body: str) -> str:
    """Write an executable script; answers its path."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(body)
    os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return path


def lines_of(text: str) -> Sequence[str]:
    return text.split("\n")
