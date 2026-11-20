# carries: workflows/_shared/scripts/fast_checks.py, workflows/_shared/scripts/verify.py, workflows/_shared/scripts/facts.py, workflows/_shared/scripts/pfstep/scoped_plan.py, workflows/_shared/scripts/pfstep/analysis.py
"""The build close's scoped selection (polyforge#164): the one light-tier
planning implementation shared by verification and the close
(SharedPlanTest), the close's own run and its parity with verification's
plan (SelectionParityTest, ExecutionTest) and the change analysis's
machine selection record (AnalysisRecordTest)."""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from typing import Any, Callable, Dict, List, Optional, Tuple

from pfstep import scoped_plan
from pfstep.engine import EngineReport, ReportError, decode_report

from .gitworld import HAVE_GIT, Repo, git, write
from .test_fast_checks import APP, FLAKY_GATE_TEST, GO_MOD, FastChecksCase
from .verifyfix import HAVE_BASH, HAVE_GO, Fixture, cmd_of, fake_engine, report, run_verify, write_files

FAST_CHECKS = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "fast_checks.py")
ENGINE = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "verify-scope")


def light_engine(root: str, default: Dict[str, Any], light: Dict[str, Any]) -> str:
    """A fake scope engine answering default without --light and light
    with it (the shape of #136/#143/#154: a forced-full path makes the
    default report full — the per-unit fast checks never see the pinned
    test — while the light report stays scoped and selects it), logging
    its arguments to args.log beside it."""
    d = tempfile.mkdtemp(prefix="light-engine-", dir=root)
    for name, rep in (("default.json", default), ("light.json", light)):
        with open(os.path.join(d, name), "w", encoding="utf-8") as f:
            json.dump(rep, f)
    script = """#!/bin/bash
dir=$(cd "$(dirname "$0")" && pwd)
echo "$(basename "$PWD") $*" >>"$dir/args.log"
for a in "$@"; do if [ "$a" = "--light" ]; then cat "$dir/light.json"; exit 0; fi; done
cat "$dir/default.json"
"""
    p = os.path.join(d, "verify-scope")
    with open(p, "w", encoding="utf-8") as f:
        f.write(script)
    os.chmod(p, 0o755)
    return p


def engine_args_of(engine: str) -> List[str]:
    """The lines the fake engine logged ("<dir> <args>")."""
    try:
        with open(os.path.join(os.path.dirname(engine), "args.log"), encoding="utf-8") as f:
            return f.read().splitlines()
    except OSError:
        return []


def run_close(capsule: str, repo: str, engine: str, out: str, *extra: str) -> Tuple[int, Dict[str, Any]]:
    """fast_checks.py --close-scoped on the capsule's repo, as a skill runs
    it; its exit and its result file."""
    argv = [sys.executable, FAST_CHECKS, "--capsule", capsule, "--repo", repo, "--close-scoped",
            "--engine", engine, "--deadline", "10m", "--out", out, *extra]
    c = subprocess.run(argv, capture_output=True, text=True)
    if not os.path.exists(out):
        raise AssertionError("fast_checks.py wrote no result file (exit %d): %s" % (c.returncode, (c.stderr or c.stdout)[-500:]))
    with open(out, encoding="utf-8") as f:
        return c.returncode, json.load(f)


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
        repo = os.path.join(self.root, "app")
        os.makedirs(repo, exist_ok=True)
        asked: List[Tuple[str, str, str]] = []
        rep = report("scoped", cmd_of("fast-1", "fast", "true"),
                     cmd_of("fast-2", "fast", "true"),
                     cmd_of("scoped-1", "scoped", "echo s"),
                     cmd_of("full-1", "full", "echo never"))
        plan = scoped_plan.plan_light(repo, "repos/app", "b0", ask_of({"app": rep}, asked))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual(plan.mode, "light")
        self.assertEqual([c.name for c in plan.commands], ["fast-1", "fast-2", "scoped-1"])
        self.assertEqual([c.phase for c in plan.commands], ["fast", "fast", "scoped"])
        self.assertEqual(asked, [(repo, "repos/app", "b0")])

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
        repo = os.path.join(self.root, "app")
        os.makedirs(repo, exist_ok=True)
        asked: List[Tuple[str, str, str]] = []
        rep = report("full", cmd_of("fast-1", "fast", "true"), cmd_of("full-1", "full", "echo f"),
                     diagnostics=["go.mod changed: falling back to the full tier"])
        plan = scoped_plan.plan_light(repo, "repos/app", "b0", ask_of({"app": rep}, asked))
        self.assertEqual(plan.status, scoped_plan.PLAN_PLANNED, plan.reason)
        self.assertEqual(plan.mode, "full")
        self.assertEqual([c.name for c in plan.commands], ["fast-1", "full-1"])
        decoded = decode_report(json.dumps(rep).encode())
        self.assertEqual(scoped_plan.fallback_reasons(decoded), ["go.mod changed: falling back to the full tier"])
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
        repo = os.path.join(self.root, "app")
        os.makedirs(repo, exist_ok=True)
        plan = scoped_plan.plan_light(repo, "repos/app", "b0", ask_of({}, []))
        self.assertEqual(plan.status, scoped_plan.PLAN_ENGINE_FAILED)
        self.assertEqual(plan.reason, "the engine exited 3: no report")
        rep = report("scoped")
        plan = scoped_plan.plan_light(repo, "repos/app", "b0", ask_of({"app": rep}, []))
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


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class SelectionParityTest(unittest.TestCase):
    """AC1: the same fixture, both entries — verification's light plan and
    the build close's --close-scoped selection — answer the same set of
    existing tests, planned by the one shared implementation: the same
    commands (name, phase, cmd), the same mode, the same engine request,
    and a full answer leaves the close incomplete with the same reasons
    verification names."""

    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def selection_of_close(self, res: Dict[str, Any]) -> List[Tuple[str, str, str]]:
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green", sel.get("reason", ""))
        return [(c["name"], c["phase"], c["cmd"]) for c in sel["commands"]]

    def test_the_same_fixture_selects_the_same_commands(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"})
        eng = fake_engine(self.root, {"app": report(
            "scoped", cmd_of("fast-1", "fast", "true"),
            cmd_of("scoped-1", "scoped", "echo s"), cmd_of("full-1", "full", "echo never"))})
        r = run_verify(fx.capsule, eng)
        self.assertEqual(r.exit, 0, r.stderr)
        assert r.doc is not None
        base = r.doc["merge_outcomes"]["app"]["base"]
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        # the same commands, the same mode, the same base — and the engine
        # was asked exactly alike
        self.assertEqual(self.selection_of_close(res),
                         [(c["name"], c["phase"], c["command"]) for c in r.doc["repos"][0]["commands"]])
        self.assertEqual(res["details"]["selected"]["app"]["mode"], "light")
        self.assertEqual(res["details"]["selected"]["app"]["base"], base)
        args = engine_args_of(eng)
        self.assertEqual(args[-1], "app --json --light --base " + base, args)
        self.assertIn("app --json --light --base " + base, "\n".join(args[:-1]))

    def test_a_nested_root_selects_the_same_on_both_sides(self) -> None:
        files = {".polyforge/verify-scope.conf": "stack=go\n",
                 "web/.polyforge/verify-scope.conf": "stack=npm\n",
                 "web/lib/a.ts": "export const a = 1;\n"}
        fx = Fixture(self.root, {"app": files})
        fx.commit_in_repo("app", {"web/lib/a.ts": "export const a = 2;\n"})
        eng = fake_engine(self.root, {
            "app": report("scoped", cmd_of("fast-1", "fast", "true"), paths=["web/lib/a.ts"]),
            "web": report("scoped", cmd_of("n-fast", "fast", "true"),
                          cmd_of("n-scoped", "scoped", "echo n"), base_setup=["npm ci"]),
        })
        r = run_verify(fx.capsule, eng)
        self.assertEqual(r.exit, 0, r.stderr)
        assert r.doc is not None
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(self.selection_of_close(res),
                         [(c["name"], c["phase"], c["command"]) for c in r.doc["repos"][0]["commands"]])
        sel = res["details"]["selected"]["app"]
        self.assertEqual([c["name"] for c in sel["commands"]],
                         ["fast-1", "web/n-fast", "web/n-scoped"])
        self.assertIn("cd web && ", sel["commands"][2]["cmd"])

    def test_a_full_answer_is_incomplete_with_the_same_reasons(self) -> None:
        fx = Fixture(self.root, {"app": {"go.mod": "module a\n\ngo 1.21\n"}})
        fx.commit_in_repo("app", {"go.mod": "module a\n\ngo 1.22\n"})
        eng = fake_engine(self.root, {"app": report(
            "full", cmd_of("fast-1", "fast", "true"), cmd_of("full-1", "full", "echo f"),
            diagnostics=["go.mod: a stack config file changed, falling back to the full tier"])})
        r = run_verify(fx.capsule, eng)
        assert r.doc is not None
        self.assertEqual(r.doc["tier"]["effective"], "full", r.doc["tier"])
        reasons = r.doc["tier"].get("reasons") or [""]
        self.assertTrue(reasons[0].startswith("repos/app: "), r.doc["tier"])
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 1, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "incomplete")
        self.assertEqual(sel["reason"], "go.mod: a stack config file changed, falling back to the full tier")
        self.assertEqual(sel["commands"], [], "a full answer runs nothing: the full bar is verification's")
        self.assertIn("falling back to the full tier", res["summary"])

    @unittest.skipUnless(HAVE_GO, "go is required for the real engine's parity")
    def test_the_real_engine_selects_the_same_commands(self) -> None:
        files = {"go.mod": "module a\n\ngo 1.21\n",
                 ".polyforge/verify-scope.conf": "stack=go\nfast_gate=echo gate-ok\n",
                 "app/app.go": "package app\n\n// Answer is the answer.\nfunc Answer() int { return 42 }\n",
                 "app/app_test.go": "package app\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(\"not 42\")\n\t}\n}\n"}
        fx = Fixture(self.root, {"app": files})
        fx.commit_in_repo("app", {"app/app.go": "package app\n\n// Answer is the answer. (changed)\nfunc Answer() int { return 42 }\n"})
        r = run_verify(fx.capsule, ENGINE)
        self.assertEqual(r.exit, 0, r.stderr)
        assert r.doc is not None
        code, res = run_close(fx.capsule, "app", ENGINE, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(self.selection_of_close(res),
                         [(c["name"], c["phase"], c["command"]) for c in r.doc["repos"][0]["commands"]])


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class ExecutionTest(FastChecksCase):
    """AC2: the selected existing tests actually run at the close — the
    #136/#143/#154 shape (a change touches text an existing test pins: the
    default report is full, the per-unit fast checks stay green, the light
    selection runs the pinned test and it is red; the fix turns it green),
    a full fallback is incomplete with its reason kept, a selected command
    that cannot run is never a skip-as-green, a timeout is red, engine
    failure is not judged, and a --reuse file of another tree reuses
    nothing."""

    def setUp(self) -> None:
        super().setUp()
        self.n = 0
        if not (shutil.which("python3") and os.path.exists(ENGINE)):
            self.skipTest("python3 or the engine missing")

    def pin_repo(self) -> Tuple[str, str]:
        """A repo whose change breaks text an existing test pins, with the
        light-aware engine that makes the default report full (fast checks
        green) and the light report scoped (the pinned test selected)."""
        clone = self.task_repo("app", {"pin/note.txt": "the answer is 42\n"},
                               {"pin/note.txt": "the answer is 43\n"})
        eng = light_engine(
            self.tmp,
            report("full", cmd_of("gate-1", "fast", "echo gate-ok")),
            report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"),
                   cmd_of("pin", "scoped", "test \"$(cat pin/note.txt)\" = \"the answer is 42\"")))
        return clone, eng

    def run_mode(self, repo: str, engine: str, *extra: str) -> Tuple[int, Dict[str, Any], str]:
        """fast_checks.py --repo (the per-unit bar) on the capsule's repo;
        its exit, its result file and the file's path."""
        self.n += 1
        out = os.path.join(self.capsule, "fast-%d.json" % self.n)
        argv = [sys.executable, FAST_CHECKS, "--capsule", self.capsule, "--repo", repo,
                "--engine", engine, "--deadline", "10m", "--out", out, *extra]
        c = subprocess.run(argv, capture_output=True, text=True)
        if not os.path.exists(out):
            raise AssertionError("fast_checks.py wrote no result file (exit %d): %s" % (c.returncode, (c.stderr or c.stdout)[-500:]))
        with open(out, encoding="utf-8") as f:
            return c.returncode, json.load(f), out

    def test_fast_checks_green_close_scoped_red_then_green(self) -> None:
        clone, eng = self.pin_repo()
        # the per-unit bar: the default report is full, its fast gate runs,
        # the pinned test is never selected — green
        code, _res, _out = self.run_mode("app", eng)
        self.assertEqual(code, 0, "the per-unit fast check must stay green")
        # the close: the light selection runs the pinned test — red
        code, res = run_close(self.capsule, "app", eng, os.path.join(self.capsule, "close.json"))
        self.assertEqual(code, 1, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "red")
        pin = [c for c in sel["commands"] if c["name"] == "pin"][0]
        self.assertNotEqual(pin["exit_code"], 0)
        # the fix turns it green, and the close reuses the fast checks'
        # green run of the same tree instead of re-running it
        write(clone, {"pin/note.txt": "the answer is 42\n"})
        git(clone, "add", "-A")
        git(clone, "commit", "-q", "-m", "fix the pinned text")
        code, _res, fast_out = self.run_mode("app", eng)
        self.assertEqual(code, 0)
        code, res = run_close(self.capsule, "app", eng, os.path.join(self.capsule, "close.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green")
        gate = [c for c in sel["commands"] if c["name"] == "gate-1"][0]
        self.assertIn("reused", gate, "a green same-tree run is not re-run")
        self.assertNotIn("exit_code", gate)
        pin = [c for c in sel["commands"] if c["name"] == "pin"][0]
        self.assertEqual(pin["exit_code"], 0)

    def test_full_fallback_is_incomplete(self) -> None:
        self.task_repo("app", {"a.txt": "base\n"}, {"go.mod": "module a\n\ngo 1.21\n"})
        eng = light_engine(
            self.tmp,
            report("full", cmd_of("gate-1", "fast", "echo gate-ok")),
            report("full", cmd_of("gate-1", "fast", "echo gate-ok"),
                   diagnostics=["go.sum: a lockfile changed, falling back to the full tier"]))
        # the per-unit bar behaves exactly as before on a forced-full path
        code, _res, _out = self.run_mode("app", eng)
        self.assertEqual(code, 0)
        code, res = run_close(self.capsule, "app", eng, os.path.join(self.capsule, "close.json"))
        self.assertEqual(code, 1, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "incomplete")
        self.assertIn("a lockfile changed, falling back to the full tier", sel["reason"])
        self.assertEqual(sel["commands"], [], "an incomplete selection runs nothing")

    def test_an_unmet_precondition_is_incomplete(self) -> None:
        self.task_repo("app", {"a.txt": "base\n"}, {"c.txt": "mine\n"})
        eng = light_engine(
            self.tmp,
            report("full", cmd_of("gate-1", "fast", "echo gate-ok")),
            report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"),
                   cmd_of("pin", "scoped", "echo pinned", requires_env=["PF_CLOSE_NO_SUCH_VAR"])))
        code, res = run_close(self.capsule, "app", eng, os.path.join(self.capsule, "close.json"))
        self.assertEqual(code, 1, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "incomplete")
        self.assertIn("cannot run", sel["reason"])
        self.assertIn("PF_CLOSE_NO_SUCH_VAR", sel["reason"])

    def test_a_timeout_is_red(self) -> None:
        self.task_repo("app", {"a.txt": "base\n"}, {"c.txt": "mine\n"})
        eng = light_engine(
            self.tmp,
            report("full", cmd_of("gate-1", "fast", "echo gate-ok")),
            report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"),
                   cmd_of("slow", "scoped", "sleep 5", timeout="2s")))
        code, res = run_close(self.capsule, "app", eng, os.path.join(self.capsule, "close.json"))
        self.assertEqual(code, 1, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "red")
        slow = [c for c in sel["commands"] if c["name"] == "slow"][0]
        self.assertTrue(slow["timed_out"])

    @unittest.skipUnless(HAVE_GO, "go is required for the real engine's flaky re-run")
    def test_a_flaky_selected_command_passes_on_its_one_rerun(self) -> None:
        """A red selected command gets the per-unit bar's own one targeted
        re-run and whole-command confirmation (the real engine's Go
        selection, the same fixture the fast-checks flaky tests use): a
        command red on its first run and green on its re-run turns the
        close green, its flaky unit and first-red log named in the result."""
        clone = self.task_repo("app", {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf": "stack=go\nfast_gate=echo fast-one\nfull_command=echo full-ran > full.txt\n",
            "app/app.go": APP,
            "app/app_test.go": FLAKY_GATE_TEST,
        }, {"app/app.go": APP.replace("// Answer is the answer.\n", "// Answer is the answer. (changed)\n")})
        code, res = run_close(self.capsule, "app", ENGINE, os.path.join(self.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green")
        scoped = [c for c in sel["commands"] if c["phase"] == "scoped"][0]
        self.assertNotEqual(scoped["exit_code"], 0, "the first run failed on its assertion")
        self.assertEqual([u["name"] for u in scoped["units"]], ["TestFlakyGate"])
        self.assertEqual(scoped["units"][0]["status"], "flaky")
        self.assertTrue(scoped["units"][0]["rerun_log"], "the passing re-run's log is recorded")
        self.assertTrue(scoped["confirmed"], "the whole command was re-run once and exited 0")
        self.assertTrue(os.path.exists(os.path.join(clone, "app", "flaky-marker")))
        self.assertIn("flaky on their one re-run", res["summary"])

    def test_engine_failure_is_not_judged(self) -> None:
        self.task_repo("app", {"a.txt": "base\n"}, {"c.txt": "mine\n"})
        eng = fake_engine(self.tmp, {})  # no report: exits 3
        argv = [sys.executable, FAST_CHECKS, "--capsule", self.capsule, "--repo", "app",
                "--close-scoped", "--engine", eng, "--deadline", "10m",
                "--out", os.path.join(self.capsule, "close.json")]
        c = subprocess.run(argv, capture_output=True, text=True)
        self.assertEqual(c.returncode, 2)
        self.assertIn("no report for app", c.stdout + c.stderr)

    def test_a_stale_reuse_file_reuses_nothing(self) -> None:
        clone, eng = self.pin_repo()
        code, _res, fast_out = self.run_mode("app", eng)
        self.assertEqual(code, 0)
        write(clone, {"pin/note.txt": "the answer is 42\n"})
        git(clone, "add", "-A")
        git(clone, "commit", "-q", "-m", "fix the pinned text")
        code, res = run_close(self.capsule, "app", eng, os.path.join(self.capsule, "close.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green")
        self.assertEqual([c for c in sel["commands"] if "reused" in c], [],
                         "a result file of another tree is not reuse evidence")



@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class CloseReuseKeyTest(unittest.TestCase):
    """AC1's close half (polyforge#182, Q6): the close's reuse is keyed
    exactly like the --repo run's — the command's root, its text as its
    own report gave it, its root's subtree tree hash of the working tree,
    its declared prerequisites — so a commit outside the nested root
    keeps that root's green runs reusable while the repository root's key
    counts the whole tree and re-runs; a green-through-flaky confirmation
    is never reuse evidence (Q4), an entry without its key fields (an
    older run's file) re-runs, and the per-unit file's own reused entries
    carry the green evidence the close accepts. Expected values come from
    the fixtures' own commits and the hand-written reuse files, never the
    implementation."""

    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def run_per_unit(self, capsule: str, engine: str, out: str) -> int:
        """fast_checks.py --repo (the per-unit bar) writing out."""
        argv = [sys.executable, FAST_CHECKS, "--capsule", capsule, "--repo", "app",
                "--engine", engine, "--deadline", "10m", "--out", out]
        c = subprocess.run(argv, capture_output=True, text=True)
        self.assertEqual(c.returncode, 0, c.stdout + c.stderr)
        return c.returncode

    def test_a_commit_outside_the_nested_root_keeps_its_reuse(self) -> None:
        files = {
            ".polyforge/verify-scope.conf": "stack=go\n",
            "web/.polyforge/verify-scope.conf": "stack=npm\n",
            "web/lib/a.ts": "export const a = 1;\n",
            "notes/root.txt": "base\n",
        }
        fx = Fixture(self.root, {"app": files})
        fx.commit_in_repo("app", {"web/lib/a.ts": "export const a = 2;\n"})
        eng = fake_engine(self.root, {
            "app": report("scoped", cmd_of("fast-1", "fast", "echo gate-ok"), paths=["web/lib/a.ts"]),
            "web": report("scoped", cmd_of("n-fast", "fast", "echo nested-ok"),
                          cmd_of("n-scoped", "scoped", "echo nested-pinned")),
        })
        fast_out = os.path.join(fx.capsule, "fast.json")
        self.run_per_unit(fx.capsule, eng, fast_out)
        # a commit outside the nested root: the repository's head moves,
        # the nested subtree does not — the key, not the head, decides
        fx.commit_in_repo("app", {"notes/root.txt": "base\n# a root-only edit\n"})
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green")
        by_name = {cmd["name"]: cmd for cmd in sel["commands"]}
        self.assertIn("reused", by_name["web/n-fast"],
                      "a commit outside the nested root keeps its green runs")
        self.assertIn("reused", by_name["web/n-scoped"],
                      "the nested root's scoped run is kept too")
        self.assertNotIn("reused", by_name["fast-1"],
                         "the repository root's key counts the whole tree and re-runs")

    def test_a_confirmed_flaky_green_is_not_reuse_evidence(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"})
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"))})
        # a hand-written reuse file whose only entry is green through the
        # flaky phase (red first run, confirmed whole): the owner's Q4 —
        # only a plain green run is cache
        from pfstep import git as pfgit
        head = git(fx.repo("app"), "rev-parse", "HEAD")
        tree = pfgit.worktree_tree(fx.repo("app"))
        entry = {"name": "gate-1", "phase": "fast", "root": "repos/app", "cmd": "echo gate-ok",
                 "exit_code": 1, "log": "logs/1/app/2/command.log", "confirmed": "echo gate-ok",
                 "tree": tree, "prereq_key": '{"cmd": [], "env": []}'}
        fast_out = os.path.join(fx.capsule, "fast.json")
        write_files(fx.capsule, {"fast.json": json.dumps(
            {"heads": {"app": head}, "details": {"commands": [entry]}})})
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green")
        gate = [cmd for cmd in sel["commands"] if cmd["name"] == "gate-1"][0]
        self.assertNotIn("reused", gate, "a green-through-flaky confirmation is never reuse evidence")
        self.assertEqual(gate["exit_code"], 0, "the command ran again")

    def test_an_entry_without_its_key_fields_re_runs(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"})
        eng = fake_engine(self.root, {"app": report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"))})
        # an older run's file: green, the same root and command, but no
        # key fields — no cache, never an error
        head = git(fx.repo("app"), "rev-parse", "HEAD")
        entry = {"name": "gate-1", "phase": "fast", "root": "repos/app", "cmd": "echo gate-ok",
                 "exit_code": 0, "log": "logs/1/app/2/command.log"}
        fast_out = os.path.join(fx.capsule, "fast.json")
        write_files(fx.capsule, {"fast.json": json.dumps(
            {"heads": {"app": head}, "details": {"commands": [entry]}})})
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        gate = [cmd for cmd in sel["commands"] if cmd["name"] == "gate-1"][0]
        self.assertNotIn("reused", gate, "an entry without its key fields is no cache")
        self.assertEqual(gate["exit_code"], 0, "the command ran again")

    def test_close_accepts_the_per_unit_files_own_reused_entries(self) -> None:
        fx = Fixture(self.root, {"app": {"a.txt": "base\n"}})
        fx.commit_in_repo("app", {"c.txt": "mine\n"})
        eng = fake_engine(self.root, {"app": report(
            "scoped", cmd_of("gate-1", "fast", "echo gate-ok"),
            cmd_of("pin", "scoped", "echo pinned"))})
        fast_out = os.path.join(fx.capsule, "fast.json")
        self.run_per_unit(fx.capsule, eng, fast_out)
        self.run_per_unit(fx.capsule, eng, fast_out)  # the second run marks its green runs reused
        with open(fast_out, encoding="utf-8") as f:
            fast = json.load(f)
        self.assertTrue([e for e in fast["details"]["commands"] if e.get("reused")],
                        "the second per-unit run reused its green commands")
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        by_name = {cmd["name"]: cmd for cmd in sel["commands"]}
        self.assertIn("reused", by_name["gate-1"], "a reused entry carries green evidence")
        self.assertIn("reused", by_name["pin"], "a reused scoped entry carries green evidence")


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class AnalysisRecordTest(unittest.TestCase):
    """AC3's machine part: the close's selection record lands in the change
    analysis whole (a close-scoped run's details.selected, merged verbatim,
    passes the close check), the engine's items are never subject to the
    supplement cap, and the record shows its own selection sources — on
    project-neutral fixtures (a repo named app, a nested pin file)."""

    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)
        self.out = os.path.join(self.root, "analysis-out.json")

    def analysis_file(self, capsule: str, doc: Dict[str, Any]) -> str:
        path = os.path.join(capsule, "artifacts", "build", "change-analysis.json")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as f:
            json.dump(doc, f, ensure_ascii=False, indent=2)
            f.write("\n")
        return path

    def run_facts(self, capsule: str, path: str) -> Tuple[int, Dict[str, Any]]:
        import io
        from contextlib import redirect_stderr, redirect_stdout
        import facts
        from .verifyfix import step_env
        so, se = io.StringIO(), io.StringIO()
        with redirect_stdout(so), redirect_stderr(se):
            code = facts.main(["analysis", "--capsule", capsule, "--analysis", path,
                               "--unit-timeout", "1m", "--json", "--out", self.out], step_env())
        with open(self.out, encoding="utf-8") as f:
            return code, json.load(f)

    def closed_repo(self) -> Tuple[Fixture, str]:
        """A repo whose close-scoped selection is green: the change touches
        the pin file's neighbourhood, the pinned test still passes, and the
        light selection runs it."""
        fx = Fixture(self.root, {"app": {"pin/note.txt": "the answer is 42\n"}})
        fx.commit_in_repo("app", {"pin/note.txt": "the answer is 42\n", "lib/x.py": "x = 2\n"})
        eng = light_engine(
            self.root,
            report("full", cmd_of("gate-1", "fast", "echo gate-ok")),
            report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"),
                   cmd_of("pin", "scoped", "test \"$(cat pin/note.txt)\" = \"the answer is 42\"")))
        return fx, eng

    def test_the_closes_record_lands_in_the_analysis_whole(self) -> None:
        fx, eng = self.closed_repo()
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green")
        # the record states its own identity and every command its source
        self.assertTrue(sel["engine"], sel)
        self.assertEqual(sel["default_branch"], "main")
        for c in sel["commands"]:
            self.assertTrue(c["source"], c)
        # merged verbatim, the close check passes on it
        path = self.analysis_file(fx.capsule, {"changed": ["the pin file"], "uncovered": {"app": []},
                                               "tests": [], "selected": {"app": sel}})
        code, fres = self.run_facts(fx.capsule, path)
        self.assertEqual(code, 0, fres.get("summary"))
        entries = (fres.get("details") or {}).get("selected") or []
        self.assertEqual([(e["repo"], e["status"]) for e in entries], [("app", "pass")])

    def test_engine_items_survive_the_supplement_cap(self) -> None:
        """The cap cuts the tests[] tail only: the engine's selected items
        are the machine's own record, never a supplement, so no cap drops
        one (the ticket's AC3)."""
        fx, eng = self.closed_repo()
        code, res = run_close(fx.capsule, "app", eng, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        tests = [{"id": "a1", "repo": "app", "file": "pin/note.txt", "cwd": ".",
                  "argv": ["bash", "-c", "echo ANALYSIS-PASS"], "marker": "^ANALYSIS-PASS$",
                  "why": "pins the pinned text"},
                 {"id": "a2", "repo": "app", "file": "pin/note.txt", "cwd": ".",
                  "argv": ["bash", "-c", "echo ANALYSIS-PASS"], "marker": "^ANALYSIS-PASS$",
                  "why": "pins the pinned text"}]
        path = self.analysis_file(fx.capsule, {"supplement_limit": 1, "changed": [], "uncovered": {"app": []},
                                               "tests": tests, "selected": {"app": sel}})
        code, fres = self.run_facts(fx.capsule, path)
        self.assertEqual(code, 0, fres.get("summary"))
        d = fres.get("details") or {}
        self.assertEqual([c["id"] for c in d.get("cut") or []], ["a2"], "the cap cuts the tests tail only")
        self.assertEqual([e["status"] for e in d.get("selected") or []], ["pass"])


if __name__ == "__main__":
    unittest.main()
