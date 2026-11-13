"""gate.py's green tag: written only for a pass of this runner on a clean
tree at the commit HEAD still is, as one annotated tag on the remote whose
message is the record; the same record never tagged twice; a name another
writer took first moves to the next number, never forced over. The Go
side's twin was verifyrun/w405_gate_green_test.go (the tag half)."""

import dataclasses
import datetime
import json
import os
import subprocess
import tempfile
import unittest
from typing import Dict, Optional

import gate
from pfstep import proc

from .fakebox import FakeRuntime
from .gatefix import HAVE_BASH, HAVE_GIT, HAVE_PY, IDENT, git_t, green_clone, run_gate, write_files

NOW = datetime.datetime(2026, 11, 8, 9, 30, 0, tzinfo=datetime.timezone.utc)


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class GreenTagTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.dir = green_clone(self.root, "stack=none\nfast_gate=echo one\n")
        self.origin = os.path.join(self.root, "origin.git")
        self.g = gate.Git(dict(os.environ, **IDENT))
        r = run_gate(self.dir, FakeRuntime(os.path.join(self.root, "box-tmp")), os.path.join(self.root, "logs"))
        self.assertTrue(r.res.passed, r.progress)
        self.res = r.res
        self.head = git_t(self.dir, "rev-parse", "HEAD")

    def tearDown(self) -> None:
        self._td.cleanup()

    def record(self, name: str) -> Dict[str, str]:
        body = git_t(self.origin, "for-each-ref", "--format=%(contents)", "refs/tags/" + name)
        self.assertNotIn("\n", body, "the message is one line")
        out: Dict[str, str] = json.loads(body)
        return out

    def test_a_pass_becomes_one_annotated_tag_carrying_the_record(self) -> None:
        self.assertEqual(gate.green_preflight(self.g, self.dir, "origin", ""), "")
        # stray names of the day: a lightweight -1, an annotated non-marker -3
        git_t(self.dir, "tag", "green/20261108-1", self.head)
        git_t(self.dir, "tag", "-a", "-m", "not json", "green/20261108-3", self.head)
        git_t(self.dir, "push", "-q", "origin", "refs/tags/green/20261108-1", "refs/tags/green/20261108-3")
        name, created = gate.mark_green(self.g, self.dir, "origin", "by hand", self.res, now=NOW)
        self.assertEqual((name, created), ("green/20261108-4", True))
        self.assertEqual(git_t(self.origin, "for-each-ref", "--format=%(objecttype) %(*objectname)", "refs/tags/" + name),
                         "tag " + self.head)
        rec = self.record(name)
        gid = subprocess.run(["sh", "tools/gate-id", self.head], cwd=self.dir, check=True, stdout=subprocess.PIPE).stdout.decode().strip()
        want = {"commit": self.head, "gate_id": gid, "runner": gate.GATE_RUNNER, "runner_version": gate.GATE_RUNNER_VERSION,
                "plan_digest": self.res.plan_digest, "verified_at": "2026-11-08T09:30:00Z"}
        for k, v in want.items():
            self.assertEqual(rec[k], v, k)
        self.assertEqual(list(rec), ["commit", "gate_id", "runner", "runner_version", "plan_digest", "verified_at", "summary"])
        self.assertIn("1 commands passed", rec["summary"])
        self.assertTrue(rec["summary"].endswith("by hand"), rec["summary"])
        self.assertEqual(git_t(self.origin, "for-each-ref", "--format=%(taggername)", "refs/tags/" + name), IDENT["GIT_COMMITTER_NAME"],
                         "the tagger is the committer running the gate")
        # the same commit and plan again: the existing tag, nothing pushed
        again, created = gate.mark_green(self.g, self.dir, "origin", "again", self.res, now=NOW + datetime.timedelta(hours=1))
        self.assertEqual((again, created), (name, False))
        self.assertEqual(len(git_t(self.origin, "tag", "-l", "green/*").split()), 3)

    def test_the_lanes_tagger(self) -> None:
        name, _ = gate.mark_green(self.g, self.dir, "origin", "batch", self.res, tagger=gate.LANE_TAGGER, now=NOW)
        self.assertEqual(git_t(self.origin, "for-each-ref", "--format=%(taggername) %(taggeremail)", "refs/tags/" + name),
                         gate.LANE_TAGGER)

    def test_what_is_not_a_pass_is_never_tagged(self) -> None:
        for name, change in (("red", {"passed": False}), ("incomplete", {"incomplete": "x"}), ("another runner", {"runner": "verify.py"}),
                             ("another version", {"runner_version": "1"}), ("dirty at start", {"clean": False}),
                             ("no digest", {"plan_digest": ""}), ("head moved", {"commit": "a" * 40})):
            with self.subTest(name):
                r = dataclasses.replace(self.res, **change)
                with self.assertRaises(gate.GreenError):
                    gate.mark_green(self.g, self.dir, "origin", "s", r, now=NOW)
        self.assertEqual(git_t(self.origin, "tag", "-l"), "")

    def test_the_preflight(self) -> None:
        write_files(self.dir, {"stray.txt": "x\n"})
        self.assertIn("not clean", gate.green_preflight(self.g, self.dir, "origin", ""))
        os.remove(os.path.join(self.dir, "stray.txt"))
        self.assertIn("cannot read the green tags", gate.green_preflight(self.g, self.dir, os.path.join(self.root, "none.git"), ""))
        # a committer with no identity: git must not invent one from the
        # user and host names (macOS's git does unless told not to)
        home = os.path.join(self.root, "home")
        os.makedirs(home)
        with open(os.path.join(home, ".gitconfig"), "w", encoding="utf-8") as f:
            f.write("[user]\n\tuseConfigOnly = true\n")
        anon = gate.Git({k: v for k, v in os.environ.items() if not k.startswith("GIT_") and k != "XDG_CONFIG_HOME"}
                        | {"HOME": home, "GIT_CONFIG_NOSYSTEM": "1"})
        self.assertIn("no committer identity", gate.green_preflight(anon, self.dir, "origin", ""))
        self.assertEqual(gate.green_preflight(anon, self.dir, "origin", gate.LANE_TAGGER), "", "a named tagger needs no identity")

    def test_a_name_taken_meanwhile_moves_to_the_next_number_never_forced(self) -> None:
        other = git_t(self.dir, "rev-parse", "HEAD")
        origin = self.origin

        class Racing(gate.Git):
            """Another writer pushes green/<day>-1 between the read of the
            remote and this gate's first push."""

            planted = False

            def run(self, directory: str, *args: str, input: Optional[bytes] = None) -> proc.Completed:
                if args and args[0] == "push" and not Racing.planted:
                    Racing.planted = True
                    git_t(origin, "update-ref", "refs/tags/green/20261108-1", other)
                return super().run(directory, *args, input=input)

        name, created = gate.mark_green(Racing(self.g.environ), self.dir, "origin", "s", self.res, now=NOW)
        self.assertEqual((name, created), ("green/20261108-2", True))
        self.assertEqual(git_t(origin, "rev-parse", "refs/tags/green/20261108-1"), other, "the other writer's tag was not forced over")


if __name__ == "__main__":
    unittest.main()
