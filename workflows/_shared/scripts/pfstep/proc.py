"""Run one command in its own session, and kill its process group whole.

The command becomes the leader of a new session (``start_new_session=True``:
``setsid()``), so it also leads its own process group, and it has no
controlling terminal: an interactive shell reads its pipe, not a terminal,
and a command that opens ``/dev/tty`` fails at once (ENXIO) instead of
waiting on the terminal the daemon was started from. When its timeout
passes, the whole group is killed (SIGKILL) and the exit code is 124; when
the leader exits, whatever it left running in its group (a server a test
forgot, a background job) is killed too, so nothing it started outlives the
call. A command killed by signal n exits 128+n, and one that cannot start
exits 127. Output is captured through temp files, never pipes, so a
grandchild holding a pipe open cannot hang the call.

Every command keeps the step markers PF_WI_ID / PF_DAEMON_ID (the workflow
contract's "Scripts" section): the daemon's sweep at the step's end finds a
process by them. ``child_env`` adds them back to an environment that
dropped them. Ported from the Go step commands' execcmd.go.
"""

import os
import signal
import subprocess
import tempfile
from dataclasses import dataclass
from typing import IO, Dict, List, Mapping, Optional, Sequence, Union

from .stepenv import MARKERS

TIMEOUT_EXIT = 124
NOT_STARTED_EXIT = 127


@dataclass
class Completed:
    """How one command ended."""

    argv: List[str]
    exit_code: int
    stdout: bytes = b""
    stderr: bytes = b""
    timed_out: bool = False
    # signal is the signal that ended the leader (0 = it exited), timeout
    # kills excluded.
    signal: int = 0
    # error says why the command could not start ("" = it started).
    error: str = ""
    pid: int = 0


def child_env(env: Optional[Mapping[str, str]] = None) -> Dict[str, str]:
    """env (default the process's) with the step markers kept: a marker the
    process carries and env lacks is added back."""
    out = dict(os.environ if env is None else env)
    for name in MARKERS:
        if name not in out and name in os.environ:
            out[name] = os.environ[name]
    return out


def killpg(pgid: int) -> None:
    """SIGKILL the process group pgid; a group already gone is no error."""
    try:
        os.killpg(pgid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass


def run(
    argv: Sequence[str],
    cwd: Optional[str] = None,
    env: Optional[Mapping[str, str]] = None,
    timeout: Optional[float] = None,
    input: Optional[bytes] = None,
    stdout: Optional[IO[bytes]] = None,
    stderr: Optional[IO[bytes]] = None,
    merge_stderr: bool = False,
) -> Completed:
    """Run argv (no shell) and answer how it ended.

    timeout is in seconds (None = none). stdout/stderr given as binary
    files receive the streams and nothing is captured for them; otherwise
    each is captured into Completed. merge_stderr sends stderr where
    stdout goes.
    """
    args = list(argv)
    with tempfile.TemporaryFile() as cap_out, tempfile.TemporaryFile() as cap_err, tempfile.TemporaryFile() as feed:
        if input is not None:
            feed.write(input)
            feed.flush()
            feed.seek(0)
        out_target = stdout if stdout is not None else cap_out
        err_target: Union[int, IO[bytes]] = (
            subprocess.STDOUT if merge_stderr else (stderr if stderr is not None else cap_err)
        )
        try:
            p = subprocess.Popen(
                args,
                cwd=cwd,
                env=child_env(env),
                stdin=feed if input is not None else subprocess.DEVNULL,
                stdout=out_target,
                stderr=err_target,
                start_new_session=True,
                close_fds=True,
            )
        except OSError as err:
            return Completed(argv=args, exit_code=NOT_STARTED_EXIT, error=str(err))
        timed_out = False
        try:
            p.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            timed_out = True
            killpg(p.pid)
            p.wait()
        except BaseException:
            killpg(p.pid)
            p.wait()
            raise
        # what the leader left running in its group dies with it
        killpg(p.pid)
        rc = p.returncode
        sig = -rc if rc < 0 else 0
        if timed_out:
            code, sig = TIMEOUT_EXIT, 0
        elif sig:
            code = 128 + sig
        else:
            code = rc
        captured_out = b""
        captured_err = b""
        if stdout is None:
            cap_out.seek(0)
            captured_out = cap_out.read()
        if stderr is None and not merge_stderr:
            cap_err.seek(0)
            captured_err = cap_err.read()
        return Completed(
            argv=args,
            exit_code=code,
            stdout=captured_out,
            stderr=captured_err,
            timed_out=timed_out,
            signal=sig,
            pid=p.pid,
        )
