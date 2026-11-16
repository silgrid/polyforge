# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/tests/test_build_run_declaration.py
"""facts.py build --replay judges a units.json run declaration before
any replay too (polyforge#62 r2, review r1#1): a unit that records no
run, a run whose argv is empty, or a run naming a repo other than the
one holding its commits is a declaration defect no replay is needed to
decide, so it fails the test-first check up front, with the sound units
beside it not replayed — the same ordering AC4 demands for the commit
shape. Expected values come from the documented refusal semantics (exit
1, the ``reports no run`` and ``names repos/`` texts) and from the
ordering the acceptance names (no replay record at all), never from the
implementation; the unit's commits stay sound in each case, so the
defect is the declaration alone."""

import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
FACTS = os.path.join(os.path.dirname(HERE), "facts.py")


class BuildRunDeclarationTest(GitCase):
    def capsule(self, base: Dict[str, str], changes: List[Tuple[str, Dict[str, str]]]) -> Tuple[str, Dict[str, str]]:
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
        return cap, heads

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

    def sound_world(self) -> Tuple[str, Dict[str, str]]:
        """u1 sound and red at its test commit, and u2's commits sound too
        (the defect is its run declaration alone)."""
        return self.capsule(
            {"app.go": "package app\n"},
            [("u1's test commit", {"test/notes_test.sh": "test -f notes-impl.txt\n"}),
             ("u1's implementation", {"notes-impl.txt": "x\n"}),
             ("u2's test commit", {"test/other_test.sh": "test -f other-impl.txt\n"}),
             ("u2's implementation", {"other-impl.txt": "x\n"})])

    def u1(self, heads: Dict[str, str]) -> Dict[str, Any]:
        return {"id": "u1", "test_commit": heads["u1's test commit"], "impl_commit": heads["u1's implementation"],
                "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/notes_test.sh"]}, "marker": "^NOTES$"}

    def u2(self, heads: Dict[str, str], run: Dict[str, Any]) -> Dict[str, Any]:
        return {"id": "u2", "test_commit": heads["u2's test commit"], "impl_commit": heads["u2's implementation"],
                "run": run, "marker": "^OTHER$"}

    def assert_stopped_before_any_replay(self, code: int, res: Dict[str, Any], err: str, uid: str, words: str) -> None:
        self.assertEqual(code, 1, res["summary"] + err)
        self.assertEqual(res["verdict"], "fail")
        self.assertIn(uid, res["summary"], res["summary"])
        self.assertIn(words, res["summary"], res["summary"])
        for u in res["details"]["units"]:
            self.assertNotIn("replay", u, "the declaration failure must stop the run before any replay: %r" % u)

    def test_a_missing_run_stops_the_run_before_any_replay(self) -> None:
        cap, heads = self.sound_world()
        u2 = self.u2(heads, {"repo": "app", "cwd": ".", "argv": ["sh", "test/other_test.sh"]})
        del u2["run"]
        self.units_file(cap, [self.u1(heads), u2])
        code, res, err = self.run_facts(cap)
        self.assert_stopped_before_any_replay(code, res, err, "u2", "reports no run")

    def test_an_empty_argv_stops_the_run_before_any_replay(self) -> None:
        cap, heads = self.sound_world()
        self.units_file(cap, [self.u1(heads), self.u2(heads, {"repo": "app", "cwd": ".", "argv": []})])
        code, res, err = self.run_facts(cap)
        self.assert_stopped_before_any_replay(code, res, err, "u2", "reports no run")

    def test_a_run_naming_another_repo_stops_the_run_before_any_replay(self) -> None:
        cap, heads = self.sound_world()
        self.units_file(cap, [self.u1(heads), self.u2(heads, {"repo": "other", "cwd": ".", "argv": ["sh", "test/other_test.sh"]})])
        code, res, err = self.run_facts(cap)
        self.assert_stopped_before_any_replay(code, res, err, "u2", "names repos/other")


if __name__ == "__main__":
    unittest.main()
