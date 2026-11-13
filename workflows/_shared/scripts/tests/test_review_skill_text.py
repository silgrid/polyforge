"""The review skill and the develop lane's review judgment say how to read
a unit whose replay could not run (facts.py build's `not_runnable`): it has
no red evidence — neither red nor a pass — and is blocking `test_weakened`
naming the unit and its rule, never made up for by reading the code; it is
not the exit 2 a review retries. Compared with whitespace folded, as the
lane's other text contracts are."""

import os
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
WORKFLOWS = os.path.dirname(os.path.dirname(os.path.dirname(HERE)))
REVIEW_SKILL = os.path.join(WORKFLOWS, "_shared", "skills", "review.md")
REVIEW_JUDGE = os.path.join(WORKFLOWS, "develop", "fragments", "review-judge.md")


def folded(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return " ".join(f.read().split())


class ReviewSkillTextTest(unittest.TestCase):
    def test_review_treats_not_runnable_as_missing_red_evidence(self) -> None:
        skill = folded(REVIEW_SKILL)
        judge = folded(REVIEW_JUDGE)
        for name, text in (("review.md", skill), ("review-judge.md", judge)):
            with self.subTest(name):
                low = text.lower()
                self.assertIn("a unit whose `replay` carries `not_runnable` has no red evidence", low)
                self.assertIn("neither red nor a pass", low)
                self.assertIn("blocking `test_weakened`", text)
                self.assertIn("`rule`", text)
        self.assertIn("reading the code by hand never stands in for the missing red", skill)
        self.assertIn("`base_setup=`", skill)
        # step 1's exit 2 (retried, then blocked) is not where not_runnable goes
        retry = skill[skill.index("Exit 2 is not judged"):skill.index("2. **Class the facts**")]
        self.assertNotIn("not_runnable", retry)
        self.assertIn("exit 1", skill[skill.index("`not_runnable`"):])
        # the sentences other contracts pin stay
        self.assertIn("`artifacts/facts/build.json` (`facts.py build --replay`) gives per unit its commits' test-first shape", judge)
        self.assertIn("Run two faces over the current tree:", skill)


if __name__ == "__main__":
    unittest.main()
