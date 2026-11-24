"""The landing lock's before-merge gate (polyforge#245, 245-lane): every
real ``gh pr merge`` of merge.py / revert.py is preceded by the daemon's
``pf step lock-check --repo <claim repo name>``, and any refusal — a lost
lease, a token mismatch, the command missing — stops the merge. A check
that passed never carries a token; the merge scripts only learn ok/failed.
"""

import os
import stat
import tempfile
import unittest
from typing import Dict, List, Optional, Tuple
from unittest import mock

from pfstep.gitmerge.ctx import Ctx, GoError
from pfstep.gitmerge.git import Git


class RecordingGit(Git):
    """A Git that records the commands it would run."""

    def __init__(self, environ: Optional[Dict[str, str]] = None) -> None:
        super().__init__(Ctx(), environ if environ is not None else {})
        self.calls: List[Tuple[str, ...]] = []
        self.refuse_lock = False

    def cmd(self, cwd: str, name: str, *args: str, stdin: Optional[bytes] = None) -> bytes:
        self.calls.append((name,) + args)
        if name == "pf" and self.refuse_lock:
            from pfstep.gitmerge.git import CmdError
            raise CmdError("pf step lock-check --repo app: exit status 1: the landing lock was lost")
        return b""


class LandingGateTest(unittest.TestCase):
    def test_armed_merge_checks_then_merges(self) -> None:
        g = RecordingGit()
        g.landing_gate = True
        g.merge_pr("/capsule/repos/app", "https://forge.invalid/pr/1", "abc")
        self.assertEqual(g.calls[0], ("pf", "step", "lock-check", "--repo", "app"))
        self.assertEqual(g.calls[1][:3], ("gh", "pr", "merge"))
        self.assertNotIn("token", " ".join(g.calls[0]))

    def test_refused_check_never_merges(self) -> None:
        g = RecordingGit()
        g.landing_gate = True
        g.refuse_lock = True
        with self.assertRaises(GoError) as cm:
            g.merge_pr("/capsule/repos/app", "https://forge.invalid/pr/1", "abc")
        self.assertIn("landing lock", str(cm.exception))
        self.assertFalse(any(c[0] == "gh" for c in g.calls), g.calls)

    def test_missing_command_never_merges(self) -> None:
        # a real Git whose PATH holds no pf: the check cannot run, so it
        # refuses and gh is never reached
        g = Git(Ctx(), {"PATH": os.path.join(tempfile.gettempdir(), "no-such-bin")})
        g.landing_gate = True
        with mock.patch("pfstep.gitmerge.git.shutil.which", return_value=None):
            with self.assertRaises(GoError) as cm:
                g.merge_pr("/capsule/repos/app", "https://forge.invalid/pr/1", "abc")
        self.assertIn("landing lock", str(cm.exception))

    def test_unarmed_git_is_unchanged(self) -> None:
        g = RecordingGit()
        g.merge_pr("/capsule/repos/app", "https://forge.invalid/pr/1", "abc")
        self.assertEqual(len(g.calls), 1)
        self.assertEqual(g.calls[0][:3], ("gh", "pr", "merge"))

    def test_the_real_command_receives_the_claim_name(self) -> None:
        d = tempfile.mkdtemp()
        self.addCleanup(lambda: __import__("shutil").rmtree(d, True))
        record = os.path.join(d, "record")
        p = os.path.join(d, "pf")
        with open(p, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %s\n" % record)
        os.chmod(p, os.stat(p).st_mode | stat.S_IXUSR)
        cwd = os.path.join(d, "repos", "app")
        os.makedirs(cwd)
        g = Git(Ctx(), {"PATH": d})
        g.check_landing(cwd)
        with open(record, encoding="utf-8") as f:
            self.assertEqual(f.read().strip(), "step lock-check --repo app")
