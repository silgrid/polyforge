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


if __name__ == "__main__":
    unittest.main()
