"""pfstep.progress: the step's progress file — the same progress lines a
script says on stderr are appended to
``<PF_CAPSULE>/runtime-state/progress/<PF_STEP>.log``, one line per say,
where Pi Web's progress card reads them while a command has not ended
(polyforge#2: step 长命令运行中在 Pi Web 看得到进度)."""

import io
import os
import tempfile
import unittest
from typing import Dict

from pfstep import progress
from pfstep.invoke import Invocation


def step_env(capsule: str, step: str = "build", **extra: str) -> Dict[str, str]:
    env = {
        "PF_WI_ID": "wi_1", "PF_DAEMON_ID": "m-a", "PF_DAEMON_SOCKET": "/s.sock",
        "PF_ROUND": "1", "PF_STEP": step, "PF_SPAWN": "1",
        "PF_TOOL_WINDOW_MS": "1800000", "PF_CAPSULE": capsule,
        "PF_LANE_DIR": os.path.join(capsule, "workflows", "develop"),
        "PF_SHARED_DIR": os.path.join(capsule, "workflows", "_shared"),
        "PF_PYTHON": "/usr/bin/python3", "PATH": "/usr/bin:/bin",
    }
    env.update(extra)
    return env


class ProgressFileTest(unittest.TestCase):
    def setUp(self) -> None:
        d = tempfile.TemporaryDirectory()
        self.addCleanup(d.cleanup)
        self.capsule = d.name
        self.env = step_env(self.capsule)
        self.log = os.path.join(self.capsule, "runtime-state", "progress", "build.log")

    def read(self) -> str:
        try:
            with open(self.log, encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def test_append_writes_one_line_per_say_under_the_capsule(self) -> None:
        self.assertEqual(progress.path(self.env), self.log)
        progress.append("verify.py: [1/2] fast-gates (fast): npm test", self.env)
        progress.append("verify.py: [1/2] fast-gates PASS (12s)", self.env)
        self.assertEqual(
            self.read(),
            "verify.py: [1/2] fast-gates (fast): npm test\nverify.py: [1/2] fast-gates PASS (12s)\n",
            "the lines appear as the command runs, one per line, no rewrites",
        )

    def test_outside_a_step_no_capsule_or_a_bad_step_id_writes_nothing(self) -> None:
        for env in [
            # no step markers: a hand run
            step_env(self.capsule, PF_DAEMON_ID="", PF_WI_ID=""),
            # a step without a capsule
            step_env("", PF_CAPSULE=""),
            # a step id that is not a plain name must not build a path
            step_env(self.capsule, step="../escape"),
        ]:
            self.assertEqual(progress.path(env), "", env)
            progress.append("x", env)
        self.assertFalse(os.path.exists(self.log))

    def test_a_write_that_cannot_happen_is_dropped_not_raised(self) -> None:
        # a capsule that is a regular file: no directory can be made under it
        not_a_dir = os.path.join(self.capsule, "not-a-dir")
        with open(not_a_dir, "w", encoding="utf-8") as f:
            f.write("x")
        progress.append("gone", step_env(not_a_dir))
        # a progress directory without write permission
        locked = os.path.join(self.capsule, "locked")
        os.makedirs(os.path.join(locked, "runtime-state", "progress"), exist_ok=True)
        os.chmod(os.path.join(locked, "runtime-state", "progress"), 0o500)
        self.addCleanup(os.chmod, os.path.join(locked, "runtime-state", "progress"), 0o700)
        progress.append("gone too", step_env(locked))
        self.assertFalse(os.path.exists(os.path.join(self.capsule, "runtime-state")))

    def test_emit_prints_and_appends_the_same_line(self) -> None:
        err = io.StringIO()
        progress.emit("run.py: repos/app: `npm test` (1/1)", err, self.env)
        self.assertEqual(err.getvalue(), "run.py: repos/app: `npm test` (1/1)\n")
        self.assertEqual(self.read(), "run.py: repos/app: `npm test` (1/1)\n")

    def test_the_invocation_say_is_the_progress_record(self) -> None:
        err = io.StringIO()
        inv = Invocation("sync.py", ["--capsule", self.capsule], self.env, stderr=err)
        inv.say("repos/app: fast-forwarded to main (abc123)")
        self.assertEqual(err.getvalue(), "sync.py: repos/app: fast-forwarded to main (abc123)\n")
        self.assertEqual(self.read(), "sync.py: repos/app: fast-forwarded to main (abc123)\n")


if __name__ == "__main__":
    unittest.main()
