"""pfstep.runner on the host (isolation "host"): a normal exit, a non-zero
exit, the deadline killing the whole process group (a grandchild
included), a command that never started (the deadline passed; no clone;
no working directory), the log's name and header, the private TMPDIR, the
working directory and the environment — the Go executor's behaviour
(verifyrun.RunCommand with a host run)."""

import datetime
import os
import shlex
import signal
import sys
import tempfile
import time
import unittest

from pfstep import proc, runner, tmpdir
from pfstep.runner import CommandOptions, ExecSpec, ExecutorError, Runner

from .capsuleutil import HAVE_GIT, make_capsule, head, write_files
from .procutil import alive


def _wait_gone(pid: int, bound: float = 5.0) -> bool:
    end = time.monotonic() + bound
    while time.monotonic() < end:
        if not alive(pid):
            return True
        time.sleep(0.05)
    return not alive(pid)


def _read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


@unittest.skipUnless(HAVE_GIT, "git is not on PATH")
class HostRunTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.capsule = make_capsule(self.root, "app", {"a.txt": "a\n", "sub/b.txt": "b\n"})
        self.clone = os.path.join(self.capsule, "repos", "app")
        self.log_dir = os.path.join(self.capsule, "runtime-state", "run", "2", "app")

    def tearDown(self) -> None:
        self._td.cleanup()

    def opts(self, command: str, seconds: float = 60.0, **kw: object) -> CommandOptions:
        o = CommandOptions(
            capsule_dir=self.capsule, repo="app", command=command,
            deadline=time.time() + seconds, timeout=seconds, log_dir=self.log_dir, run=2,
            isolation=runner.ISOLATION_HOST, env=dict(os.environ),
        )
        for k, v in kw.items():
            setattr(o, k, v)
        return o

    def test_a_normal_exit(self) -> None:
        res = runner.run_command(self.opts("echo hello"))
        self.assertEqual((res.exit, res.timed_out, res.not_started, res.host), (0, False, False, True))
        self.assertEqual(res.log, "runtime-state/run/2/app/command.log")
        self.assertEqual((res.head, res.dirty, res.image, res.container), (head(self.clone), False, "", ""))
        self.assertGreaterEqual(res.duration_ms, 1)
        self.assertIsNotNone(res.started_at)
        self.assertEqual(
            _read(os.path.join(self.capsule, res.log)),
            "$ bash -c echo hello\n# cwd: repos/app\nhello\n",
        )
        self.assertEqual(sorted(os.listdir(self.log_dir)), ["command.log"], "a host run writes command.log alone")

    def test_a_non_zero_exit(self) -> None:
        res = runner.run_command(self.opts("echo out; echo err >&2; exit 3"))
        self.assertEqual((res.exit, res.timed_out, res.not_started), (3, False, False))
        log = _read(os.path.join(self.capsule, res.log))
        self.assertIn("out\n", log)
        self.assertIn("err\n", log)

    def test_the_deadline_kills_the_whole_group(self) -> None:
        pidfile = os.path.join(self.root, "grandchild.pid")
        start = time.monotonic()
        res = runner.run_command(self.opts('sleep 300 & echo $! > "%s"; wait' % pidfile, seconds=2.0))
        self.assertLess(time.monotonic() - start, 20)
        self.assertEqual((res.exit, res.timed_out, res.not_started), (runner.TIMEOUT_EXIT, True, False))
        with open(pidfile, encoding="utf-8") as f:
            grandchild = int(f.read())
        self.assertTrue(_wait_gone(grandchild), "the grandchild outlived the deadline")
        self.assertTrue(
            _read(os.path.join(self.capsule, res.log)).endswith("\n# pf verify-run: killed at the run step's timeout (2s)\n")
        )

    def test_never_started_when_the_deadline_has_passed(self) -> None:
        res = runner.run_command(self.opts("echo never", seconds=-1.0))
        self.assertEqual((res.exit, res.timed_out, res.not_started, res.log, res.host), (runner.TIMEOUT_EXIT, True, True, "", False))
        self.assertEqual(res.head, head(self.clone), "the HEAD is read before the deadline is judged")
        self.assertFalse(os.path.exists(os.path.join(self.log_dir, runner.COMMAND_LOG)), "nothing ran, no log")

    def test_never_started_without_a_clone(self) -> None:
        with self.assertRaises(ExecutorError) as cm:
            runner.run_command(self.opts("echo never", repo="nope"))
        self.assertIn("repos/nope is not a task repo clone of the capsule", str(cm.exception))
        self.assertNotIsInstance(cm.exception, runner.EnvironmentNotSatisfied)

    def test_never_started_without_its_working_directory(self) -> None:
        missing = os.path.join(self.clone, "no-such-dir")
        with self.assertRaises(ExecutorError) as cm:
            runner.run_command(self.opts("echo never", workdir=missing))
        self.assertIn("the command could not start on the host", str(cm.exception))
        self.assertIn("could not start", _read(os.path.join(self.log_dir, runner.COMMAND_LOG)))

    def test_a_working_directory_inside_the_clone(self) -> None:
        sub = os.path.join(self.clone, "sub")
        res = runner.run_command(self.opts("pwd", workdir=sub))
        self.assertEqual(_read(os.path.join(self.capsule, res.log)), "$ bash -c pwd\n# cwd: repos/app/sub\n%s\n" % sub)
        for outside in (self.root, "relative/dir"):
            with self.assertRaises(ExecutorError) as cm:
                runner.run_command(self.opts("pwd", workdir=outside))
            self.assertIn("lies outside the clone", str(cm.exception))

    def test_a_private_tmpdir_removed_after(self) -> None:
        res = runner.run_command(self.opts('test -d "$TMPDIR" && echo "TMP=$TMPDIR"'))
        self.assertEqual(res.exit, 0)
        line = [ln for ln in _read(os.path.join(self.capsule, res.log)).splitlines() if ln.startswith("TMP=")][0]
        tmp = line[len("TMP="):]
        self.assertTrue(os.path.basename(tmp).startswith(tmpdir.DIR_PREFIX), tmp)
        self.assertFalse(os.path.exists(tmp), "the private TMPDIR outlived its command")

    def test_the_private_tmpdir_is_under_tmp_whatever_the_runners_tmpdir(self) -> None:
        # (r1#2): the host's private TMPDIR lives under /tmp,
        # never under the runner process's own (here long) temp dir.
        long_tmp = os.path.join(self.root, "x" * 60)
        os.makedirs(long_tmp)
        saved = tempfile.tempdir
        tempfile.tempdir = long_tmp
        try:
            res = runner.run_command(self.opts('echo "TMP=$TMPDIR"'))
        finally:
            tempfile.tempdir = saved
        self.assertEqual(res.exit, 0)
        line = [ln for ln in _read(os.path.join(self.capsule, res.log)).splitlines() if ln.startswith("TMP=")][0]
        tmp = line[len("TMP="):]
        self.assertEqual(os.path.dirname(tmp), "/tmp", tmp)
        self.assertTrue(os.path.basename(tmp).startswith(tmpdir.DIR_PREFIX), tmp)

    def test_a_socket_in_a_go_temp_dir_under_the_private_tmpdir_binds(self) -> None:
        # Go's t.TempDir() is $TMPDIR/<test name, up to 64 bytes><MkdirTemp
        # suffix, at most 10 digits>/001; a test listening on a unix socket
        # there needs the whole path within sun_path (107 bytes plus NUL).
        code = (
            "import os, socket\n"
            "d = os.path.join(os.environ['TMPDIR'], "
            "'TestAccounts_AddRefusesAMalformedRequestOrAnUnusablePassword4294967295', '001')\n"
            "os.makedirs(d)\n"
            "p = os.path.join(d, 'bff.sock')\n"
            "print('SOCK=' + p, flush=True)\n"
            "s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)\n"
            "s.bind(p)\n"
            "s.close()\n"
        )
        res = runner.run_command(self.opts("%s -c %s" % (shlex.quote(sys.executable), shlex.quote(code))))
        log = _read(os.path.join(self.capsule, res.log))
        self.assertEqual(res.exit, 0, log)
        sock = [ln for ln in log.splitlines() if ln.startswith("SOCK=")][0][len("SOCK="):]
        self.assertLessEqual(len(sock.encode("utf-8")), 107, sock)
        tmp = os.path.dirname(os.path.dirname(os.path.dirname(sock)))
        self.assertTrue(os.path.basename(tmp).startswith(tmpdir.DIR_PREFIX), tmp)

    def test_the_environment_is_the_given_one(self) -> None:
        env = {"PATH": os.environ["PATH"], "W506_GIVEN": "yes"}
        res = runner.run_command(self.opts('echo "given=$W506_GIVEN home=${HOME:-unset}"', env=env))
        self.assertIn("given=yes home=unset", _read(os.path.join(self.capsule, res.log)))

    def test_dirty_and_a_log_outside_the_capsule(self) -> None:
        write_files(self.clone, {"untracked.txt": "x\n"})
        outside = os.path.join(self.root, "logs-outside")
        res = runner.run_command(self.opts("true", log_dir=outside))
        self.assertTrue(res.dirty)
        self.assertEqual(res.log, os.path.join(outside, runner.COMMAND_LOG), "a log outside the capsule is absolute")

    def test_an_unknown_isolation_is_refused(self) -> None:
        with self.assertRaises(ExecutorError):
            runner.run_command(self.opts("true", isolation="vm"))


class TerminationTest(unittest.TestCase):
    def test_sigterm_raises_interrupted(self) -> None:
        scripts = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        code = (
            "import os, signal, sys, time\n"
            "sys.path.insert(0, sys.argv[1])\n"
            "from pfstep import runner\n"
            "runner.raise_on_termination()\n"
            "try:\n"
            "    os.kill(os.getpid(), signal.SIGTERM)\n"
            "    time.sleep(30)\n"
            "except runner.Interrupted as err:\n"
            "    print('interrupted', err.signum)\n"
        )
        c = proc.run([sys.executable, "-B", "-c", code, scripts], timeout=60)
        self.assertEqual((c.exit_code, c.stdout.decode().strip()), (0, "interrupted %d" % signal.SIGTERM))


class RunnerTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def test_numbered_logs_per_directory(self) -> None:
        r = Runner(self.root, time.time() + 60, env={"PATH": os.environ["PATH"]})
        a, b = os.path.join(self.root, "a"), os.path.join(self.root, "b")
        spec = ExecSpec(argv=["true"], dir=self.root, cwd=".")
        got = [os.path.relpath(r.exec(d, spec).log_path, self.root) for d in (a, a, b, a)]
        self.assertEqual(got, ["a/1.log", "a/2.log", "b/1.log", "a/3.log"])

    def test_capture_answers_stdout_and_logs_both(self) -> None:
        r = Runner(self.root, time.time() + 60, env={"PATH": os.environ["PATH"]})
        er = r.exec(self.root, ExecSpec(argv=["sh", "-c", "echo out; echo err >&2"], dir=self.root, cwd=".", capture=True))
        self.assertEqual(er.stdout, b"out\n")
        self.assertEqual(_read(er.log_path), "$ sh -c echo out; echo err >&2\n# cwd: .\nerr\nout\n")
        assert er.evidence is not None
        self.assertEqual(er.evidence.log_path, "1.log")
        self.assertEqual(er.evidence.as_dict()["argv"], ["sh", "-c", "echo out; echo err >&2"])

    def test_own_timeout_vs_the_deadline(self) -> None:
        r = Runner(self.root, time.time() + 60, env={"PATH": os.environ["PATH"]}, deadline_what="the gate's deadline")
        er = r.exec(self.root, ExecSpec(argv=["sleep", "30"], dir=self.root, cwd=".", timeout=1.0))
        self.assertEqual((er.exit, er.timed_out, er.deadline_hit), (runner.TIMEOUT_EXIT, True, False))
        self.assertTrue(_read(er.log_path).endswith("# pf verify-run: killed at its 1s timeout\n"))
        r2 = Runner(self.root, time.time() + 1.5, env={"PATH": os.environ["PATH"]}, deadline_what="the gate's deadline")
        er2 = r2.exec(self.root, ExecSpec(argv=["sleep", "30"], dir=self.root, cwd=".", timeout=30.0))
        self.assertEqual((er2.exit, er2.timed_out, er2.deadline_hit), (runner.TIMEOUT_EXIT, True, True))
        self.assertTrue(_read(er2.log_path).endswith("# pf verify-run: killed at the gate's deadline\n"))

    def test_go_time_and_set_env(self) -> None:
        utc = datetime.timezone.utc
        self.assertEqual(runner.go_time(None), "0001-01-01T00:00:00Z")
        self.assertEqual(runner.go_time(datetime.datetime(2026, 11, 12, 1, 2, 3, tzinfo=utc)), "2026-11-12T01:02:03Z")
        self.assertEqual(runner.go_time(datetime.datetime(2026, 11, 12, 1, 2, 3, 450000, tzinfo=utc)), "2026-11-12T01:02:03.45Z")
        self.assertEqual(runner.set_env(["A=1", "TMPDIR=/x", "B", "TMPDIR=/y"], "TMPDIR", "/z"), ["A=1", "B", "TMPDIR=/z"])


if __name__ == "__main__":
    unittest.main()
