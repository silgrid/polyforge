"""Test helpers: real git repositories, isolated from the host's git
configuration, with a fixed identity."""

import os
import shutil
import subprocess
import tempfile
import unittest
from typing import Dict, Mapping, Optional

HAVE_GIT = shutil.which("git") is not None


def git(cwd: str, *args: str) -> str:
    env = dict(os.environ)
    for k in list(env):
        if k.startswith("GIT_"):
            del env[k]
    env.update({
        "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.invalid",
        "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.invalid",
    })
    c = subprocess.run(["git", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", *args], cwd=cwd, env=env,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if c.returncode != 0:
        raise AssertionError("git %s in %s: %s" % (" ".join(args), cwd, c.stderr.decode()))
    return c.stdout.decode().strip()


def write(root: str, files: Mapping[str, str]) -> None:
    for rel, body in files.items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write(body)


class GitCase(unittest.TestCase):
    """A test with a temp directory of its own (removed after)."""

    def setUp(self) -> None:
        if not HAVE_GIT:
            self.skipTest("git not installed")
        self.tmp = tempfile.mkdtemp(prefix="pfstep-gw-")
        self.addCleanup(shutil.rmtree, self.tmp, True)

    def new_dir(self, name: str) -> str:
        d = os.path.join(self.tmp, name)
        os.makedirs(d, exist_ok=True)
        return d


class Repo:
    """One repository on branch main."""

    def __init__(self, path: str, files: Optional[Dict[str, str]] = None) -> None:
        self.dir = path
        os.makedirs(path, exist_ok=True)
        git(path, "init", "-q", "-b", "main")
        self.commit("base", files or {"base.txt": "base\n"}, allow_empty=True)

    def git(self, *args: str) -> str:
        return git(self.dir, *args)

    def commit(self, msg: str, files: Mapping[str, str], allow_empty: bool = False) -> str:
        write(self.dir, files)
        self.git("add", "-A")
        self.git("commit", "-q", "-m", msg, *(["--allow-empty"] if allow_empty else []))
        return self.git("rev-parse", "HEAD")

    def head(self) -> str:
        return self.git("rev-parse", "HEAD")
