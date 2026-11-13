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
