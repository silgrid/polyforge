"""published.py: the published record of a work item whose
lane does not merge its PRs — run from a capsule root the way the publish
skill's line runs it, on real repositories: the record, the refusal of a
published head that is not the verified one, the usage errors, and the
result file (its JSON Schema)."""

import json
import os
import subprocess
import sys
from typing import Any, Dict, List, Tuple

from .gitworld import GitCase, Repo
from .schemacheck import errors as schema_errors

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
SCRIPT = os.path.join(SCRIPTS, "published.py")

with open(os.path.join(SCRIPTS, "schema", "pf-step-result-1.json"), encoding="utf-8") as _f:
    SCHEMA = json.load(_f)

BRANCH = "polyforge/wi"
LINE = ["--publish", "artifacts/publish.json", "--verified", "artifacts/verify/result.json",
        "--record-out", "artifacts/publish/integration.json", "--json", "--out", "artifacts/publish/record.json"]


def pr_of(repo: str) -> str:
    return "https://forge.invalid/demo/%s/pull/3" % repo


class PublishedTest(GitCase):
    def setUp(self) -> None:
        super().setUp()
        self.capsule = self.new_dir("capsule")
        self.repos: Dict[str, Repo] = {}
        self.heads: Dict[str, str] = {}
        for name in ("alpha", "beta"):
            r = Repo(os.path.join(self.capsule, "repos", name))
            self.heads[name] = r.commit("feature", {"feature.txt": name + "\n"})
            self.repos[name] = r

    def write_json(self, rel: str, v: Any) -> None:
        p = os.path.join(self.capsule, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            json.dump(v, f)

    def inputs(self, published: Dict[str, str], verified: Dict[str, str]) -> None:
        entries: List[Dict[str, str]] = [{"name": n, "branch": BRANCH, "pr_url": pr_of(n), "head": h} for n, h in published.items()]
        entries.append({"name": "docs", "skipped_reason": "no change"})
        self.write_json("artifacts/publish.json", {"repos": entries})
        self.write_json("artifacts/verify/result.json", {"schema": "pf-step-result/1", "command": "verify.py", "heads": verified})

    def run_line(self, args: List[str]) -> Tuple[int, str]:
        env = {k: v for k, v in os.environ.items() if not k.startswith("PF_")}
        c = subprocess.run([sys.executable, "-B", SCRIPT, *args], cwd=self.capsule, env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        return c.returncode, c.stderr.decode()

    def result(self) -> Dict[str, Any]:
        with open(os.path.join(self.capsule, "artifacts", "publish", "record.json"), encoding="utf-8") as f:
            res: Dict[str, Any] = json.load(f)
        self.assertEqual(schema_errors(SCHEMA, res), [], res)
        self.assertEqual(res["command"], "published.py")
        return res

    def record_path(self) -> str:
        return os.path.join(self.capsule, "artifacts", "publish", "integration.json")

    def test_the_record_carries_the_verified_heads(self) -> None:
        self.inputs(self.heads, self.heads)
        code, err = self.run_line(LINE)
        self.assertEqual(code, 0, err)
        with open(self.record_path(), encoding="utf-8") as f:
            rec = json.load(f)
        self.assertEqual(rec, {"state": "published", "repos": [
            {"repo": n, "pr": pr_of(n), "head": self.heads[n], "light_verified_head": self.heads[n]} for n in ("alpha", "beta")]})
        res = self.result()
        self.assertEqual((res["exit_code"], res["verdict"]), (0, "pass"))
        self.assertEqual(res["heads"], self.heads)
        self.assertEqual([r["name"] for r in res["details"]["repos"]], ["alpha", "beta"])

    def test_a_head_not_the_verified_one_is_refused(self) -> None:
        self.inputs(self.heads, self.heads)
        self.assertEqual(self.run_line(LINE)[0], 0)
        later = self.repos["beta"].commit("later", {"later.txt": "x\n"})
        self.inputs({"alpha": self.heads["alpha"], "beta": later}, self.heads)
        code, err = self.run_line(LINE)
        self.assertEqual(code, 1, err)
        self.assertFalse(os.path.exists(self.record_path()), "a refusal leaves no record, a stale one removed")
        res = self.result()
        self.assertEqual(res["verdict"], "fail")
        self.assertIn("repos/beta", res["summary"])

    def test_usage_errors(self) -> None:
        self.inputs(self.heads, self.heads)
        self.assertEqual(self.run_line([])[0], 2)
        self.assertEqual(self.run_line(["--publish", "artifacts/publish.json"])[0], 2)
        self.assertEqual(self.run_line(LINE + ["extra"])[0], 2)
        res = self.result()
        self.assertEqual((res["exit_code"], res["verdict"]), (2, "error"))
        # a published repo the verification names no head for is not judged
        self.inputs(self.heads, {"alpha": self.heads["alpha"]})
        code, err = self.run_line(LINE)
        self.assertEqual(code, 2, err)
        self.assertIn("beta", err)
        self.assertFalse(os.path.exists(self.record_path()))
        # an unreadable publish file
        self.write_json("artifacts/publish.json", {"repos": "nope"})
        self.assertEqual(self.run_line(LINE)[0], 2)
