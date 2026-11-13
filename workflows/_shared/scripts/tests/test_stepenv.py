"""pfstep.stepenv: the step environment's names and their defaults."""

import os
import sys
import unittest

from pfstep import stepenv

SHARED = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


class StepEnvTest(unittest.TestCase):
    def test_reads_every_name(self) -> None:
        env = {
            "PF_WI_ID": "wi_1", "PF_DAEMON_ID": "m-a", "PF_DAEMON_SOCKET": "/s.sock",
            "PF_ROUND": "2", "PF_STEP": "build", "PF_SPAWN": "3", "PF_TOOL_WINDOW_MS": "600000",
            "PF_CAPSULE": "/c", "PF_LANE_DIR": "/c/workflows/develop", "PF_SHARED_DIR": "/c/workflows/_shared",
            "PF_PYTHON": "/usr/bin/python3.12", "PATH": "/bin",
        }
        e = stepenv.read(env)
        self.assertTrue(e.in_step)
        self.assertEqual(
            (e.wi_id, e.daemon_id, e.daemon_socket, e.round, e.step, e.spawn, e.tool_window_ms),
            ("wi_1", "m-a", "/s.sock", "2", "build", "3", "600000"),
        )
        self.assertEqual(
            (e.capsule, e.lane_dir, e.shared_dir, e.python),
            ("/c", "/c/workflows/develop", "/c/workflows/_shared", "/usr/bin/python3.12"),
        )

    def test_defaults_outside_a_step(self) -> None:
        e = stepenv.read({"PATH": "/usr/bin:/bin"})
        self.assertFalse(e.in_step)
        self.assertEqual(e.shared_dir, SHARED)
        self.assertTrue(os.path.isdir(os.path.join(e.shared_dir, "scripts", "pfstep")))
        self.assertEqual(e.python, sys.executable)
        self.assertEqual((e.round, e.capsule, e.lane_dir), ("", "", ""))
        self.assertFalse(stepenv.read({"PF_WI_ID": "wi_1"}).in_step, "one marker is not a step")


if __name__ == "__main__":
    unittest.main()
