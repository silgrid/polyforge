# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/tests/test_build_structure_first.py
"""facts.py build --replay runs every structural check to completion
before any replay (polyforge#62 AC4): a unit's resolution, its commits'
test-first shape (test_commit one ordinary commit of test paths alone,
a proper ancestor of impl_commit) and the units file's shape are judged
first, and any failure among them exits before a replay is made — the
run polyforge#53 paid five replay minutes for before its structure
failure surfaced. Expected values come from the documented refusal
semantics (exit 1, the ``has 2 parents`` text) and from the ordering
the acceptance names (no replay record at all when structure fails),
never from the implementation; a sound unit still replays red at its
test commit, so the fix cannot merely switch replays off."""

import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
FACTS = os.path.join(os.path.dirname(HERE), "facts.py")


class BuildStructureFirstTest(GitCase):
    def capsule(self, base: Dict[str, str], changes: List[Tuple[str, Dict[str, str]]]) -> Tuple[str, Repo, Dict[str, str]]:
        """A capsule whose repos/app holds base (origin/main at that
        commit) and then one commit per change, each answering its head by
        its message; the capsule's units.json is written separately, once
        the test knows the heads."""
        cap = self.new_dir("capsule")
        repo = Repo(os.path.join(cap, "repos", "app"), base)
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        heads: Dict[str, str] = {}
        for msg, files in changes:
            heads[msg] = repo.commit(msg, files)
        write(cap, {
            "capsule.json": json.dumps({"work_item_id": "wi_62", "bundle_name": "develop", "repos": {"app": "app"}}),
        })
        return cap, repo, heads

    def units_file(self, cap: str, units: List[Dict[str, Any]]) -> None:
        write(cap, {"artifacts/build/units.json": json.dumps(units)})

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
                got: Dict[str, Any] = u
                return got
        self.fail("no unit %s in the details: %s" % (uid, res["details"]["units"]))

    def test_a_structural_failure_stops_the_run_before_any_replay(self) -> None:
        # u1 is sound and red at its test commit; u2's test_commit is a
        # merge commit (two parents) — the structural refusal of the
        # report. Whatever is sound is not replayed either: the failure
        # exits before any replay, so no unit carries a replay record.
        cap, repo, heads = self.capsule(
            {"app.go": "package app\n"},
            [("the unit's test commit", {"test/notes_test.sh": "test -f notes-impl.txt\n"}),
             ("the unit's implementation", {"notes-impl.txt": "x\n"})])
        repo.git("checkout", "-q", "-b", "side")
        side = repo.commit("side work", {"side.txt": "s\n"})
        repo.git("checkout", "-q", "main")
        repo.git("merge", "-q", "--no-ff", "-m", "u2's test commit (a merge)", side)
        merged = repo.head()
        impl2 = repo.commit("u2's implementation", {"impl2.txt": "x\n"})
        self.units_file(cap, [
            {"id": "u1", "test_commit": heads["the unit's test commit"], "impl_commit": heads["the unit's implementation"],
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^NOTES$"},
            {"id": "u2", "test_commit": merged, "impl_commit": impl2,
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^NOTES$"}])
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 1, res["summary"] + err)
        self.assertEqual(res["verdict"], "fail")
        self.assertIn("2 parents", res["summary"], res["summary"])
        self.assertIn("u2", res["summary"], res["summary"])
        for u in res["details"]["units"]:
            self.assertNotIn("replay", u, "the structure failure must stop the run before any replay: %r" % u)

    def test_sound_units_still_replay(self) -> None:
        # no structural problem: the replay runs as before, red at the
        # unit's test commit, and the run exits 0
        cap, repo, heads = self.capsule(
            {"app.go": "package app\n"},
            [("the unit's test commit", {"test/notes_test.sh": "test -f notes-impl.txt\n"}),
             ("the unit's implementation", {"notes-impl.txt": "x\n"})])
        self.units_file(cap, [
            {"id": "u1", "test_commit": heads["the unit's test commit"], "impl_commit": heads["the unit's implementation"],
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^NOTES$"}])
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 0, res["summary"] + err)
        r = self.unit_of(res, "u1")["replay"]
        self.assertTrue(r["red"], r)
        self.assertEqual((r["exit_code"], r["commit"]), (1, heads["the unit's test commit"]))


if __name__ == "__main__":
    unittest.main()
