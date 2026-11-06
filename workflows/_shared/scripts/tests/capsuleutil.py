"""Test helpers: a capsule whose repos/<name> is a git clone with files."""

import os
import shutil
import subprocess
from typing import Dict, Mapping

HAVE_GIT = shutil.which("git") is not None

# A Dockerfile the fake engine never builds for real.
TEST_DOCKERFILE = "FROM scratch-test-image\nRUN true\n"


def _git(cwd: str, home: str, *args: str) -> str:
    env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", HOME=home,
               GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.invalid",
               GIT_COMMITTER_NAME="t", GIT_COMMITTER_EMAIL="t@example.invalid")
    return subprocess.run(["git", *args], cwd=cwd, env=env, check=True,
                          stdout=subprocess.PIPE).stdout.decode().strip()


def write_files(root: str, files: Mapping[str, str]) -> None:
    for rel, body in files.items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write(body)


def make_capsule(root: str, repo: str, files: Dict[str, str]) -> str:
    """A capsule under root whose repos/<repo> holds files in one commit;
    answers the capsule's path."""
    capsule = os.path.join(root, "capsule")
    clone = os.path.join(capsule, "repos", repo)
    os.makedirs(clone)
    _git(clone, root, "init", "-q")
    write_files(clone, files)
    commit(clone, root, "one")
    return capsule


def commit(clone: str, home: str, msg: str) -> str:
    _git(clone, home, "add", "-A")
    _git(clone, home, "commit", "-qm", msg)
    return _git(clone, home, "rev-parse", "HEAD")


def head(clone: str) -> str:
    return _git(clone, os.path.dirname(clone), "rev-parse", "HEAD")
