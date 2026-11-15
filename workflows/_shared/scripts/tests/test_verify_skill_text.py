"""The verify skill says how to read verify.py's `failure` and `not_rerun`
units: a `browser_crash` (Chromium's renderer crashed) is
never placed on the change from its log — still red it is a red the agent
cannot place, flaky it is a browser crash in the hand-off, never a flaky
test of the change — and a `not_rerun` unit is neither a proven red nor
flaky: the owner is asked with the list. Compared with whitespace folded,
as the lane's other text contracts are."""

import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
WORKFLOWS = os.path.dirname(os.path.dirname(os.path.dirname(HERE)))
VERIFY_SKILL = os.path.join(WORKFLOWS, "_shared", "skills", "verify.md")


def folded(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return " ".join(f.read().split())


class VerifySkillTextTest(unittest.TestCase):
    def test_verify_skill_reads_browser_crash_and_not_rerun(self) -> None:
        skill = folded(VERIFY_SKILL)
        # exit 1's routing carries both fields
        exit1 = skill[skill.index("- **1** —"):skill.index("- **2** —")]
        for want in ("`failure`", "`browser_crash`", "`assertion`", "`Target crashed`", "`Page crashed`",
                     "most often the container's memory OOM"):
            self.assertIn(want, exit1)
        self.assertIn("never place a `browser_crash` on the change from its log", exit1)
        self.assertIn("still red, it is a red you cannot place", exit1)
        self.assertIn("`not_rerun` is neither a proven red nor flaky", exit1)
        self.assertIn("`pf step ask` the owner with those units by check", exit1)
        # the hand-off names a flaky browser crash for what it is
        handoff = skill[skill.index("## 交接要写什么"):skill.index("## Never")]
        self.assertIn("a flaky `browser_crash` is a browser crash, never a flaky test of the change", handoff)
        # the sentences TestW371TextContract pins stay
        self.assertIn("A red you cannot place: `pf step ask` the owner with the red checks, the log tails and your"
                      " reading, then end your turn", exit1)
        self.assertIn("run any other build, lint or test command, or re-run one check by hand; hand off `done` on a"
                      " red you could not place", skill)

    def test_the_line_runs_the_tier_and_the_analysis_the_ticket_names(self) -> None:
        """polyforge#52: the line asks --tier auto (the work item's own
        attrs decide — an integration_fix ticket runs the full bar) and
        passes the build's change analysis; a missing or unparseable one
        is the gate's FAIL to build, and the same-tree rule covers the
        analysis too."""
        skill = folded(VERIFY_SKILL)
        for want in (
            "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/verify.py\" --tier auto --unit-runs"
            " artifacts/build/units.json --analysis artifacts/build/change-analysis.json"
            " --json --out artifacts/verify/result.json`",
            "`--tier auto` reads the work item's own attrs: an `integration_fix` ticket (an integration batch's"
            " fix ticket) runs the full bar; every other ticket the light one",
            "the change analysis's listed tests run beside the engine's plan, add-only, under its supplement cap",
            "a missing or unparseable analysis is this gate's FAIL",
            "`change-analysis sha256: <digest>`",
        ):
            self.assertIn(want, skill)
        # the pre-check names what a red of a listed test is
        self.assertIn("a failing `<repo>:analysis:<id>` check is a test the analysis listed", skill)


if __name__ == "__main__":
    unittest.main()
