# carries: workflows/integrate/skills/full-gate.md, workflows/_shared/scripts/tests/test_gate_keep_going.py
"""The integrate lane's full gate runs every command: the skill's two
gate.py lines (the gate and its --triage) carry --keep-going, and the
lines as the lane runs them show every red of one batch at once — a gate
whose two full commands both fail records both failed with no command
left not_run, and the triage of such a result reports each command's own
failed unit. The develop and micro lanes' verification commands carry no
--keep-going (the ticket leaves them as they are: verify.py runs its
whole plan, run.py stops at its first failing line). Compared with whitespace folded, as the
lane's other text contracts are."""

import json
import os
import shlex
import tempfile
import unittest
from typing import Dict, List, Tuple

from .fakebox import FakeRuntime
from .gatefix import HAVE_BASH, HAVE_GIT, HAVE_PY, commit_all, gate_clone, git_t, read_json, run_main, write_files

HERE = os.path.dirname(os.path.abspath(__file__))
WORKFLOWS = os.path.dirname(os.path.dirname(os.path.dirname(HERE)))
REPO_ROOT = os.path.dirname(WORKFLOWS)
FULL_GATE_SKILL = os.path.join(WORKFLOWS, "integrate", "skills", "full-gate.md")
VERIFY_SKILL = os.path.join(WORKFLOWS, "_shared", "skills", "verify.md")
EVIDENCE_SKILL = os.path.join(WORKFLOWS, "micro", "skills", "evidence.md")

GATE_LINE = ("`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/gate.py\" --isolation container --keep-going"
             " --capsule . --pins artifacts/integrate/revert.json --mark-green"
             " --summary \"integration batch $PF_WI_ID\" --json --out artifacts/integrate/gate.json`")
TRIAGE_LINE = ("`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/gate.py\" --isolation container --triage --keep-going"
               " --capsule . --pins artifacts/integrate/revert.json --gate artifacts/integrate/gate.json"
               " --batch artifacts/integrate/batch.json --mark-green"
               " --summary \"integration batch $PF_WI_ID\" --json --out artifacts/integrate/triage.json`")
VERIFY_LINE = ("`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/verify.py\" --tier auto --unit-runs"
               " artifacts/build/units.json --analysis artifacts/build/change-analysis.json"
               " --json --out artifacts/verify/result.json`")
EVIDENCE_LINE = ("`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/run.py\" --isolation host --plan-from"
                 " artifacts/fix/evidence.json --json --out artifacts/run/result.json`")

# The red head's conf: two full commands, each failing one e2e suite its
# rerun= template can re-run alone.
TWO_REDS_CONF = ("stack=none\nfast_gate=echo one\n"
                 "full_command=sh run-e2e-a.sh\nfull_command=sh run-e2e-b.sh\n"
                 "rerun=full-1|sh run-e2e-a.sh {unit}\nrerun=full-2|sh run-e2e-b.sh {unit}\n")
# Each script reports one failing suite; with a unit argument (the
# targeted re-run) it fails the same suite again.
E2E_A = """#!/bin/sh
if [ $# -eq 0 ]; then
  echo "===== PASS e2e/ok-a.mjs (1s) ====="
  echo "===== failures ====="
  echo "FAIL e2e/red-a.mjs: expected 1 got 2"
  exit 1
fi
echo "===== failures ====="
echo "FAIL $1: still red"
exit 1
"""
E2E_B = """#!/bin/sh
if [ $# -eq 0 ]; then
  echo "===== PASS e2e/ok-b.mjs (1s) ====="
  echo "===== failures ====="
  echo "FAIL e2e/red-b.mjs: expected 3 got 4"
  exit 1
fi
echo "===== failures ====="
echo "FAIL $1: still red"
exit 1
"""


def folded(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return " ".join(f.read().split())


class GateKeepGoingSkillTextTest(unittest.TestCase):
    def test_the_full_gate_lines_carry_keep_going_and_the_delivery_lanes_ones_do_not(self) -> None:
        # AC2: the lane's two gate.py lines carry --keep-going, the
        # develop and micro lanes' verification commands do not.
        skill = folded(FULL_GATE_SKILL)
        for line in (GATE_LINE, TRIAGE_LINE):
            self.assertIn(line, skill)
        for path, line in ((VERIFY_SKILL, VERIFY_LINE), (EVIDENCE_SKILL, EVIDENCE_LINE)):
            text = folded(path)
            self.assertIn(line, text)
            self.assertNotIn("--keep-going", text, "the delivery lanes' verification commands stay as they are, without --keep-going")


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class GateKeepGoingLaneRunTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def lane_argv(self, triage: bool) -> List[str]:
        """The skill's gate.py line (the triage's when triage) as the
        lane's shell runs it: its words, the interpreter and the script
        dropped (run_main takes the flags)."""
        with open(FULL_GATE_SKILL, encoding="utf-8") as f:
            for ln in f.read().splitlines():
                if "scripts/gate.py" not in ln or "--isolation" not in ln:
                    continue
                if ("--triage" in ln) != triage:
                    continue
                words = shlex.split(ln.strip().strip("`"))
                if len(words) < 3 or not words[1].endswith("gate.py"):
                    raise AssertionError("the full-gate skill's line does not parse: " + ln)
                return words[2:]
        raise AssertionError("the full-gate skill carries no such gate.py line")

    def capsule(self) -> Tuple[str, str]:
        """A batch capsule whose one repo's pinned head fails two full
        commands: the base commit (a green tag on origin) carries a clean
        conf, the reds commit on top of it the two failing commands, their
        scripts and their e2e files. Answers (capsule, head)."""
        seed = gate_clone(self.root, "stack=none\nfast_gate=echo one\nfull_command=echo full-ok\n", name="seed")
        write_files(seed, {".polyforge/verify-scope.conf": TWO_REDS_CONF, "e2e/red-a.mjs": "// carries: web/A*\n",
                           "e2e/red-b.mjs": "// carries: web/B*\n"})
        write_files(seed, {"run-e2e-a.sh": E2E_A, "run-e2e-b.sh": E2E_B}, 0o755)
        commit_all(seed, "reds")
        head = git_t(seed, "rev-parse", "HEAD")
        base = git_t(seed, "rev-parse", "HEAD~1")
        origin = os.path.join(self.root, "app.git")
        git_t(self.root, "init", "-q", "--bare", "-b", "main", origin)
        git_t(seed, "remote", "add", "origin", origin)
        git_t(seed, "push", "-q", "origin", "main")
        git_t(seed, "tag", "-a", "-m", "{}", "green/20261101-1", base)
        git_t(seed, "push", "-q", "origin", "refs/tags/green/20261101-1")
        capsule = os.path.join(self.root, "capsule")
        art = os.path.join(capsule, "artifacts", "integrate")
        os.makedirs(os.path.join(capsule, "repos"))
        os.makedirs(art)
        git_t(self.root, "clone", "-q", origin, os.path.join(capsule, "repos", "app"))
        with open(os.path.join(art, "revert.json"), "w", encoding="utf-8") as f:
            json.dump({"heads": {"app": head}, "gated": ["app"]}, f)
        with open(os.path.join(art, "batch.json"), "w", encoding="utf-8") as f:
            json.dump({"schema": "pf-step-result/1", "command": "integrate.py", "verdict": "pass",
                       "details": {"items": []}}, f)
        return capsule, head

    def stub_pf_dir(self) -> str:
        """A bin dir whose pf answers the hub list the triage reads with
        an empty pool (the step itself reads it through its own pf)."""
        d = os.path.join(self.root, "stub-bin")
        os.makedirs(d)
        p = os.path.join(d, "pf")
        with open(p, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\necho '{\"items\": []}'\n")
        os.chmod(p, 0o755)
        return d

    def test_the_gate_line_runs_both_failing_commands(self) -> None:
        # AC3: two commands both fail and the --keep-going gate records
        # both failed, no command left not_run.
        capsule, head = self.capsule()
        fb = FakeRuntime(os.path.join(self.root, "box-tmp"))
        m = run_main(self.lane_argv(False), capsule, env={"PF_WI_ID": "wi_kg"}, runtime=fb)
        self.assertEqual(m.exit, 1, m.stdout + m.stderr)
        res = read_json(os.path.join(capsule, "artifacts", "integrate", "gate.json"))
        self.assertEqual(res["verdict"], "fail")
        repo = res["details"]["repos"]["app"]
        self.assertEqual((repo["head"], repo["branch"]), (head, "main"))
        gate_res = repo["result"]
        self.assertFalse(gate_res["passed"])
        self.assertNotIn("incomplete", gate_res)
        cmds = gate_res["commands"]
        self.assertEqual([c["name"] for c in cmds], ["fast-1", "full-1", "full-2"])
        for c in cmds:
            self.assertNotIn("not_run", c, "keep-going leaves no command not_run: %s" % c["name"])
        self.assertEqual([c["exit"] for c in cmds], [0, 1, 1], "both failing commands ran and are recorded failed")

    def test_the_triage_line_reports_each_commands_failed_unit(self) -> None:
        # AC3: --triage over the keep-going gate's result reports each
        # failing command's own failed unit.
        capsule, head = self.capsule()
        art = os.path.join(capsule, "artifacts", "integrate")
        fb = FakeRuntime(os.path.join(self.root, "box-tmp"))
        m = run_main(self.lane_argv(False), capsule, env={"PF_WI_ID": "wi_kg"}, runtime=fb)
        self.assertEqual(m.exit, 1, m.stdout + m.stderr)
        stub = self.stub_pf_dir()
        env: Dict[str, str] = {"PF_WI_ID": "wi_kg", "PATH": stub + os.pathsep + os.environ.get("PATH", "")}
        m = run_main(self.lane_argv(True), capsule, env=env, runtime=fb)
        self.assertEqual(m.exit, 1, m.stdout + m.stderr)
        res = read_json(os.path.join(art, "triage.json"))
        self.assertEqual(res["heads"], {"app": head})
        details = res["details"]
        self.assertEqual(details["repos"], ["app"])
        self.assertFalse(details["all_flaky"])
        self.assertNotIn("regate", details)
        self.assertEqual([(u["command"], u["unit"], u["kind"]) for u in details["units"]],
                         [("full-1", "e2e/red-a.mjs", "e2e-suite"), ("full-2", "e2e/red-b.mjs", "e2e-suite")],
                         "the triage sees each failing command's own unit")
        self.assertEqual(details["classes"], {"unattributed": 2})


if __name__ == "__main__":
    unittest.main()
