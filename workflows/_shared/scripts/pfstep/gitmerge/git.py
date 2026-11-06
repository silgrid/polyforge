"""git and gh for the merge mechanics, in a task repo's clone.

Every git call is pfstep's hardened one (``pfstep.git``: no fsmonitor, no
hooks, no optional locks, literal pathspecs, every inherited ``GIT_*``
dropped, ``LC_ALL=C``) run as the lane's own identity — ``polyforge daemon
<daemon@polyforge.invalid>`` as author and committer, set in the
environment so neither the host's git configuration nor a missing one
decides who a merge or a revert is by. gh runs under the step's command
environment (the machine's own credentials, never a step's), prompts
disabled. Both resolve on the environment's PATH, else on the process's
PATH.

Each call is bounded by ten minutes and by what is left of the run's
context (ctx.py); a call that fails raises ``CmdError`` worded as the Go
step commands worded it: ``<name> <args>: <why>: <stderr>``.
"""

import json
import os
import shutil
import tempfile
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Mapping, Optional, TypeVar

from .. import git as hardened
from .. import proc
from ..runner import Interrupted
from . import gostr
from .ctx import Ctx, GoError

IDENTITY_NAME = "polyforge daemon"
IDENTITY_EMAIL = "daemon@polyforge.invalid"
# The lane's identity as a tagger or an author line names it.
IDENTITY = IDENTITY_NAME + " <" + IDENTITY_EMAIL + ">"

IDENTITY_ENV = {
    "GIT_AUTHOR_NAME": IDENTITY_NAME,
    "GIT_AUTHOR_EMAIL": IDENTITY_EMAIL,
    "GIT_COMMITTER_NAME": IDENTITY_NAME,
    "GIT_COMMITTER_EMAIL": IDENTITY_EMAIL,
}

# One call's bound (seconds).
CALL_TIMEOUT = 600.0

# The merge attribute override (pin_attributes): every path merges with the
# text driver (which still treats binary content as binary) and default
# conflict markers, whatever a branch's .gitattributes says — merge=union
# would keep both sides of a real conflict silently, a conflict-marker-size
# would hide a conflicted list's markers.
LANE_ATTRIBUTES = b"* merge=text -conflict-marker-size\n"

T = TypeVar("T")


class CmdError(GoError):
    """A git or gh call that failed; stdout is what it printed."""

    def __init__(self, message: str, stdout: bytes = b"", exit_code: int = -1) -> None:
        super().__init__(message)
        self.stdout = stdout
        self.exit_code = exit_code


def _text(b: bytes) -> str:
    return b.decode("utf-8", "surrogateescape")


@dataclass
class PR:
    """A pull request as gh reports it."""

    state: str = ""
    head_ref_name: str = ""
    base_ref_name: str = ""
    merge_commit: str = ""

    def mismatch(self, base: str, branch: str) -> str:
        """Why an OPEN PR is not the lane's PR of branch into base ("" = it is)."""
        if self.base_ref_name != base:
            return "targets %s, not the default branch %s" % (self.base_ref_name, base)
        if self.head_ref_name != branch:
            return "heads the branch %s, not the published %s" % (self.head_ref_name, branch)
        return ""


@dataclass
class BranchPR:
    """The newest PR of a head branch, as gh lists it."""

    url: str = ""
    state: str = ""
    head_ref_oid: str = ""
    merge_commit: Optional[str] = None


def _oid(v: Any) -> str:
    oid = v.get("oid") if isinstance(v, dict) else None
    return oid if isinstance(oid, str) else ""


class Git:
    """git and gh in a clone, under environ (default the process's) and ctx."""

    def __init__(self, ctx: Ctx, environ: Optional[Mapping[str, str]] = None, timeout: float = CALL_TIMEOUT) -> None:
        self.ctx = ctx
        self.environ: Dict[str, str] = dict(os.environ if environ is None else environ)
        self.timeout = timeout

    def with_ctx(self, ctx: Ctx) -> "Git":
        """The same runner under another context."""
        return Git(ctx, self.environ, self.timeout)

    def _resolve(self, name: str) -> str:
        p = hardened_look_path(name, self.environ.get("PATH", ""))
        if p:
            return p
        return shutil.which(name) or name

    def cmd(self, cwd: str, name: str, *args: str) -> bytes:
        """Run name with args in cwd; its stdout, or CmdError."""
        what = name + " " + " ".join(args)
        why = self.ctx.err()
        if why is not None:
            raise CmdError("%s: %s: " % (what, why))
        timeout = self.timeout
        rem = self.ctx.remaining()
        if rem is not None and rem < timeout:
            timeout = rem
        if name == "git":
            env = hardened.env(self.environ)
            env.update(IDENTITY_ENV)
            argv = hardened.argv(*args)
        else:
            env = dict(self.environ)
            env["GIT_TERMINAL_PROMPT"] = "0"
            argv = [name, *args]
        env["GH_PROMPT_DISABLED"] = "1"
        argv[0] = self._resolve(name)
        try:
            c = proc.run(argv, cwd=cwd, env=env, timeout=max(timeout, 0.001))
        except (KeyboardInterrupt, Interrupted):
            # the interrupt cancels the run: the command was killed with its
            # group, every later call fails at once
            self.ctx.cancel()
            raise CmdError("%s: signal: killed: " % what) from None
        if c.error:
            if c.exit_code == proc.NOT_STARTED_EXIT and os.path.sep not in argv[0]:
                raise CmdError('%s: exec: "%s": executable file not found in $PATH: ' % (what, name))
            raise CmdError("%s: %s: " % (what, c.error))
        if c.timed_out:
            raise CmdError("%s: signal: killed: %s" % (what, _text(c.stderr).strip()), c.stdout, c.exit_code)
        if c.exit_code != 0:
            if c.signal:
                why = "signal: " + _signal_name(c.signal)
            else:
                why = "exit status %d" % c.exit_code
            raise CmdError("%s: %s: %s" % (what, why, _text(c.stderr).strip()), c.stdout, c.exit_code)
        return c.stdout

    def git(self, cwd: str, *args: str) -> str:
        """One git call's stdout (as text)."""
        return _text(self.cmd(cwd, "git", *args))

    run = git

    def rev(self, cwd: str, rev: str) -> str:
        """The commit a revision names."""
        return self.git(cwd, "rev-parse", "--verify", "--quiet", rev + "^{commit}").strip()

    def is_ancestor(self, cwd: str, a: str, b: str) -> bool:
        try:
            self.git(cwd, "merge-base", "--is-ancestor", a, b)
        except GoError:
            return False
        return True

    def default_branch(self, cwd: str) -> str:
        """origin's default branch (origin/HEAD, else main)."""
        try:
            out = self.git(cwd, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
        except GoError:
            return "main"
        b = out.strip()
        if b.startswith("origin/"):
            b = b[len("origin/"):]
        return b or "main"

    def fetch(self, cwd: str, *branches: str) -> None:
        """Fetch origin's named branches into refs/remotes/origin/*, and the
        green tags."""
        args = ["fetch", "-q", "origin", "+refs/tags/green/*:refs/tags/green/*"]
        args += ["+refs/heads/" + b + ":refs/remotes/origin/" + b for b in branches]
        self.git(cwd, *args)

    def checkout(self, cwd: str, rev: str) -> None:
        """The worktree on a detached, clean commit."""
        try:
            self.git(cwd, "merge", "--abort")
        except GoError:
            pass
        self.git(cwd, "checkout", "-q", "-f", "--detach", rev)
        self.git(cwd, "reset", "-q", "--hard", rev)
        self.git(cwd, "clean", "-q", "-fd")

    def first_parents(self, cwd: str, frm: str, to: str) -> List[str]:
        """The first-parent commits of to since frm (oldest first, frm
        excluded); frm "" answers the newest 500."""
        rng = ["--max-count=500", to] if frm == "" else [frm + ".." + to]
        return self.git(cwd, "rev-list", "--first-parent", "--reverse", *rng).split()

    def push(self, cwd: str, branch: str) -> None:
        """HEAD to origin's branch (never forced)."""
        self.git(cwd, "push", "-q", "origin", "HEAD:refs/heads/" + branch)

    def _attributes_path(self, cwd: str) -> str:
        p = self.git(cwd, "rev-parse", "--git-path", "info/attributes").strip()
        if not os.path.isabs(p):
            p = os.path.join(cwd, p)
        return p

    def pin_attributes(self, cwd: str) -> None:
        """LANE_ATTRIBUTES into the clone's $GIT_DIR/info/attributes, which
        takes precedence over every in-tree .gitattributes."""
        p = self._attributes_path(cwd)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "wb") as f:
            f.write(LANE_ATTRIBUTES)

    def keep_attributes(self, cwd: str, fn: Callable[[], T]) -> T:
        """fn(), then the clone's $GIT_DIR/info/attributes exactly as found —
        the same bytes, or no file when there was none — however fn ended. A
        file holding exactly the override is one a crash left between the pin
        and this restore: it is removed."""
        p = self._attributes_path(cwd)
        before: Optional[bytes]
        try:
            with open(p, "rb") as f:
                before = f.read()
        except FileNotFoundError:
            before = None
        if before == LANE_ATTRIBUTES:
            before = None
        ok = False
        try:
            out = fn()
            ok = True
        finally:
            try:
                if before is not None:
                    with open(p, "wb") as f:
                        f.write(before)
                else:
                    try:
                        os.remove(p)
                    except FileNotFoundError:
                        pass
            except OSError as err:
                if ok:
                    raise GoError("restoring %s: %s" % (p, err)) from None
        return out

    # -- gh --

    def view_pr(self, cwd: str, url: str) -> PR:
        out = _text(self.cmd(cwd, "gh", "pr", "view", url, "--json", "state,headRefName,baseRefName,mergeCommit"))
        try:
            v = json.loads(out)
            if not isinstance(v, dict):
                raise ValueError("not an object")
        except ValueError as err:
            raise GoError("gh pr view %s: unreadable answer %s: %s" % (url, gostr.quote(out.strip()), _json_why(out, err))) from None
        return PR(state=_str(v.get("state")), head_ref_name=_str(v.get("headRefName")),
                  base_ref_name=_str(v.get("baseRefName")), merge_commit=_oid(v.get("mergeCommit")))

    def merge_pr(self, cwd: str, url: str, head: str) -> None:
        """Merge a PR with a merge commit, only while its head is head
        (``--match-head-commit``)."""
        if head == "":
            raise GoError("gh pr merge %s: no head commit to match" % url)
        self.cmd(cwd, "gh", "pr", "merge", url, "--merge", "--match-head-commit", head)

    def create_pr(self, cwd: str, base: str, head: str, title: str, body: str) -> str:
        """Open a PR; its URL."""
        fd, path = tempfile.mkstemp(prefix="pf-integrate-pr-", suffix=".md")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as f:
                f.write(body)
            out = _text(self.cmd(cwd, "gh", "pr", "create", "--base", base, "--head", head, "--title", title, "--body-file", path))
        finally:
            try:
                os.remove(path)
            except OSError:
                pass
        words = out.split()
        if not words:
            raise GoError("gh pr create answered no URL")
        return words[-1]

    def find_pr(self, cwd: str, branch: str) -> Optional[BranchPR]:
        """The newest PR whose head is branch (None = none)."""
        out = _text(self.cmd(cwd, "gh", "pr", "list", "--head", branch, "--state", "all", "--json", "url,state,headRefOid,mergeCommit"))
        try:
            v = json.loads(out)
            if not isinstance(v, list):
                raise ValueError("not an array")
        except ValueError as err:
            raise GoError("gh pr list --head %s: unreadable answer %s: %s" % (branch, gostr.quote(out.strip()), _json_why(out, err))) from None
        if not v:
            return None
        p = v[0] if isinstance(v[0], dict) else {}
        mc = p.get("mergeCommit")
        return BranchPR(url=_str(p.get("url")), state=_str(p.get("state")), head_ref_oid=_str(p.get("headRefOid")),
                        merge_commit=_oid(mc) if isinstance(mc, dict) else None)


def _str(v: Any) -> str:
    return v if isinstance(v, str) else ""


def _json_why(out: str, err: Exception) -> str:
    if out.strip() == "":
        return "unexpected end of JSON input"
    return str(err)


def _signal_name(sig: int) -> str:
    names = {1: "hangup", 2: "interrupt", 6: "aborted", 9: "killed", 13: "broken pipe", 15: "terminated"}
    return names.get(sig, "signal %d" % sig)


def hardened_look_path(name: str, path: str) -> str:
    """Where name resolves on the PATH value path ("" = nowhere): the first
    entry holding an executable regular file of that name."""
    for d in path.split(os.pathsep):
        if not d:
            continue
        p = os.path.join(d, name)
        if os.path.isfile(p) and os.access(p, os.X_OK):
            return p
    return ""


def paths(z: str) -> List[str]:
    """The paths of a ``-z`` listing of git names, in order and each once: a
    path is read verbatim — a space, a newline or a non-ASCII byte in it is
    no separator and is never quoted."""
    out: List[str] = []
    for p in z.split("\x00"):
        if p and p not in out:
            out.append(p)
    return out

