# carries: workflows/_shared/scripts/facts.py, workflows/_shared/scripts/tests/test_facts_handoff_existing.py
"""facts.py `handoff` — D6, the plan's write set against the existing test
files the build changed (polyforge#241 AC2). The ticket's rule: a test file
that already exists at the change base is read-only for the build unless
the plan's `Touched files:` declares it `(write)`; a newly added test file
is no existing test and owes no declaration, and a unit's test files are no
exemption (owner Q5 A).

Expected values come from the rule, not the implementation: only a path
that exists at the merge-base, is a test path by the engine's own rules,
and is absent from the plan's write set is a hit. The red run of this file
is the assertion that D6 is missing from the handoff kind (no `D6` hit, or
a refused hand-off where a pass is expected)."""

import json
import os
import subprocess
import sys
import unittest
from typing import Any, Dict, List, Optional, Sequence, Tuple

from .gitworld import GitCase, Repo, write

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
FACTS = os.path.join(SCRIPTS, "facts.py")

CLEAN_ANALYSIS = {"supplement_limit": 10, "changed": [], "uncovered": {}, "tests": [], "selected": {}}
UNIT_TEST = 'test -f answer.txt && { echo ok; exit 0; }\nexit 1\n'
NEW_TEST = {"test/check_test.sh": UNIT_TEST}


def plan(writes: Sequence[str], raw: Optional[str] = None) -> str:
    if raw is not None:
        entry = raw
    else:
        entry = ", ".join("`%s` (write)" % p for p in writes)
    return "## Units\n\n**u1** — the change.\nTouched files: %s\nCoupled obligations: none — a private constant no document lists\n" % entry


class ExistingTestWriteSetTest(GitCase):
    maxDiff = None

    # --- fixtures --------------------------------------------------------

    def capsule(self, name: str, base: Optional[Dict[str, str]] = None) -> Tuple[str, Repo]:
        cap = os.path.join(self.tmp, name)
        repo = Repo(os.path.join(cap, "repos", "app"), base or {"app.go": "package app\n"})
        repo.git("update-ref", "refs/remotes/origin/main", repo.head())
        write(cap, {"capsule.json": json.dumps(
            {"work_item_id": "wi_241", "bundle_name": "develop", "repos": {"app": "app"}})})
        return cap, repo

    def record(self, cap: str, test: str, impl: str, writes: Sequence[str],
               raw_plan: Optional[str] = None) -> None:
        write(cap, {
            "artifacts/plan.md": plan(writes, raw_plan),
            "artifacts/build/units.json": json.dumps([
                {"id": "u1", "test_commit": test, "impl_commit": impl,
                 "run": {"repo": "app", "cwd": ".", "argv": ["sh", "test/check_test.sh"]},
                 "marker": "^ok$"}]),
            "artifacts/build/change-analysis.json": json.dumps(CLEAN_ANALYSIS),
        })

    def run_handoff(self, cap: str) -> Tuple[int, Dict[str, Any]]:
        out = os.path.join(cap, "artifacts", "build", "handoff-facts", "app.json")
        os.makedirs(os.path.dirname(out), exist_ok=True)
        env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "PF_", "BASH_"))}
        argv = [sys.executable, "-B", FACTS, "handoff", "--capsule", cap, "--json", "--out", out]
        p = subprocess.run(argv, cwd=cap, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if not os.path.exists(out):
            self.fail("facts.py handoff wrote no result (exit %d): %s" % (p.returncode, p.stderr.decode()))
        with open(out, encoding="utf-8") as f:
            res = json.load(f)
        self.assertEqual(p.returncode, res["exit_code"], "the result file carries the run's own exit code")
        return p.returncode, res

    def d6(self, res: Dict[str, Any]) -> List[str]:
        return [str(h.get("path", "")) for h in res["details"].get("hits", []) if h.get("item") == "D6"]

    def standard(self, name: str, base: Optional[Dict[str, str]] = None,
                 change: Optional[Dict[str, str]] = None, writes: Optional[Sequence[str]] = None,
                 test_files: Optional[Dict[str, str]] = None,
                 raw_plan: Optional[str] = None) -> Tuple[str, Repo, int, Dict[str, Any]]:
        cap, repo = self.capsule(name, base)
        test = repo.commit("u1: test", test_files or dict(NEW_TEST))
        impl = repo.commit("u1: impl", {"answer.txt": "42\n", **(change or {})})
        self.record(cap, test, impl, writes or ["answer.txt"], raw_plan)
        code, res = self.run_handoff(cap)
        return cap, repo, code, res

    # --- D6 --------------------------------------------------------------

    def test_an_undeclared_existing_test_is_a_hit(self) -> None:
        _, _, code, res = self.standard(
            "d6-und", {"pkg/foo_test.go": "package pkg\n"},
            {"pkg/foo_test.go": "package pkg\n\n// changed\n"})
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("pkg/foo_test.go", self.d6(res))
        hits = [h for h in res["details"]["hits"] if h.get("item") == "D6"]
        self.assertIn("existing test", hits[0]["reason"])

    def test_a_declared_existing_test_passes(self) -> None:
        _, _, code, res = self.standard(
            "d6-decl", {"pkg/foo_test.go": "package pkg\n"},
            {"pkg/foo_test.go": "package pkg\n\n// changed\n"},
            writes=["answer.txt", "pkg/foo_test.go"])
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(self.d6(res), [])

    def test_a_new_test_file_is_not_a_hit(self) -> None:
        # the unit's own new test file, absent at the change base and not
        # declared by the plan: no existing test, so D6 takes no notice
        _, _, code, res = self.standard("d6-new")
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(self.d6(res), [])

    def test_a_testdata_golden_is_a_hit(self) -> None:
        _, _, code, res = self.standard(
            "d6-golden", {"testdata/golden.txt": "one\n"},
            {"testdata/golden.txt": "two\n"})
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("testdata/golden.txt", self.d6(res))

    def test_a_deleted_existing_test_is_a_hit(self) -> None:
        cap, repo = self.capsule("d6-del", {"app.go": "package app\n", "pkg/gone_test.go": "package pkg\n"})
        test = repo.commit("u1: test", dict(NEW_TEST))
        repo.git("rm", "-q", "pkg/gone_test.go")
        write(repo.dir, {"answer.txt": "42\n"})
        repo.git("add", "-A")
        repo.git("commit", "-qm", "u1: impl")
        impl = repo.git("rev-parse", "HEAD")
        self.record(cap, test, impl, ["answer.txt"])
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("pkg/gone_test.go", self.d6(res), "a deletion is a modification (--no-renames)")

    def test_a_renamed_existing_test_hits_its_old_path(self) -> None:
        cap, repo = self.capsule("d6-mv", {"app.go": "package app\n", "pkg/old_test.go": "package pkg\n"})
        test = repo.commit("u1: test", dict(NEW_TEST))
        repo.git("mv", "pkg/old_test.go", "pkg/new_test.go")
        write(repo.dir, {"answer.txt": "42\n"})
        repo.git("add", "-A")
        repo.git("commit", "-qm", "u1: impl")
        impl = repo.git("rev-parse", "HEAD")
        self.record(cap, test, impl, ["answer.txt", "pkg/new_test.go"])
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertEqual(self.d6(res), ["pkg/old_test.go"], "the old path is a deletion; the new one is no existing test")

    def test_a_malformed_plan_entry_declares_nothing(self) -> None:
        cap, repo = self.capsule("d6-mal", {"app.go": "package app\n", "pkg/foo_test.go": "package pkg\n"})
        test = repo.commit("u1: test", dict(NEW_TEST))
        impl = repo.commit("u1: impl", {"answer.txt": "42\n", "pkg/foo_test.go": "package pkg\n\n// changed\n"})
        self.record(cap, test, impl, [], raw_plan="`answer.txt` (write), `pkg/foo_test.go (write)`")
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("pkg/foo_test.go", self.d6(res))

    def test_a_units_test_file_is_no_declaration(self) -> None:
        # the existing test is the unit's own test commit's file: the plan
        # still has to declare it (Q5 A — units.json is no write set)
        cap, repo = self.capsule("d6-unit", {"app.go": "package app\n", "pkg/foo_test.go": "package pkg\n"})
        test = repo.commit("u1: test", {"test/check_test.sh": UNIT_TEST,
                                        "pkg/foo_test.go": "package pkg\n\n// the unit's test change\n"})
        impl = repo.commit("u1: impl", {"answer.txt": "42\n"})
        self.record(cap, test, impl, ["answer.txt"])
        code, res = self.run_handoff(cap)
        self.assertEqual(code, 1, res["summary"])
        self.assertIn("pkg/foo_test.go", self.d6(res))


if __name__ == "__main__":
    unittest.main()