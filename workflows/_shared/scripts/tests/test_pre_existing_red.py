# carries: workflows/_shared/scripts/facts.py
"""facts.py build --replay --isolation host on a unit declared
`pre_existing_red: true` (polyforge#59): its reproduction test already
exists and is red before the change (an integration fix ticket: the test
came with the batch, the fix is the change), so the engine replays its
recorded run on the tree of the change's base (the merge-base with the
fetched origin default branch) — red there is the red evidence the
result records, beside an ordinary unit replayed at its test commit
(AC1) — and a run that is green there fails the unit, naming the reason:
the test was green before the change, so it was not already red and the
unit has no red evidence (AC2). Expected values come from the fixture's
own git state (the base commit the merge-base names, each tree's own
exit code), never from the implementation."""

import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
FACTS = os.path.join(os.path.dirname(HERE), "facts.py")


class PreExistingRedReplayTest(GitCase):
    def capsule(self, base: Dict[str, str], changes: List[Tuple[str, Dict[str, str]]],
                units: List[Dict[str, Any]]) -> Tuple[str, str, Dict[str, str]]:
        """A capsule whose repos/app holds base (origin/main at that
        commit) and then one commit per change, each answering its head
        by its message; the capsule's units.json is units."""
        cap = self.new_dir("capsule")
        repo = Repo(os.path.join(cap, "repos", "app"), base)
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        base_head = repo.head()
        heads: Dict[str, str] = {}
        for msg, files in changes:
            heads[msg] = repo.commit(msg, files)
        write(cap, {
            "capsule.json": json.dumps({"work_item_id": "wi_59", "bundle_name": "develop", "repos": {"app": "app"}}),
            "artifacts/build/units.json": json.dumps(units),
        })
        return cap, base_head, heads

    def run_facts(self, cap: str) -> Tuple[int, Dict[str, Any], str]:
        out = os.path.join(cap, "artifacts", "facts", "build.json")
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        p = subprocess.run([sys.executable, "-B", FACTS, "build", "--capsule", cap, "--replay", "--isolation", "host",
                            "--unit-timeout", "3m", "--deadline", "5m", "--json", "--out", out],
                           env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        with open(out, encoding="utf-8") as f:
            res = json.load(f)
        return p.returncode, res, p.stderr.decode("utf-8", "replace")

    def unit_of(self, res: Dict[str, Any], uid: str) -> Dict[str, Any]:
        for u in res["details"]["units"]:
            if u["id"] == uid:
                return u
        self.fail("no unit %s in the details: %s" % (uid, res["details"]["units"]))

    def test_a_pre_existing_red_unit_replays_red_at_the_change_base(self) -> None:
        # the reproduction test exists at the base and fails there; the
        # change is the fix — and an ordinary unit replays beside it, one
        # run per place in the same report
        cap, base, heads = self.capsule(
            {"app.go": "package app\n", "test/run.sh": "test -f impl.txt\n"},
            [("the unit's test commit", {"test/notes_test.sh": "test -f notes-impl.txt\n"}),
             ("the unit's implementation", {"notes-impl.txt": "x\n"}),
             ("the fix", {"impl.txt": "done\n"})],
            [{"id": "u1", "test_commit": heads["the unit's test commit"], "impl_commit": heads["the unit's implementation"],
              "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^NOTES$"},
             {"id": "u2", "pre_existing_red": True, "impl_commit": heads["the fix"],
              "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/run.sh"]}, "marker": "^IMPL$"}])
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 0, res["summary"] + err)
        u2 = self.unit_of(res, "u2")
        self.assertEqual(u2.get("pre_existing_red"), True)
        self.assertNotIn("test_commit", u2)
        r = u2["replay"]
        self.assertTrue(r["red"], r)
        self.assertEqual((r["exit_code"], r["commit"]), (1, base))
        r1 = self.unit_of(res, "u1")["replay"]
        self.assertTrue(r1["red"], r1)
        self.assertEqual(r1["commit"], heads["the unit's test commit"])

    def test_a_run_green_at_the_base_fails_the_unit(self) -> None:
        # the test passes at the base: it was not already red, so the
        # unit has no red evidence and the engine says why
        cap, base, heads = self.capsule(
            {"impl.txt": "x\n", "test/run.sh": "test -f impl.txt\n"},
            [("the fix", {"impl.txt": "y\n"})],
            [{"id": "u1", "pre_existing_red": True, "impl_commit": heads["the fix"],
              "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/run.sh"]}, "marker": "^IMPL$"}])
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 1, res["summary"] + err)
        self.assertEqual(res["verdict"], "fail")
        r = self.unit_of(res, "u1")["replay"]
        self.assertFalse(r["red"], r)
        self.assertEqual((r["exit_code"], r["commit"]), (0, base))
        self.assertIn("exited 0 on the tree of the change's base", res["summary"])
        self.assertIn("the test was green before the change", res["summary"])
        self.assertIn("not already red", res["summary"])

    def test_no_base_to_replay_on_names_the_unit_problem(self) -> None:
        cap, base, heads = self.capsule(
            {"test/run.sh": "test -f impl.txt\n"},
            [("the fix", {"impl.txt": "done\n"})],
            [{"id": "u1", "pre_existing_red": True, "impl_commit": heads["the fix"],
              "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/run.sh"]}, "marker": "^IMPL$"}])
        repo = os.path.join(cap, "repos", "app")
        for ref in ("refs/remotes/origin/main", "refs/remotes/origin/master"):
            subprocess.run(["git", "-C", repo, "update-ref", "-d", ref],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 1, res["summary"] + err)
        self.assertIn("the change's base", res["summary"])
        self.assertIn("origin default branch", res["summary"])
        self.assertNotIn("replay", self.unit_of(res, "u1"))


if __name__ == "__main__":
    unittest.main()
