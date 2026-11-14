# carries: skills/polyforge/SKILL.md
"""polyforge 建票路由的文本契约。"""
import os
import unittest


ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))))
SKILL = os.path.join(ROOT, "skills", "polyforge", "SKILL.md")


class SkillTextTest(unittest.TestCase):
    def test_same_area_work_items_require_dependency_or_merge(self) -> None:
        with open(SKILL, encoding="utf-8") as f:
            text = f.read()
        self.assertIn("同一区域（同一组文件或同一概念）", text)
        self.assertIn("加先后依赖（`--after`），或合并成一张票", text)


if __name__ == "__main__":
    unittest.main()
