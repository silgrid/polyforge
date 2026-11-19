# carries: workflows/_shared/scripts/verify.py, workflows/_shared/scripts/pfstep/scoped_plan.py
"""The build close's scoped selection (polyforge#164): the one light-tier
planning implementation shared by verification and the close
(SharedPlanTest), the close's own run and its parity with verification's
plan (SelectionParityTest, ExecutionTest) and the change analysis's
machine selection record (AnalysisRecordTest)."""

import json
import os
import sys  # noqa: F401  (ExecutionTest, added with u2)
import unittest
from typing import Any, Callable, Dict, List, Optional, Tuple  # noqa: F401  (u2/u3)

from pfstep import scoped_plan
from pfstep.engine import EngineReport, ReportError, decode_report

from .gitworld import HAVE_GIT, Repo, write
from .verifyfix import HAVE_BASH, Fixture, cmd_of, report, run_verify


Ask = Callable[[str, str, str], Tuple[Optional[EngineReport], str, str]]


def ask_of(reports: Dict[str, Any], asked: List[Tuple[str, str, str]]) -> Ask:
    """An ask callable answering reports[<directory's base name>] (None and
    a why when it has none), recording every (directory, cwd_rel, base)."""
    def ask(directory: str, cwd_rel: str, base: str) -> Tuple[Optional[EngineReport], str, str]:
        asked.append((directory, cwd_rel, base))
        rep = reports.get(os.path.basename(directory))
        ref = "the engine --json --light" + ((" --base " + base) if base else "")
        if rep is None:
            return None, ref, "the engine exited 3: no report"
        try:
            return decode_report(json.dumps(rep).encode()), ref, ""
        except ReportError as err:
            return None, ref, "the engine's report is unreadable: %s" % err
    return ask


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class SharedPlanTest(unittest.TestCase):
    """The one light-tier planning implementation (pfstep/scoped_plan.py):
    the engine argv both planners ask with, the light selection of one
    report, the nested engine roots, the full-tier fallback's reasons, the
    plan's statuses and the branch the change is measured against — the
    decisions verification's light tier and the build close share, so the
    two cannot drift."""

    def setUp(self) -> None:
        import tempfile
        import shutil
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def test_engine_argv_carries_the_tier_and_the_base(self) -> None:
        """The invocation both planners ask with: --json, the tier's flag
        and --base <base> when one is named (never --base with an empty
        base); a non-executable engine runs under python3, an executable
        one runs itself."""
        script = os.path.join(self.root, "verify-scope")
        self.assertEqual(scoped_plan.engine_argv(script, "abc"),
                         ["python3", script, "--json", "--light", "--base", "abc"])
        self.assertEqual(scoped_plan.engine_argv(script, ""),
                         ["python3", script, "--json", "--light"])
        self.assertEqual(scoped_plan.engine_argv(script, "abc", light=False),
                         ["python3", script, "--json", "--full", "--base", "abc"])
        with open(script, "w", encoding="utf-8") as f:
            f.write("#!/bin/sh\n")
        os.chmod(script, 0o755)
        self.assertEqual(scoped_plan.engine_argv(script, "abc")[0], script)

    def test_plan_light_chooses_the_light_selection(self) -> None:
        """A scoped report contributes its fast gates plus its scoped
        selection, never its full tail; the ask sees the repository's own
        directory, its record name and the base."""
        asked: List[Tuple[str, str, str]] = []
        rep = report("scoped", cmd_of("fast-1", "fast", "true"),
                     cmd_of("fast-2", "fast", "true"),
                     cmd_of("scoped-1", "scoped", "echo s"),
                     cmd_of("full-1", "full", "echo never"))
        plan = scoped_plan.plan_light(self.root, "repos/app", "b0", ask_of({"app": rep}, asked))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual(plan.mode, "light")
        self.assertEqual([c.name for c in plan.commands], ["fast-1", "fast-2", "scoped-1"])
        self.assertEqual([c.phase for c in plan.commands], ["fast", "fast", "scoped"])
        self.assertEqual(asked, [(self.root, "repos/app", "b0")])

    def test_plan_light_wraps_nested_roots(self) -> None:
        """Every nested engine root the changed paths reach is a
        repository of its own: its commands run behind `cd <root> &&`,
        named <root>/<name>, its base setup behind the same cd."""
        clone = Repo(os.path.join(self.root, "app"))
        write(clone.dir, {".polyforge/verify-scope.conf": "stack=none\n",
                          "tools/site/.polyforge/verify-scope.conf": "stack=none\n"})
        clone.git("add", "-A")
        clone.git("commit", "-q", "-m", "conf")
        root = report("scoped", cmd_of("fast-1", "fast", "true"),
                      cmd_of("scoped-1", "scoped", "echo s"), paths=["tools/site/page.ts"])
        nested = report("scoped", cmd_of("n-fast", "fast", "true"),
                        cmd_of("n-scoped", "scoped", "echo n"), base_setup=["npm ci"])
        asked: List[Tuple[str, str, str]] = []
        plan = scoped_plan.plan_light(clone.dir, "repos/app", "b0",
                                      ask_of({"app": root, "site": nested}, asked))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual(plan.mode, "light")
        self.assertEqual([(c.name, c.phase) for c in plan.commands],
                         [("fast-1", "fast"), ("scoped-1", "scoped"),
                          ("tools/site/n-fast", "fast"), ("tools/site/n-scoped", "scoped")])
        self.assertEqual(plan.commands[2].cmd, "cd tools/site && true")
        self.assertEqual(plan.commands[3].dir, "tools/site")
        self.assertEqual(plan.base_setup, ["cd tools/site && npm ci"])
        self.assertEqual([(d, rel) for d, rel, _b in asked],
                         [(clone.dir, "repos/app"), (os.path.join(clone.dir, "tools/site"), "repos/app/tools/site")])

    def test_plan_light_keeps_a_full_answer_and_its_reasons(self) -> None:
        """A report that answers full makes the plan full — its fast gates
        plus its full tail, the scoped selection gone — and the reason is
        its own fallback diagnostic; no nested root is asked."""
        asked: List[Tuple[str, str, str]] = []
        rep = report("full", cmd_of("fast-1", "fast", "true"), cmd_of("full-1", "full", "echo f"),
                     diagnostics=["go.mod changed: falling back to the full tier"])
        plan = scoped_plan.plan_light(self.root, "repos/app", "b0", ask_of({"app": rep}, asked))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual(plan.mode, "full")
        self.assertEqual([c.name for c in plan.commands], ["fast-1", "full-1"])
        self.assertEqual(scoped_plan.fallback_reasons(rep), ["go.mod changed: falling back to the full tier"])
        self.assertEqual(len(asked), 1, "a full root answer asks no nested root")

    def test_plan_light_names_a_nested_full_fallback(self) -> None:
        """A nested root whose own light report answers full keeps its
        commands (its fast gates plus its full tail) and names why the
        run is full, one entry per its fallback diagnostic."""
        clone = Repo(os.path.join(self.root, "app"))
        write(clone.dir, {".polyforge/verify-scope.conf": "stack=none\n",
                          "tools/site/.polyforge/verify-scope.conf": "stack=none\n"})
        clone.git("add", "-A")
        clone.git("commit", "-q", "-m", "conf")
        root = report("scoped", cmd_of("fast-1", "fast", "true"),
                      cmd_of("scoped-1", "scoped", "echo s"), paths=["tools/site/page.ts"])
        nested = report("full", cmd_of("n-fast", "fast", "true"), cmd_of("n-full", "full", "echo f"),
                        diagnostics=["the analyzer cannot see this root: falling back to the full tier"])
        plan = scoped_plan.plan_light(clone.dir, "repos/app", "b0",
                                      ask_of({"app": root, "site": nested}, []))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual([(c.name, c.phase) for c in plan.commands],
                         [("fast-1", "fast"), ("scoped-1", "scoped"),
                          ("tools/site/n-fast", "fast"), ("tools/site/n-full", "full")])
        self.assertEqual(plan.nested_full,
                         ["the nested engine root tools/site fell back to the full tier:"+
                          " the analyzer cannot see this root: falling back to the full tier"])

    def test_plan_light_statuses(self) -> None:
        """An engine that answers nothing leaves the repo unplanned with
        its why; a report that plans no command is empty, its reason
        naming the stack and the conf."""
        plan = scoped_plan.plan_light(self.root, "repos/app", "b0", ask_of({}, []))
        self.assertEqual(plan.status, scoped_plan.PLAN_ENGINE_FAILED)
        self.assertEqual(plan.reason, "the engine exited 3: no report")
        rep = report("scoped")
        plan = scoped_plan.plan_light(self.root, "repos/app", "b0", ask_of({"app": rep}, []))
        self.assertEqual(plan.status, scoped_plan.PLAN_EMPTY)
        self.assertEqual(plan.reason, "the engine planned no command (stack \"none\", conf present)")

    def test_base_branch_of(self) -> None:
        """The branch the change is measured against: the capsule's fixed
        target branch when it carries one (read strictly — a branch
        missing on origin is an error, never a fall-back), else the
        origin's default branch."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        self.assertEqual(scoped_plan.base_branch_of(fx.capsule, fx.repo("app")), ("main", ""))
        write(fx.capsule, {"runtime-state/target-branch.json": json.dumps({"target": "no-such-branch"})})
        branch, why = scoped_plan.base_branch_of(fx.capsule, fx.repo("app"))
        self.assertEqual(branch, "")
        self.assertIn("origin has no branch no-such-branch", why)
        write(fx.capsule, {"runtime-state/target-branch.json": json.dumps({"target": "main"})})
        self.assertEqual(scoped_plan.base_branch_of(fx.capsule, fx.repo("app")), ("main", ""))

    def test_verifys_light_plan_is_the_shared_plans_answer(self) -> None:
        """verification's light tier plans through the shared
        implementation: on one fixture, verify.py's recorded plan and
        scoped_plan.plan_light's answer with the same reports are the
        same selection."""
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"})
        from .verifyfix import fake_engine
        rep = report("scoped", cmd_of("fast-1", "fast", "true"), cmd_of("scoped-1", "scoped", "echo s"))
        eng = fake_engine(self.root, {"app": rep})
        r = run_verify(fx.capsule, eng)
        self.assertEqual(r.exit, 0, r.stderr)
        assert r.doc is not None
        base = r.doc["merge_outcomes"]["app"]["base"]
        ran = r.doc["repos"][0]["commands"]
        plan = scoped_plan.plan_light(fx.repo("app"), "repos/app", base,
                                      ask_of({"app": rep}, []))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual([(c.name, c.phase) for c in plan.commands],
                         [(c["name"], c["phase"]) for c in ran])


if __name__ == "__main__":
    unittest.main()
