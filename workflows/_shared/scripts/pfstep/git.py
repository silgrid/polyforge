"""One hardened git call.

A script's git never runs a program the repository's config or the
environment names: ``-c core.fsmonitor=false -c core.hooksPath=/dev/null``
(no fsmonitor hook, no hooks), ``--no-optional-locks`` (a read takes no
index lock), ``--literal-pathspecs`` (a path is a path, never a pattern;
a read whose pathspec is a pattern on purpose, ``git grep -l <re> --
'*_test.go'``, passes ``literal_pathspecs=False``); every inherited ``GIT_*`` variable dropped (no GIT_DIR, GIT_CONFIG_* or
GIT_EXTERNAL_DIFF from the caller), ``GIT_CONFIG_NOSYSTEM=1`` (no system
config), ``GIT_TERMINAL_PROMPT=0`` (never a prompt) and ``LC_ALL=C`` (its
messages are never translated). git resolves on the environment's PATH
and runs in its own process group under a timeout (proc.run). The
daemon's twin is internal/polyforge/taskrepo's Git.
"""

import os
import shutil
import tempfile
from typing import Dict, List, Mapping, Optional

from . import proc

# READ_TIMEOUT bounds one git read (seconds).
READ_TIMEOUT = 10.0

HARDENING = (
    "-c",
    "core.fsmonitor=false",
    "-c",
    "core.hooksPath=/dev/null",
    "--no-optional-locks",
    "--literal-pathspecs",
)


class GitError(Exception):
    """A git call that did not exit 0: "git <args>: <its stderr>"."""

    def __init__(self, message: str, completed: proc.Completed) -> None:
        super().__init__(message)
        self.completed = completed


def env(environ: Optional[Mapping[str, str]] = None) -> Dict[str, str]:
    """The environment git runs under (see the module's doc)."""
    base = os.environ if environ is None else environ
    out = {k: v for k, v in base.items() if not k.startswith("GIT_") and k != "LC_ALL"}
    out["GIT_CONFIG_NOSYSTEM"] = "1"
    out["GIT_TERMINAL_PROMPT"] = "0"
    out["LC_ALL"] = "C"
    return out


def argv(*args: str, literal_pathspecs: bool = True) -> List[str]:
    """git's argv: the hardening flags, then args (without
    ``--literal-pathspecs`` when literal_pathspecs is False)."""
    hardening = HARDENING if literal_pathspecs else tuple(h for h in HARDENING if h != "--literal-pathspecs")
    return ["git", *hardening, *args]


def run(
    cwd: str,
    *args: str,
    timeout: float = READ_TIMEOUT,
    environ: Optional[Mapping[str, str]] = None,
    input: Optional[bytes] = None,
    literal_pathspecs: bool = True,
) -> proc.Completed:
    """Run one hardened git in cwd and answer how it ended (no raise)."""
    return proc.run(argv(*args, literal_pathspecs=literal_pathspecs), cwd=cwd, env=env(environ), timeout=timeout, input=input)


def git(
    cwd: str,
    *args: str,
    timeout: float = READ_TIMEOUT,
    environ: Optional[Mapping[str, str]] = None,
    input: Optional[bytes] = None,
    literal_pathspecs: bool = True,
) -> str:
    """Run one hardened git in cwd and answer its stdout; GitError otherwise."""
    c = run(cwd, *args, timeout=timeout, environ=environ, input=input, literal_pathspecs=literal_pathspecs)
    if c.exit_code == 0:
        return c.stdout.decode("utf-8", "replace")
    if c.timed_out:
        msg = "timed out after %gs" % timeout
    elif c.error:
        msg = c.error
    else:
        msg = c.stderr.decode("utf-8", "replace").strip() or "exit %d" % c.exit_code
    raise GitError("git %s: %s" % (" ".join(args), msg), c)


def worktree_tree(cwd: str, subdir: str = "", timeout: float = READ_TIMEOUT) -> str:
    """The working tree's own tree hash — tracked modifications plus every
    untracked, unignored file, the state a build's uncommitted work leaves
    (Q1 of polyforge#182) — computed through a private index (``git add -A``
    over a copy of the repository's index, then ``git write-tree``; the
    repository's own index is never touched, and a file outside the tree
    never moves it), or the hash of the whole tree's ``<subdir>`` subtree
    ("" = the whole tree, nested roots included). GitError when git cannot
    answer — the caller degrades to running everything, never to judging
    nothing."""
    fd, index = tempfile.mkstemp(prefix="pf-worktree-index-")
    os.close(fd)
    os.unlink(index)  # git creates the index itself; an empty file it would refuse
    e = env()
    e["GIT_INDEX_FILE"] = index
    try:
        # 保留已跟踪状态，否则后来匹配忽略规则的输入会从临时索引消失。
        source = git(cwd, "rev-parse", "--git-path", "index", timeout=timeout).strip()
        if not os.path.isabs(source):
            source = os.path.join(cwd, source)
        if os.path.exists(source):
            shutil.copyfile(source, index)
        c = proc.run(argv("add", "-A"), cwd=cwd, env=e, timeout=timeout)
        if c.exit_code != 0:
            raise GitError("git add -A (a private index of the working tree): %s"
                           % (c.stderr.decode("utf-8", "replace").strip() or "exit %d" % c.exit_code), c)
        c = proc.run(argv("write-tree"), cwd=cwd, env=e, timeout=timeout)
        if c.exit_code != 0 or not c.stdout.decode("utf-8", "replace").strip():
            raise GitError("git write-tree (a private index of the working tree): exit %d" % c.exit_code, c)
        tree = c.stdout.decode("utf-8", "replace").strip()
        if not subdir:
            return tree
        c = proc.run(argv("rev-parse", "--verify", "%s:%s" % (tree, subdir)), cwd=cwd,
                     env=env(), timeout=timeout)
        if c.exit_code != 0 or not c.stdout.decode("utf-8", "replace").strip():
            raise GitError("git rev-parse %s:%s: the subtree hash is unreadable" % (tree, subdir), c)
        return c.stdout.decode("utf-8", "replace").strip()
    finally:
        try:
            os.unlink(index)
        except OSError:
            pass
