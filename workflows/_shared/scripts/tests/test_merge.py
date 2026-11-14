# carries: workflows/_shared/scripts/merge.py, skills/polyforge/SKILL.md
"""回归测试：merge 在执行外部合入前必须重新确认默认分支版本。"""
import importlib.util
import os
from typing import Callable, cast

 = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "merge.py")
_spec = importlib.util.spec_from_file_location("merge_entry", MERGE_PATH)
assert _spec is not None and _spec.loader is not None
merge = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(merge)


class MergeDefaultBranchFreshnessTest(unittest.TestCase):
    def test_same_verified_base_allows_merge(self) -> None:
        check = cast(Callable[[str, str], str], getattr(merge, "default_branch_update_reason", None))
        self.assertIsNotNone(check)
        self.assertEqual(check("base-1", "base-1"), "")

    def test_new_default_commit_routes_back_to_sync(self) -> None:
        check = cast(Callable[[str, str], str], getattr(merge, "default_branch_update_reason", None))
        self.assertIsNotNone(check)
        reason = check("base-1", "base-2")
        self.assertIn("default branch moved since verification", reason)
        self.assertIn("sync", reason)


if __name__ == "__main__":
    unittest.main()
