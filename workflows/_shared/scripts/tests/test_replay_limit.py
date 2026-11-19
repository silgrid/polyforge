# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/pfstep/unitruns.py
"""facts.py build --replay: the red-state time limit (polyforge#157 AC2).

A replayed red-state run must exit non-zero within its limit — the unit's
self-reported ``timeout`` (a Go duration, units.json), else the
``--unit-timeout`` default — a run killed at its limit is an evidence
problem (exit 1, the problem naming the limit and the log), never the old
"not judged" (exit 2); only a kill by the whole run's deadline stays not
judged. Expected values come from the fixtures' own exit behaviour (a
sleep never exits; a failing case exits non-zero at once) and the literal
limits, never the implementation."""

import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
FACTS = os.path.join(os.path.dirname(HERE), "facts.py")


class ReplayLimitTest(GitCase):
    def capsule(self, changes: List[Tuple[str, Dict[str, str]]]) -> Tuple[str, Dict[str, str]]:
        """A capsule whose repos/app holds the base (origin/main at it) and
        one commit per change, each answering its head by its message."""
        cap = self.new_dir("capsule")
        repo = Repo(os.path.join(cap, "repos", "app"), {"app.go": "package app\n", "test/run.sh": "exit 1\n"})
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        heads: Dict[str, str] = {}
        for msg, files in changes:
            heads[msg] = repo.commit(msg, files)
        write(cap, {
            "capsule.json": json.dumps({"work_item_id": "wi_157", "bundle_name": "develop", "repos": {"app": "app"}}),
        })
        return cap, heads

    def units_file(self, cap: str, units: List[Dict[str, Any]]) -> None:
        write(cap, {"artifacts/build/units.json": json.dumps(units)})

    def run_facts(self, cap: str, unit_timeout: str = "2s", deadline: str = "5m") -> Tuple[int, Dict[str, Any], str]:
        out = os.path.join(cap, "artifacts", "facts", "build.json")
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        p = subprocess.run([sys.executable, "-B", FACTS, "build", "--capsule", cap, "--replay", "--isolation", "host",
                            "--unit-timeout", unit_timeout, "--deadline", deadline, "--json", "--out", out],
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

    def test_a_red_state_run_killed_at_its_limit_is_an_evidence_problem(self) -> None:
        # a run that never exits, replayed with a 2s limit: it was red in
        # the making (it hangs), and it is judged an evidence problem —
        # exit 1, the problem naming the limit — never "not judged" (2)
        cap, heads = self.capsule([
            ("the unit's test commit", {"test/hang_test.sh": "sleep 30\n"}),
            ("the unit's implementation", {"app.go": "package app\n// changed\n"})])
        self.units_file(cap, [
            {"id": "u1", "test_commit": heads["the unit's test commit"], "impl_commit": heads["the unit's implementation"],
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/hang_test.sh"]}, "marker": "^RED$"}])
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 1, res["summary"] + err)
        self.assertEqual(res["verdict"], "fail")
        self.assertIn("did not exit non-zero within the limit", res["summary"])
        self.assertIn("(2s)", res["summary"])
        r = self.unit_of(res, "u1")["replay"]
        self.assertTrue(r["timed_out"], r)
        self.assertTrue(r.get("limit_kill"), "the unit's own limit, not the run's window, killed it")

    def test_a_self_reported_timeout_raises_the_replays_limit(self) -> None:
        # the run takes 3s red (a non-zero exit), the flag's limit is 2s
        # and the unit self-reports 10s: the self-report governs, the run
        # reaches its non-zero exit and is proper red evidence — the facts
        # hold (exit 0)
        cap, heads = self.capsule([
            ("the unit's test commit", {"test/slow_test.sh": "sleep 3; exit 1\n"}),
            ("the unit's implementation", {"app.go": "package app\n// changed\n"})])
        self.units_file(cap, [
            {"id": "u1", "timeout": "10s", "test_commit": heads["the unit's test commit"],
             "impl_commit": heads["the unit's implementation"],
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/slow_test.sh"]}, "marker": "^RED$"}])
        code, res, err = self.run_facts(cap, unit_timeout="2s")
        self.assertEqual(code, 0, res["summary"] + err)
        r = self.unit_of(res, "u1")["replay"]
        self.assertFalse(r["timed_out"], r)
        self.assertTrue(r["red"], r)
        self.assertEqual(r["exit_code"], 1)

    def test_an_invalid_self_reported_timeout_is_not_judged(self) -> None:
        cap, heads = self.capsule([
            ("the unit's test commit", {"test/x_test.sh": "exit 1\n"}),
            ("the unit's implementation", {"app.go": "package app\n// changed\n"})])
        self.units_file(cap, [
            {"id": "u1", "timeout": "abc", "test_commit": heads["the unit's test commit"],
             "impl_commit": heads["the unit's implementation"],
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/x_test.sh"]}, "marker": "^RED$"}])
        code, res, err = self.run_facts(cap)
        self.assertEqual(code, 2, res["summary"] + err)
        self.assertIn("timeout", err)
        self.assertIn("Go duration", err)


    def test_a_kill_by_the_whole_runs_window_stays_not_judged(self) -> None:
        # the other side of the red-state limit: the run's window (3s) is
        # shorter than the unit's limit (30s), so the window kills first —
        # that kill is the machine's, not evidence: exit 2, not judged
        # (r1#N6a)
        cap, heads = self.capsule([
            ("the unit's test commit", {"test/hang_test.sh": "sleep 30\n"}),
            ("the unit's implementation", {"app.go": "package app\n// changed\n"})])
        self.units_file(cap, [
            {"id": "u1", "test_commit": heads["the unit's test commit"], "impl_commit": heads["the unit's implementation"],
             "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/hang_test.sh"]}, "marker": "^RED$"}])
        code, res, err = self.run_facts(cap, unit_timeout="30s", deadline="3s")
        self.assertEqual(code, 2, res["summary"] + err)
        self.assertIn("the window ran out first", res["summary"])
        r = self.unit_of(res, "u1")["replay"]
        self.assertTrue(r["timed_out"], r)
        self.assertNotIn("limit_kill", r, "the window killed it, not the unit's own limit")


if __name__ == "__main__":
    unittest.main()
