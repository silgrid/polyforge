"""verify.py's per-command progress lines (polyforge#2): every command of
the plan opens with gate.py's start line ``[i/n] name (phase): cmd`` and
closes with a PASS / FAIL / SKIP end line — on stderr and, inside a step,
in the step's progress file (<capsule>/runtime-state/progress/<step>.log),
the same lines, where Pi Web's progress card reads them while the command
runs."""

import os
import re
import tempfile
import unittest
from typing import Dict

from .verifyfix import Fixture, cmd_of, fake_engine, report, run_verify


def touched(root: str) -> Fixture:
    """A fixture whose one repo carries a commit of its own (the plan
    runs only for a repo with commits beyond its default branch)."""
    fx = Fixture(root, {"app": {"a.txt": "base\n"}})
    fx.commit_in_repo("app", {"c.txt": "mine\n"}, "feature")
    return fx


def step_env(capsule: str) -> Dict[str, str]:
    return {
        "PF_WI_ID": "wi_1", "PF_DAEMON_ID": "m-a", "PF_DAEMON_SOCKET": "/s.sock",
        "PF_ROUND": "1", "PF_STEP": "verification", "PF_SPAWN": "1", "PF_CAPSULE": capsule,
    }


class BarProgressTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def progress_log(self, capsule: str) -> str:
        """The step's progress file as verify.py wrote it ("" when there is
        none)."""
        p = os.path.join(capsule, "runtime-state", "progress", "verification.log")
        try:
            with open(p, encoding="utf-8") as f:
                return f.read()
        except OSError:
            return ""

    def test_every_command_opens_with_a_start_line_and_closes_with_its_end(self) -> None:
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report(
            "full",
            cmd_of("lint", "fast", "echo lint ok"),
            cmd_of("unit", "full", "echo unit broke >&2; exit 3"),
            cmd_of("browser", "full", "touch ran.txt", requires_env=["VR_NO_SUCH_VAR"]),
        )})
        r = run_verify(fx.capsule, eng, env=step_env(fx.capsule))
        self.assertEqual(r.exit, 1, r.result.get("summary"))
        # the ordered start and end lines, gate.py's format: [i/n] opens a
        # command, PASS / FAIL / SKIP closes it (the FAIL names its log)
        whole = (r"\Averify\.py: \[1/3\] lint \(fast\): echo lint ok\n"
                 r"verify\.py: \[1/3\] lint PASS \([^)]+\)\n"
                 r"verify\.py: \[2/3\] unit \(full\): echo unit broke >&2; exit 3\n"
                 r"verify\.py: \[2/3\] unit FAIL - exit 3 \([^)]+\), log \S+\n"
                 r"verify\.py: \[3/3\] browser \(full\): touch ran\.txt\n"
                 r"verify\.py: \[3/3\] browser SKIP - .+\n"
                 r"\Z")
        for name, stream in [("stderr", r.stderr), ("the step's progress file", self.progress_log(fx.capsule))]:
            self.assertRegex(stream, whole, "%s carries exactly the start and end lines: %r" % (name, stream))

    def test_inside_a_step_the_progress_file_is_the_stderr_record(self) -> None:
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("full", cmd_of("lint", "fast", "echo ok"))})
        r = run_verify(fx.capsule, eng, env=step_env(fx.capsule))
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        log = self.progress_log(fx.capsule)
        self.assertNotEqual(log, "", "the step's progress file holds the lines")
        self.assertEqual(r.stderr, log, "verify.py says nothing on stderr it does not also append")

    def test_outside_a_step_the_lines_go_to_stderr_only(self) -> None:
        fx = touched(self.root)
        eng = fake_engine(self.root, {"app": report("full", cmd_of("lint", "fast", "echo ok"))})
        r = run_verify(fx.capsule, eng)
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertIn("verify.py: [1/1] lint (fast): echo ok", r.stderr)
        self.assertFalse(os.path.exists(os.path.join(fx.capsule, "runtime-state", "progress")),
                         "no step environment: no progress file")


if __name__ == "__main__":
    unittest.main()
