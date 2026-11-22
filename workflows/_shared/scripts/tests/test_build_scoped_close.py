# carries: workflows/_shared/scripts/fast_checks.py, workflows/_shared/scripts/verify.py, workflows/_shared/scripts/facts.py, workflows/_shared/scripts/pfstep/scoped_plan.py, workflows/_shared/scripts/pfstep/analysis.py
"""The build close's scoped selection (polyforge#164): the one light-tier
planning implementation shared by verification and the close
(SharedPlanTest), the close's own run and its parity with verification's
plan (SelectionParityTest, ExecutionTest) and the change analysis's
machine selection record (AnalysisRecordTest); the close's isolation
(polyforge#222, CloseContainerTest): a repo whose HEAD declares the
verification image runs its whole close inside that image, every other
shape keeps the host close."""

import hashlib
import glob
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from typing import Any, Callable, Dict, List, Optional, Tuple

import fast_checks
from pfstep import container, scoped_plan
from pfstep.engine import EngineReport, ReportError, decode_report

from .capsuleutil import TEST_DOCKERFILE
from .fakebox import FakeRuntime
from .gitworld import HAVE_GIT, Repo, git, write
from .test_fast_checks import APP, FLAKY_GATE_TEST, GO_MOD, FastChecksCase
from .verifyfix import HAVE_BASH, HAVE_GO, Fixture, cmd_of, fake_engine, report, run_verify, step_env, write_files

FAST_CHECKS = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "fast_checks.py")
ENGINE = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "verify-scope")
DOCKERFILE = container.DOCKERFILE_VERIFY
# another generation of the declared image: different bytes, another tag
DOCKERFILE2 = "FROM scratch-test-image\nRUN two\n"


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


def run_close(capsule: str, repo: str, engine: str, out: str, *extra: str,
              env: Optional[Dict[str, str]] = None) -> Tuple[int, Dict[str, Any]]:
    """fast_checks.py --close-scoped on the capsule's repo, as a skill runs
    it; its exit and its result file. env passes a step environment through
    (a fake bin on the PATH — the baseline worktree's re-runs need the same
    fake runner the bar ran with)."""
    argv = [sys.executable, FAST_CHECKS, "--capsule", capsule, "--repo", repo, "--close-scoped",
            "--engine", engine, "--deadline", "10m", "--out", out, *extra]
    c = subprocess.run(argv, capture_output=True, text=True, env=env)
    if not os.path.exists(out):
        raise AssertionError("fast_checks.py wrote no result file (exit %d): %s" % (c.returncode, (c.stderr or c.stdout)[-500:]))
    with open(out, encoding="utf-8") as f:
        return c.returncode, json.load(f)


def box_close(capsule: str, repo: str, engine: str, rt: FakeRuntime, out: str, *extra: str,
              env: Optional[Dict[str, str]] = None) -> Tuple[int, Dict[str, Any]]:
    """fast_checks.py --close-scoped in-process on the fake container
    engine rt (a test's injection: the host's docker is never probed);
    its exit and its result file. env passes a step environment through
    (a private bin on the host PATH the close's host side must see)."""
    argv = ["--capsule", capsule, "--repo", repo, "--close-scoped",
            "--engine", engine, "--deadline", "10m", "--out", out, *extra]
    so, se = io.StringIO(), io.StringIO()
    with redirect_stdout(so), redirect_stderr(se):
        code = fast_checks.main(argv, step_env(env), runtime=rt)
    if not os.path.exists(out):
        raise AssertionError("fast_checks.py wrote no result file (exit %d): %s" % (code, se.getvalue()[-500:]))
    with open(out, encoding="utf-8") as f:
        return code, json.load(f)


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
class CloseContainerTest(unittest.TestCase):
    """The close's isolation (polyforge#222): a repo whose HEAD declares
    .polyforge/Dockerfile.verify runs its whole close-scoped selection
    inside the verification image — one container for the engine asks and
    the selected commands, the image built (or reused) by the same
    machinery verification starts its boxes with — while a repo without
    the declaration keeps the host close exactly as before (AC2). Every
    run here goes through tests/fakebox.py's FakeRuntime, which really
    executes the commands under env -i with the image's own environment,
    never the host's; expected tags and volumes derive from the committed
    Dockerfile's bytes and the clone's path, never from the
    implementation's own tagging."""

    # the image's own PATH: the tools a bash command needs, never the
    # host's private test tools (they live on the host PATH alone)
    IMAGE_PATH = "/usr/bin:/bin"

    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def fake_box(self, sub: str = "box") -> FakeRuntime:
        """A fake container engine whose image PATH is its own, never the
        host's (a private tool of a test lives only on the host PATH)."""
        d = os.path.join(self.root, sub)
        os.makedirs(d, exist_ok=True)
        fb = FakeRuntime(d)
        fb.image_environ = ["PATH=" + self.IMAGE_PATH, "IMAGE_ONLY=from-image", "HOME=/root"]
        return fb

    def declared_repo(self, sub: str, dockerfile: str = TEST_DOCKERFILE,
                      cmds: Optional[List[Dict[str, Any]]] = None) -> Tuple[Fixture, str]:
        """A capsule whose repo app declares the verification image at HEAD
        (the Dockerfile committed with the change, never only in the
        working tree) and a fake engine answering the light report of the
        given commands."""
        root = os.path.join(self.root, sub)
        os.makedirs(root)
        fx = Fixture(root, {"app": {"pin/note.txt": "the answer is 42\n"}})
        fx.commit_in_repo("app", {DOCKERFILE: dockerfile, "pin/note.txt": "the answer is 42\n"},
                          "declare the verification image")
        eng = fake_engine(root, {"app": report("scoped", *(cmds if cmds is not None else [
            cmd_of("gate-1", "fast", "echo gate-ok"),
            cmd_of("pin", "scoped", 'echo "boxed IMAGE_ONLY=$IMAGE_ONLY"')]))})
        return fx, eng

    def test_declared_image_runs_the_whole_close_in_its_container(self) -> None:
        """The declared image carries the whole close: the engine's light
        ask and every selected command run in the one container, the
        commands see the image's environment (never the host's), the
        result records the isolation and the container's own facts, and
        the container is removed at the end."""
        fx, eng = self.declared_repo("fx")
        fb = self.fake_box()
        out = os.path.join(fx.capsule, "close.json")
        code, res = box_close(fx.capsule, "app", eng, fb, out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green", sel.get("reason", ""))
        self.assertEqual(res["details"].get("isolation"), "container",
                         "a declared image selects the container isolation")
        # the container's facts: the image of the committed Dockerfile's
        # bytes — the tag derived here from the bytes, never from the
        # implementation's own tagging — built by this run
        tag = "pf-verify-app:" + hashlib.sha256(TEST_DOCKERFILE.encode()).digest()[:16].hex()
        cont = res["details"]["container"]
        self.assertEqual(cont["image"], tag)
        self.assertTrue(cont["built"])
        self.assertEqual(len(fb.starts), 1, fb.starts)
        # the whole close ran in the one container: the light ask and both
        # commands, nothing on the host
        self.assertTrue(fb.command_execs("--light"), "the light ask ran in the container")
        for needle in ("gate-ok", "IMAGE_ONLY"):
            self.assertTrue(fb.command_execs(needle),
                            "the command mentioning %s ran in the container" % needle)
        self.assertEqual(sorted({e[0] for e in fb.execs}), ["cid-1"])
        # the selected command saw the image's own environment: IMAGE_ONLY
        # carries the image's value, which the host environment does not
        pin = [c for c in sel["commands"] if c["name"] == "pin"][0]
        self.assertEqual(pin["exit_code"], 0)
        with open(os.path.join(fx.capsule, pin["log"]), encoding="utf-8") as f:
            self.assertIn("IMAGE_ONLY=from-image", f.read())
        # the container ends with the run
        self.assertEqual(fb.removes, ["cid-1"])

    def host_fast(self, capsule: str, repo: str, engine: str, out: str,
                  env: Optional[Dict[str, str]] = None) -> Tuple[int, Dict[str, Any]]:
        """fast_checks.py --repo (the per-unit bar) in-process — the mode
        that never starts a container, whatever the repo declares; its
        exit and its result file."""
        argv = ["--capsule", capsule, "--repo", repo, "--engine", engine,
                "--deadline", "10m", "--out", out]
        so, se = io.StringIO(), io.StringIO()
        with redirect_stdout(so), redirect_stderr(se):
            code = fast_checks.main(argv, step_env(env))
        if not os.path.exists(out):
            raise AssertionError("fast_checks.py wrote no result file (exit %d): %s" % (code, se.getvalue()[-500:]))
        with open(out, encoding="utf-8") as f:
            return code, json.load(f)

    def test_no_dockerfile_keeps_host_behavior(self) -> None:
        """AC2: a repo without the declared image keeps the host close —
        the container engine is never even probed (unavailable here), no
        build, start, exec or remove happens, the commands run on the
        host, and a same-tree green --reuse entry still hits; a
        working-tree-only Dockerfile is no declaration, and the per-unit
        --repo run stays on the host whatever the repo carries."""
        root = os.path.join(self.root, "host")
        os.makedirs(root)
        fx = Fixture(root, {"app": {"pin/note.txt": "the answer is 42\n"}})
        fx.commit_in_repo("app", {"pin/note.txt": "the answer is 42\n", "lib/x.py": "x = 2\n"}, "change")
        eng = light_engine(
            root,
            report("full", cmd_of("gate-1", "fast", "echo gate-ok")),
            report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"),
                   cmd_of("pin", "scoped", "echo pinned")))
        fb = self.fake_box()
        fb.unavailable = "the container engine is not here"
        # the per-unit bar on the host: green, its file the close's reuse
        fast_out = os.path.join(fx.capsule, "fast.json")
        code, _res = self.host_fast(fx.capsule, "app", eng, fast_out)
        self.assertEqual(code, 0)
        out = os.path.join(fx.capsule, "close.json")
        code, res = box_close(fx.capsule, "app", eng, fb, out, "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "green", sel.get("reason", ""))
        self.assertEqual(res["details"].get("isolation"), "host",
                         "no declaration keeps the host isolation, recorded")
        self.assertEqual((fb.builds, fb.starts, fb.execs, fb.removes), ([], [], [], []),
                         "the host close never touches the container engine")
        by_name = {c["name"]: c for c in sel["commands"]}
        self.assertIn("reused", by_name["gate-1"],
                      "a same-tree host green run is still reuse evidence on the host")
        self.assertNotIn("reused", by_name["pin"], "a scoped run the fast check never made is no reuse")
        self.assertEqual(by_name["pin"]["exit_code"], 0, "the selected command ran on the host")
        # a Dockerfile in the working tree alone is no declaration
        write(fx.repo("app"), {DOCKERFILE: TEST_DOCKERFILE})
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close2.json"),
                              "--reuse", fast_out)
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(res["details"].get("isolation"), "host",
                         "an uncommitted Dockerfile changes nothing")
        self.assertEqual((fb.builds, fb.starts, fb.execs, fb.removes), ([], [], [], []))
        # a declared repo's per-unit bar stays on the host: the close may
        # run in the container, the per-unit run never does
        fx2, eng2 = self.declared_repo("declared")
        fb2 = self.fake_box("box2")
        code, res = self.host_fast(fx2.capsule, "app", eng2, os.path.join(fx2.capsule, "fast.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertNotIn("isolation", res["details"],
                         "the per-unit run's record keeps its shape")
        self.assertEqual((fb2.builds, fb2.starts, fb2.execs), ([], [], []),
                         "the per-unit bar starts no container")

    def private_tool_env(self, sub: str = "bin") -> Dict[str, str]:
        """A host environment whose PATH carries a private tool the image's
        PATH (IMAGE_PATH) never holds: the host has it, the image does
        not."""
        d = os.path.join(self.root, sub)
        os.makedirs(d)
        with open(os.path.join(d, "close-proof-tool"), "w", encoding="utf-8") as f:
            f.write("#!/bin/bash\necho close-proof-tool-ok\n")
        os.chmod(os.path.join(d, "close-proof-tool"), 0o755)
        return {"PATH": d + os.pathsep + os.environ.get("PATH", "/usr/bin:/bin")}

    def test_host_tool_missing_in_image_is_red(self) -> None:
        """AC1: a command needing a tool the host has but the image does
        not is green on the host and red at the close — the boxed close
        consumes no --reuse green evidence (the command really executes
        in the container and fails there, its re-run too), an unusable
        --reuse file is still the invocation's error, and a red selected
        record is what the shared validator rejects — the process rule
        (ask the owner, never self-hand-off done) stands."""
        env = self.private_tool_env()
        fx, eng = self.declared_repo("fx", cmds=[
            cmd_of("gate-1", "fast", "echo gate-ok"),
            cmd_of("pin", "scoped", "close-proof-tool")])
        fb = self.fake_box()
        # the per-unit bar on the host: the tool is on the host's PATH —
        # green, and its result file is the close's --reuse evidence
        fast_out = os.path.join(fx.capsule, "fast.json")
        code, _res = self.host_fast(fx.capsule, "app", eng, fast_out, env=env)
        self.assertEqual(code, 0, "the host has the tool: the per-unit bar is green")
        out = os.path.join(fx.capsule, "close.json")
        code, res = box_close(fx.capsule, "app", eng, fb, out, "--reuse", fast_out, env=env)
        self.assertEqual(code, 1, res.get("summary"))
        self.assertEqual(res["verdict"], "fail")
        self.assertEqual(res["details"].get("isolation"), "container")
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "red", sel.get("reason", ""))
        self.assertEqual([c for c in sel["commands"] if "reused" in c], [],
                         "a boxed close consumes no host green evidence")
        pin = [c for c in sel["commands"] if c["name"] == "pin"][0]
        self.assertNotEqual(pin["exit_code"], 0, "the tool is missing in the image: red")
        # the command really ran in the container — never a simulated red —
        # and no re-run of it can land on the host's PATH: a whole-command
        # residual gets no re-run by the per-unit bar's own rules (never
        # re-runnable), and a located unit's re-run goes through the box
        # (u5's pre-existing scenario pins that path)
        tool_execs = fb.command_execs("close-proof-tool")
        self.assertGreaterEqual(len(tool_execs), 1,
                                "the command really executed in the container")
        for e in tool_execs:
            self.assertEqual(e[0], "cid-1")
        # an unusable --reuse file is the invocation's error, whatever the
        # isolation — never swallowed by the no-reuse rule
        bad = os.path.join(fx.capsule, "bad-reuse.json")
        write_files(fx.capsule, {"bad-reuse.json": "{not json"})
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close2.json"),
                              "--reuse", bad, env=env)
        self.assertEqual(code, 2)
        self.assertEqual(res["verdict"], "error")
        self.assertEqual(res["details"].get("commands"), [], "nothing ran, nothing claimed")

    def test_container_failures_are_errors_never_host_reds(self) -> None:
        """AC1's boundaries: an image that cannot be built or a container
        that cannot start is the run's error — exit 2, the chosen
        isolation recorded, nothing executed on the host as a stand-in;
        a declared requires_cmd missing in the image is the selection's
        incomplete (probed on the image's PATH, never the host's); a
        container lost mid-run is not judged — never a test red — and a
        removal that fails is recorded beside the run's own verdict."""
        # the image build fails: the run is not judged, the host runs
        # nothing in the container's place
        fx, eng = self.declared_repo("build-fail")
        fb = self.fake_box()
        fb.build_err = "the build broke"
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 2)
        self.assertEqual(res["verdict"], "error")
        self.assertEqual(res["details"].get("isolation"), "container",
                         "the failed start still records the chosen isolation")
        self.assertIn("could not be prepared", res["summary"])
        self.assertEqual(fb.execs, [], "nothing ran — never a host stand-in")
        # the container start fails: the same not-judged shape
        fx, eng = self.declared_repo("start-fail")
        fb = self.fake_box()
        fb.start_err = "the engine refused the start"
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 2)
        self.assertEqual(res["details"].get("isolation"), "container")
        self.assertIn("could not be prepared", res["summary"])
        self.assertEqual(fb.execs, [])
        # a declared requires_cmd missing in the IMAGE is incomplete —
        # the probe ran on the image's PATH (the host's copy of the tool
        # never satisfies it), an undeclared missing tool stays red (u3)
        env = self.private_tool_env()
        fx, eng = self.declared_repo("unmet", cmds=[
            cmd_of("gate-1", "fast", "echo gate-ok"),
            cmd_of("pin", "scoped", "close-proof-tool", requires_cmd=["close-proof-tool"])])
        fb = self.fake_box()
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close.json"), env=env)
        self.assertEqual(code, 1, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "incomplete", sel.get("reason", ""))
        self.assertIn("close-proof-tool", sel.get("reason", ""))
        pin = [c for c in sel["commands"] if c["name"] == "pin"][0]
        self.assertTrue(pin.get("skipped"), "the unmet declaration never ran")
        # a container lost mid-run is not judged — never a red, never a green
        fx, eng = self.declared_repo("lost")
        fb = self.fake_box()
        fb.reap_err = "the container did not answer"
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 2)
        self.assertEqual(res["verdict"], "error")
        self.assertEqual(res["details"].get("isolation"), "container")
        self.assertIn("became unusable", res["summary"])
        self.assertTrue(res["details"]["container"].get("lost"), "the loss is recorded")
        # a removal that fails is recorded beside the run's own verdict
        fx, eng = self.declared_repo("left")
        fb = self.fake_box()
        fb.remove_err = "the engine refused the removal"
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertTrue(res["details"]["container"].get("left"), "the left-behind container is recorded")

    def test_the_baseline_re_runs_in_the_same_container(self) -> None:
        """AC1's rerun path: the located failing unit's one targeted re-run,
        the baseline's base worktree — under the directory mounted into
        the container — its setup and re-runs, and the skip-form
        confirmation all execute in the same container; #212's
        pre-existing rules stand unchanged, and the base worktree is
        cleaned up afterwards."""
        from .test_verify_flows import FAKE_PYTEST, fake_bin
        root = os.path.join(self.root, "base")
        os.makedirs(root)
        env = fake_bin(root, "pytest", FAKE_PYTEST)
        bindir = env["PATH"].split(os.pathsep)[0]
        fx = Fixture(root, {"app": {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n"}})
        fx.commit_in_repo("app", {"tests/test_a.py": "def test_old(): # FAIL assert old breakage\n# the change\n",
                                  DOCKERFILE: TEST_DOCKERFILE}, "the change, with the image declared")
        eng = fake_engine(root, {"app": report("scoped", cmd_of("tests", "scoped", "pytest"))})
        fb = self.fake_box()
        # the image's PATH carries the fake pytest: the box, never the
        # host, can run every execution the close makes
        fb.image_environ = ["PATH=" + bindir + os.pathsep + self.IMAGE_PATH,
                            "IMAGE_ONLY=from-image", "HOME=/root"]
        out = os.path.join(fx.capsule, "close.json")
        code, res = box_close(fx.capsule, "app", eng, fb, out, env=env)
        self.assertEqual(code, 0, res.get("summary"))
        sel = res["details"]["selected"]["app"]
        self.assertEqual(sel["status"], "pre_existing", sel.get("reason", ""))
        tests_cmd = [c for c in sel["commands"] if c["name"] == "tests"][0]
        self.assertEqual(tests_cmd.get("confirmed"), "pytest --deselect tests/test_a.py::test_old",
                         "the pre-existing failure is excluded by its skip form")
        self.assertEqual((res["details"].get("baseline") or {}).get("ran"), True,
                         "the baseline actually ran")
        # the base worktree lived under the directory the container was
        # mounted — its re-run executes show as the container's, never the
        # host's (a host-side baseline run leaves no exec here)
        clone = fx.repo("app")
        base_execs = [e for e in fb.command_execs("pytest") if e[1] != clone]
        self.assertTrue(base_execs,
                        "the baseline's re-runs executed in the container, in the mounted base worktree")
        logs_dir = os.path.join(fx.capsule, "logs")
        for e in base_execs:
            self.assertTrue(e[1].startswith(logs_dir),
                            "the base worktree sits under the run's mounted base root: %s" % e[1])
        # the first run, the re-run at HEAD and the skip-form confirm went
        # through the same container as the baseline
        self.assertGreaterEqual(len([e for e in fb.command_execs("pytest") if e[1] == clone]), 3,
                                "the command, its re-run and its confirm all ran in the container")
        self.assertEqual(sorted({e[0] for e in fb.execs}), ["cid-1"])
        # the base worktree is gone again: nothing left under the base root
        left = [p for p in glob.glob(os.path.join(logs_dir, "*", "base", "pf-verify-base-*"))]
        self.assertEqual(left, [], "the throwaway base worktree is cleaned up")
        listed = git(clone, "worktree", "list")
        self.assertEqual(len([ln for ln in listed.split("\n") if ln.strip()]), 1, listed)

    def test_unchanged_image_reuses_verification_cache(self) -> None:
        """AC3: an unchanged image is never rebuilt — close after close,
        verification between closes, all reference one image tag and one
        cache volume, and each run still executes the selection inside
        the container (the cache is the image and the tools, never a
        skipped command); a Dockerfile byte change builds the next image,
        a code-only commit does not, a deleted local image is rebuilt,
        and another clone path shares the image but never the cache
        volume."""
        fx, eng = self.declared_repo("fx", cmds=[cmd_of("gate-1", "fast", "echo gate-ok")])
        fb = self.fake_box()
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        tag = "pf-verify-app:" + hashlib.sha256(TEST_DOCKERFILE.encode()).digest()[:16].hex()
        self.assertEqual([(b[0], b[1]) for b in fb.builds], [(tag, TEST_DOCKERFILE.encode())],
                         "the first run builds the image once, from the committed bytes")
        volume = sorted(fb.starts[0].volumes)[0]
        self.assertTrue(volume.startswith("pf-verify-cache-app-"), volume)
        execs_after_first = len(fb.execs)
        # the second close: the image is present, nothing builds, and the
        # selection still runs in a container of the same image
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close2.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(len(fb.builds), 1, "an unchanged image is never rebuilt")
        self.assertFalse(res["details"]["container"]["built"])
        self.assertGreater(len(fb.execs), execs_after_first,
                           "the image cache is never a skipped command: the selection still ran")
        self.assertEqual(sorted(fb.starts[1].volumes), [volume], "the same clone keeps one cache volume")
        # verification between closes: the same image, still no build
        r = run_verify(fx.capsule, eng, runtime=fb)
        self.assertEqual(r.exit, 0, r.stderr)
        self.assertEqual(len(fb.builds), 1, "verification shares the close's image")
        # close again after verify: still one build, the same volume
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close3.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(len(fb.builds), 1)
        self.assertEqual(sorted(fb.starts[3].volumes), [volume])
        # a Dockerfile byte change: the next generation builds
        fx.commit_in_repo("app", {DOCKERFILE: DOCKERFILE2}, "bump the image")
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close4.json"))
        self.assertEqual(code, 0, res.get("summary"))
        tag2 = "pf-verify-app:" + hashlib.sha256(DOCKERFILE2.encode()).digest()[:16].hex()
        self.assertEqual([(b[0], b[1]) for b in fb.builds[1:]], [(tag2, DOCKERFILE2.encode())],
                         "changed bytes build the next generation")
        # a code-only commit: nothing rebuilds
        fx.commit_in_repo("app", {"pin/note.txt": "the answer is 43\n"}, "a code-only edit")
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close5.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(len(fb.builds), 2, "a code-only commit does not rebuild the image")
        # the local image deleted: the same generation builds again
        fb.present[tag2] = False
        code, res = box_close(fx.capsule, "app", eng, fb, os.path.join(fx.capsule, "close6.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual([(b[0], b[1]) for b in fb.builds[2:]], [(tag2, DOCKERFILE2.encode())],
                         "a deleted local image is rebuilt")
        # another clone path: the same image is shared, the cache volume is not
        root2 = os.path.join(self.root, "other")
        os.makedirs(root2)
        fx2 = Fixture(root2, {"app": {"pin/note.txt": "the answer is 42\n"}})
        fx2.commit_in_repo("app", {DOCKERFILE: TEST_DOCKERFILE, "pin/note.txt": "the answer is 42\n"},
                           "declare the verification image")
        eng2 = fake_engine(root2, {"app": report("scoped", cmd_of("gate-1", "fast", "echo gate-ok"))})
        code, res = box_close(fx2.capsule, "app", eng2, fb, os.path.join(fx2.capsule, "close.json"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(len(fb.builds), 3, "another clone of the same Dockerfile shares the image")
        vol2 = sorted(fb.starts[-1].volumes)[0]
        self.assertNotEqual(vol2, volume, "another clone path never shares the cache volume")


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
