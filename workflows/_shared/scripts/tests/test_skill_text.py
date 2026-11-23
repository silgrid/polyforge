# carries: skills/polyforge/SKILL.md, skills/polyforge/verbs/file.md
"""polyforge 建票路由的文本契约。"""
import os
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))))
SKILL = os.path.join(ROOT, "skills", "polyforge", "SKILL.md")
FILE = os.path.join(ROOT, "skills", "polyforge", "verbs", "file.md")


class SkillTextTest(unittest.TestCase):
    def test_same_area_work_items_require_dependency_or_merge(self) -> None:
        with open(SKILL, encoding="utf-8") as f:
            text = f.read()
        self.assertIn("同一区域（同一组文件或同一概念）", text)
        self.assertIn("加先后依赖（`--after`），或合并成一张票", text)
        with open(FILE, encoding="utf-8") as f:
            file_text = f.read()
        self.assertIn("same area", file_text)
        self.assertIn("`--after <slug>`", file_text)
        self.assertIn("merge the", file_text)


if __name__ == "__main__":
    unittest.main()


class FastChecksParallelTextTest(unittest.TestCase):
    """polyforge#254 AC3: the build skill and the fast_checks reference say
    what the grouped plan does — the groups side by side, each group's own
    commands in plan order, two at once by default, one group or
    ``--parallel 1`` the serial plan, and (a boxed close) one container per
    group. A document that said "each command in parallel" or "a repository
    without groups runs in parallel too" would be a different contract."""

    ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))))

    def flat(self, rel: str) -> str:
        with open(os.path.join(self.ROOT, rel), encoding="utf-8") as f:
            return " ".join(f.read().split())

    def test_the_build_skill_states_the_grouped_plan(self) -> None:
        text = self.flat("workflows/_shared/skills/build.md")
        for want in ("group by group", "each group's own commands in the plan's order",
                     "`--parallel N`", "default 2", "one group, or `--parallel 1`",
                     "one container per group"):
            self.assertIn(want, text)

    def test_the_fast_checks_reference_states_the_grouped_plan(self) -> None:
        for rel in ("workflows/_shared/fast_checks_build.md", "docs/reference/commands.md",
                    "docs/contributing.md"):
            text = self.flat(rel)
            for want in ("group by group", "each group's own commands in the plan's order",
                         "default 2", "one group, or `--parallel 1`"):
                self.assertIn(want, text, rel)

    def test_the_develop_build_step_states_the_grouped_close(self) -> None:
        text = self.flat("workflows/develop/steps/build.md")
        for want in ("group by group", "each group's own commands in plan order",
                     "`--parallel N`", "default 2", "each group's own container"):
            self.assertIn(want, text)
