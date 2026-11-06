"""pfstep.proc: a command runs as the leader of its own session (so of its
own process group too), without a controlling terminal; its timeout kills
the whole group (grandchildren included) and exits 124; what it leaves
running after it exits is killed too; a signal death exits 128+n; a command
that cannot start exits 127; the step markers reach the child."""

import json
import os
import pty
import select
import signal
import sys
import tempfile
import time
import unittest
from typing import Any, Dict, List
from unittest import mock

from pfstep import proc

from .procutil import alive


def _wait_gone(pid: int, bound: float = 5.0) -> bool:
    end = time.monotonic() + bound
    while time.monotonic() < end:
        if not alive(pid):
            return True
        time.sleep(0.05)
    return not alive(pid)


# GRANDCHILD forks a child that reports "<pid> <sid>" through a pipe once
# fork has returned in it, then becomes sleep 300; the leader writes that
# line to argv[1] and sleeps until it is killed.
GRANDCHILD = r"""
import os, sys, time
r, w = os.pipe()
if os.fork() == 0:
    os.close(r)
    os.write(w, ("%d %d\n" % (os.getpid(), os.getsid(0))).encode())
    os.close(w)
    os.execvp("sleep", ["sleep", "300"])
os.close(w)
line = b""
while not line.endswith(b"\n"):
    chunk = os.read(r, 100)
    if not chunk:
        break
    line += chunk
with open(sys.argv[1], "wb") as f:
    f.write(line)
time.sleep(300)
"""


def _run_on_a_terminal(argv: List[str], timeout: float) -> Dict[str, Any]:
    """Call proc.run(argv, timeout=timeout) from a child that leads a session
    whose controlling terminal is a pty (as a daemon started from a terminal
    does) and answer how the command ended: exit_code, timed_out, stdout,
    secs. exit_code None: the child gave no answer within timeout + 15 s."""
    rfd, wfd = os.pipe()
    pid, master = pty.fork()
    if pid == 0:
        try:
            os.close(rfd)
            start = time.monotonic()
            c = proc.run(argv, timeout=timeout)
            data = json.dumps(
                {
                    "exit_code": c.exit_code,
                    "timed_out": c.timed_out,
                    "stdout": c.stdout.decode("utf-8", "replace"),
                    "secs": time.monotonic() - start,
                }
            ).encode()
            while data:
                data = data[os.write(wfd, data) :]
        finally:
            os._exit(0)
    os.close(wfd)
    got = b""
    done = False
    deadline = time.monotonic() + timeout + 15
    fds = [master, rfd]
    try:
        # drain the terminal too (a shell writes its prompts there) so the
        # child never blocks on a full pty
        while not done:
            left = deadline - time.monotonic()
            if left <= 0:
                break
            ready, _, _ = select.select(fds, [], [], left)
            for fd in ready:
                try:
                    chunk = os.read(fd, 65536)
                except OSError:  # EIO: the terminal's last user is gone
                    chunk = b""
                if fd == rfd:
                    if chunk:
                        got += chunk
                    else:
                        done = True
                elif not chunk:
                    fds.remove(master)
    finally:
        os.close(master)
        os.close(rfd)
        if not done:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        os.waitpid(pid, 0)
    if not done or not got:
        return {"exit_code": None}
    answer: Dict[str, Any] = json.loads(got)
    return answer


class ProcTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.dir = self._td.name

    def tearDown(self) -> None:
        self._td.cleanup()

    def test_exit_code_and_streams(self) -> None:
        c = proc.run(["sh", "-c", "echo out; echo err >&2; exit 3"])
        self.assertEqual((c.exit_code, c.stdout, c.stderr, c.timed_out, c.signal), (3, b"out\n", b"err\n", False, 0))
        merged = proc.run(["sh", "-c", "echo out; echo err >&2"], merge_stderr=True)
        self.assertEqual(sorted(merged.stdout.split()), [b"err", b"out"])
        fed = proc.run(["cat"], input=b"fed\n")
        self.assertEqual(fed.stdout, b"fed\n")
        self.assertEqual(proc.run(["cat"]).stdout, b"", "stdin is /dev/null by default")

    def test_own_process_group(self) -> None:
        c = proc.run([sys.executable, "-c", "import os; print(os.getpgid(0) == os.getpid())"])
        self.assertEqual(c.stdout.strip(), b"True")

    def test_timeout_kills_the_grandchild(self) -> None:
        pidfile = os.path.join(self.dir, "pid")
        start = time.monotonic()
        c = proc.run(["sh", "-c", 'sleep 300 & echo $! > "$1"; wait', "sh", pidfile], timeout=1.0)
        self.assertLess(time.monotonic() - start, 30)
        self.assertEqual((c.exit_code, c.timed_out), (proc.TIMEOUT_EXIT, True))
        with open(pidfile, encoding="utf-8") as f:
            grandchild = int(f.read())
        self.assertTrue(_wait_gone(grandchild), "the grandchild outlived its command's timeout")

    def test_timeout_kills_the_grandchild_in_its_own_session(self) -> None:
        report = os.path.join(self.dir, "report")
        c = proc.run([sys.executable, "-c", GRANDCHILD, report], timeout=3.0)
        self.assertEqual((c.exit_code, c.timed_out), (proc.TIMEOUT_EXIT, True))
        with open(report, encoding="utf-8") as f:
            grandchild, sid = (int(v) for v in f.read().split())
        self.assertEqual(sid, c.pid, "the command does not lead a session of its own")
        self.assertTrue(_wait_gone(grandchild), "the grandchild outlived its command's timeout")

    def test_interactive_shell_reads_its_pipe_on_a_terminal(self) -> None:
        got = _run_on_a_terminal(["sh", "-c", "printf 'echo MARK\\n' | sh -i"], timeout=5.0)
        self.assertEqual((got["exit_code"], "MARK" in got.get("stdout", "")), (0, True), got)

    def test_dev_tty_fails_at_once_on_a_terminal(self) -> None:
        got = _run_on_a_terminal(["sh", "-c", "cat /dev/tty"], timeout=5.0)
        self.assertIsNotNone(got["exit_code"], got)
        self.assertFalse(got["timed_out"], got)
        self.assertNotEqual(got["exit_code"], 0, got)

    def test_leftovers_die_when_the_leader_exits(self) -> None:
        start = time.monotonic()
        c = proc.run(["sh", "-c", "sleep 300 >/dev/null 2>&1 & echo $!"])
        self.assertLess(time.monotonic() - start, 30, "the call waited for a background job")
        self.assertEqual(c.exit_code, 0)
        self.assertTrue(_wait_gone(int(c.stdout)), "a background job outlived its command")

    def test_signal_death_is_128_plus_n(self) -> None:
        c = proc.run(["sh", "-c", "kill -TERM $$"])
        self.assertEqual((c.exit_code, c.signal, c.timed_out), (128 + signal.SIGTERM, signal.SIGTERM, False))

    def test_not_startable_is_127(self) -> None:
        c = proc.run([os.path.join(self.dir, "no-such-command")])
        self.assertEqual(c.exit_code, proc.NOT_STARTED_EXIT)
        self.assertNotEqual(c.error, "")

    def test_step_markers_are_kept(self) -> None:
        with mock.patch.dict(os.environ, {"PF_WI_ID": "wi_x", "PF_DAEMON_ID": "m-x"}):
            c = proc.run(["sh", "-c", 'printf "%s %s" "$PF_WI_ID" "$PF_DAEMON_ID"'], env={"PATH": os.environ["PATH"]})
            self.assertEqual(c.stdout, b"wi_x m-x")
            # an explicit value is the caller's
            c = proc.run(["sh", "-c", 'printf "%s" "$PF_WI_ID"'], env={"PATH": os.environ["PATH"], "PF_WI_ID": "wi_y"})
            self.assertEqual(c.stdout, b"wi_y")

    def test_streams_to_files(self) -> None:
        log = os.path.join(self.dir, "log")
        with open(log, "wb") as f:
            c = proc.run(["sh", "-c", "echo to-log; echo err >&2"], stdout=f, merge_stderr=True)
        self.assertEqual(c.stdout, b"")
        with open(log, "rb") as f:
            self.assertEqual(sorted(f.read().split()), [b"err", b"to-log"])


if __name__ == "__main__":
    unittest.main()
