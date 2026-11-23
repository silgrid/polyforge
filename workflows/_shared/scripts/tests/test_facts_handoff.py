# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/tests/test_facts_handoff.py
"""facts.py `handoff` — the mechanical facts a build's done gate reads
(polyforge#241, D1–D5): one `pf-step-result/1` summarising the four
existing kinds (`build --replay`, `plan-conformance`, `carries`,
`analysis`) and the two deterministic `test_weakened` markers ("adds a
skip", "changes the `carries:` header") with no finding cited for them.

Expected values come from the ticket's definitions, never from the
implementation: any deterministic hit exits 1 with the hit listed in
`details.hits`; a clean tree exits 0 with an empty hit list; a sub-item
that cannot be judged (exit 2) is recorded in `details.not_judged` and is
no hit — except `carries`, whose exit 2 stays not-pass and refuses the
hand-off (owner Q3 B). The red run of this file is the exit-code
assertion: the `handoff` kind does not exist yet, so the command answers a
usage exit 2 where a hit (exit 1) is expected."""

import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
FACTS = os.path.join(SCRIPTS, "facts.py")

CARRIES_SEED = {
    "app.go": "package app\n",
    "web/.polyforge/verify-scope.conf": "stack=npm\ne2e_runner=npm run test:e2e:all --\n",
    "web/package.json": '{"scripts":{"test:e2e:all":"node e2e/all.mjs"}}\n',
    "web/lib/panel.ts": "export const panel = 1;\n",
    "web/e2e/all.mjs": 'import "./run.mjs";\n',
    "web/e2e/run.mjs": '// carries: lib/run*\n\nimport "node:assert";\n',
}

CLEAN_ANALYSIS = {"supplement_limit": 10, "changed": [], "uncovered": {}, "tests": [], "selected": {}}

UNIT_TEST = 'test -f answer.txt && { echo ok; exit 0; }\nexit 1\n'


def plan(writes: List[str]) -> str:
    entries = ", ".join("`%s` (write)" % p for p in writes)
    return "## Units\n\n**u1** — the change.\nTouched files: %s\nCoupled obligations: none — a private constant no document lists\n" % entries


class HandoffCase(GitCase):
    maxDiff = None

    # --- fixtures --------------------------------------------------------

    def capsule(self, name: str, base: Optional[Dict[str, str]] = None) -> Tuple[str, Repo]:
        cap = os.path.join(self.tmp, name)
        repo = Repo(os.path.join(cap, "repos", "app"), base or {"app.go": "package app\n"})
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        write(cap, {"capsule.json": json.dumps(
            {"work_item_id": "wi_241", "bundle_name": "develop", "repos": {"app": "app"}})})
        return cap, repo

    def sound_unit(self, cap: str, repo: Repo, writes: List[str],
                   analysis: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        """One unit whose test is red at its test commit and green after
        the implementation, with a conformant plan and analysis."""
        test = repo.commit("u1: test", {"test/check_test.sh": UNIT_TEST})
        impl = repo.commit("u1: impl", {"answer.txt": "42\n"})
        write(cap, {
            "artifacts/plan.md": plan(writes),
            "artifacts/build/units.json": json.dumps([
                {"id": "u1", "test_commit": test, "impl_commit": impl,
                 "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/check_test.sh"]},
                 "marker": "^ok$"}]),
            "artifacts/build/change-analysis.json": json.dumps(analysis or CLEAN_ANALYSIS),
        })
        return {"test": test, "impl": impl}

    def run_handoff(self, cap: str, *extra: str) -> Tuple[int, Dict[str, Any]]:
        out = os.path.join(cap, "artifacts", "build", "handoff-facts", "app.json")
        os.makedirs(os.path.dirname(out), exist_ok=True)
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        argv = [sys.executable, "-B", FACTS, "handoff", "--capsule", cap, "--json", "--out", out, *extra]
        p = subprocess.run(argv, cwd=cap, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if not os.path.exists(out):
            self.fail("facts.py handoff wrote no result (exit %d): %s" % (p.returncode, p.stderr.decode()))
        with open(out, encoding="utf-8") as f:
            res = json.load(f)
        self.assertEqual(p.returncode, res["exit_code"], "the result file carries the run's own exit code")
        return p.returncode, res

    def items(self, res: Dict[str, Any]) -> List[str]:
        return [h.get("item", "") for h in res["details"].get("hits", [])]

    # --- D1–D5 -----------------------------------------------------------

    def test_clean_tree_passes(self) -> None:
        cap, repo = self.capsule("clean")
        self.sound_unit(cap, repo, ["answer.txt"])
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["verdict"], "pass")
        self.assertEqual(res["details"]["hits"], [])
        self.assertEqual(res["details"]["not_judged"], [])
        self.assertEqual(res["details"]["checks"],
                         {"build": 0, "plan-conformance": 0, "carries": 0, "analysis": 0})

    def test_d1_a_test_green_at_its_test_commit_is_a_hit(self) -> None:
        cap, repo = self.capsule("d1")
        test = repo.commit("u1: test", {"test/check_test.sh": "echo ok\nexit 0\n"})
        impl = repo.commit("u1: impl", {"answer.txt": "42\n"})
        write(cap, {
            "artifacts/plan.md": plan(["answer.txt"]),
            "artifacts/build/units.json": json.dumps([
                {"id": "u1", "test_commit": test, "impl_commit": impl,
                 "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/check_test.sh"]},
                 "marker": "^ok$"}]),
            "artifacts/build/change-analysis.json": json.dumps(CLEAN_ANALYSIS),
        })
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("D1", self.items(res))
        self.assertIn("u1", res["summary"])
        self.assertIn("exited 0 on the tree of test_commit", res["summary"])

    def test_d2_an_undeclared_write_is_a_hit(self) -> None:
        cap, repo = self.capsule("d2")
        self.sound_unit(cap, repo, ["answer.txt"])
        # app.go changed with no unit declaring it
        repo.commit("an undeclared write", {"app.go": "package app\n\nvar X = 1\n"})
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("D2", self.items(res))
        self.assertIn("app.go", res["summary"])

    def test_d3_an_e2e_suite_without_carries_is_a_hit(self) -> None:
        cap, repo = self.capsule("d3", dict(CARRIES_SEED))
        self.sound_unit(cap, repo, ["answer.txt", "web/lib/panel.ts", "web/e2e/new.mjs"])
        repo.commit("a new suite with no carries header",
                    {"web/lib/panel.ts": "export const panel = 2;\n",
                     "web/e2e/new.mjs": 'import "node:assert";\n'})
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("D3", self.items(res))
        self.assertIn("web/e2e/new.mjs", res["summary"])

    def test_d4_an_analysis_entry_that_does_not_exist_is_a_hit(self) -> None:
        cap, repo = self.capsule("d4")
        self.sound_unit(cap, repo, ["answer.txt"], analysis={
            "supplement_limit": 10, "changed": [], "uncovered": {},
            "tests": [{"id": "a1", "repo": "app", "file": "test/missing_test.sh",
                       "argv": ["sh", "test/missing_test.sh"], "marker": "^ok$",
                       "why": "pins the change", "evidence": "searched the diff"}],
            "selected": {}})
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("D4", self.items(res))
        self.assertIn("a1", res["summary"])

    def test_d5_a_skip_added_after_the_test_commit_is_a_hit(self) -> None:
        cap, repo = self.capsule("d5")
        self.sound_unit(cap, repo, ["answer.txt"])
        repo.commit("weaken the test", {"test/check_test.sh": UNIT_TEST + 't.Skip("flaky")\n'})
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("D5", self.items(res))
        self.assertIn("adds a skip", res["summary"])

    def test_d5_a_plain_deletion_is_listed_not_taken(self) -> None:
        cap, repo = self.capsule("d5-plain")
        self.sound_unit(cap, repo, ["answer.txt"])
        repo.commit("rewrite the test", {"test/check_test.sh": 'test -f answer.txt && { echo ok; exit 0; }\nexit 2\n'})
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["details"]["hits"], [])
        reasons = " ".join(h["reason"] for h in res["details"]["test_weakened"])
        self.assertIn("deletes or changes", reasons, "the semantic hunk stays review's to read")

    def test_d5_a_cited_skip_is_exempt(self) -> None:
        cap, repo = self.capsule("d5-cited")
        self.sound_unit(cap, repo, ["answer.txt"])
        repo.commit("weaken the test", {"test/check_test.sh": UNIT_TEST + 't.Skip("flaky")\n'})
        code, res = self.run_handoff(cap, "--cite", "test/check_test.sh s1#2: the skip is the cited finding")
        self.assertEqual(code, 0, res["summary"])
        self.assertNotIn("D5", self.items(res))

    # --- exit 2 semantics (owner Q3 B) -----------------------------------

    def test_a_sub_item_that_cannot_be_judged_is_no_hit(self) -> None:
        cap, repo = self.capsule("exit2")
        self.sound_unit(cap, repo, ["answer.txt"])
        # a container replay the repo cannot provide: build exits 2, the
        # other three judge the same tree and pass
        code, res = self.run_handoff(cap, "--isolation", "container")
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["verdict"], "pass")
        self.assertEqual(res["details"]["hits"], [])
        self.assertIn("D1", [n["item"] for n in res["details"]["not_judged"]],
                      "the unjudged sub-item is named, not counted as a hit")

    def test_carries_exit_two_still_refuses(self) -> None:
        cap, repo = self.capsule("exit2-carries")
        self.sound_unit(cap, repo, ["answer.txt"])
        write(cap, {"artifacts/build/units.json": "{not a JSON array"})
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"], "carries keeps exit 2 no pass and refuses the hand-off")
        self.assertIn("D3", self.items(res))
        self.assertIn("not judged", res["summary"])


if __name__ == "__main__":
    unittest.main()
