# carries: workflows/_shared/scripts/verify-scope, workflows/_shared/scripts/pfstep/audit.py, workflows/_shared/scripts/verify.py, workflows/_shared/scripts/gate.py, workflows/_shared/scripts/tests/test_audit_param.py
"""polyforge#138: the ``audit`` step parameter (PF_PARAM_AUDIT), the
project owner's switch for the engine-appended dependency-audit gate.

The acceptance pinned here (AC2/AC3, the engine and the two steps' side;
AC1 is the lanes' yaml, pinned by internal/contracttest/
w138_audit_param_contract_test.go; AC4's documents are the review step's):

- the engine takes ``--audit <on|off>`` — only the lower-case pair, any
  other value (or a missing one) a usage error, exit 2; the default (no
  option) is today's behavior byte for byte: the ``audit`` command still
  closes every full-tier report of a repository with an analyzer, and
  ``--audit on`` answers the same report the default does;
- ``--audit off`` drops the ``audit`` command from the full tail and
  nothing else — the other commands, their order and the report's keys
  stay;
- verify.py and gate.py read PF_PARAM_AUDIT the same way (on, empty and
  unset are today's behavior; anything else a named error, exit 2, before
  any check or container starts, carrying the fixed mark
  ``pf-audit-param-invalid``); when it is ``off`` they hand the engine
  ``--audit off`` on every engine invocation, and their results record
  the closure — verify.py's result.json an ``audit_disabled`` note,
  gate.py's gate.json and green-tag record an ``audit_off`` note, both
  the merged source hint (the owner's 1-B ruling: the specific layer,
  project config or pf override, is not distinguished);
- a green tag written with the audit off never lends itself to a run
  with the audit on (and the other way round): the reuse comparison
  includes the audit note, so even a plan that carries no audit surface
  (and so the same digest) keeps the two states' evidence apart;
- a tree whose own scope engine predates ``--audit`` is never silently
  audited nor silently switched to the lane's engine: with the audit off
  the gate refuses, named, before its container starts.

The expected values come from the ticket and the owner's 2026-11-19
rulings (2-A: the scripts parse the parameter and pass an explicit engine
option; 4-A: the default is byte for byte unchanged; 6-A: only lower-case
on/off, empty or unset reads as on, everything else a named error before
any check runs; 3-A: an old tree engine is blocked by name), never from
this implementation's own output."""

import json
import os
import subprocess
import tempfile
import unittest
from typing import Any, Dict, List, cast

from .gitworld import GitCase, Repo
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, Run, cmd_of, engine_args, fake_engine, report, run_verify

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENGINE = os.path.join(SCRIPTS, "verify-scope")

# The closure note AC3 demands, the owner's 1-B ruling verbatim: the merged
# source hint, no distinction between the project's config and a pf
# override. Pinned as a literal here — the ticket's words, never this
# implementation's own output.
AUDIT_OFF_NOTE = "PF_PARAM_AUDIT=off，来自项目配置或 pf override"


def run_engine(d: str, *flags: str) -> "subprocess.CompletedProcess[str]":
    return subprocess.run(["python3", ENGINE, "--json", "--full", "--base", "HEAD", *flags], cwd=d,
                           capture_output=True, text=True, stdin=subprocess.DEVNULL)


def engine_cmds(c: "subprocess.CompletedProcess[str]") -> List[Dict[str, Any]]:
    return cast(List[Dict[str, Any]], json.loads(c.stdout)["commands"])


def audits(cmds: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    return [c for c in cmds if c["name"] == "audit" or "audit-deps" in c["cmd"]]


class EngineAuditTest(GitCase):
    """verify-scope's --audit switch: the full tail's dependency-audit
    command is the default's, on keeps it, off drops it alone, and any
    other value is a usage error."""

    def go_repo(self, name: str) -> str:
        d = self.new_dir(name)
        Repo(d, {"go.mod": "module example.invalid/" + name + "\n", "src/a.go": "package src\n"})
        return d

    def npm_repo(self, name: str) -> str:
        d = self.new_dir(name)
        Repo(d, {".polyforge/verify-scope.conf": "stack=go\n", "go.mod": "module example.invalid/" + name + "\n",
                 "package-lock.json": "{\n  \"name\": \"" + name + "\",\n  \"lockfileVersion\": 3\n}\n"})
        return d

    def test_the_default_full_tail_closes_with_exactly_one_audit(self) -> None:
        c = run_engine(self.go_repo("default"))
        self.assertEqual(c.returncode, 0, c.stderr)
        cmds = engine_cmds(c)
        found = audits(cmds)
        self.assertEqual(len(found), 1, "want exactly one audit command, got %s" % json.dumps(cmds))
        last = cmds[-1]
        self.assertEqual((last["name"], last["phase"], last["source"]), ("audit", "full", "default"))

    def test_audit_on_answers_the_default_report(self) -> None:
        d = self.go_repo("on")
        default, on = run_engine(d), run_engine(d, "--audit", "on")
        self.assertEqual(default.returncode, 0, default.stderr)
        self.assertEqual(on.returncode, 0, on.stderr)
        self.assertEqual(engine_cmds(on), engine_cmds(default), "--audit on must not change the report")

    def test_audit_off_drops_the_audit_command_alone(self) -> None:
        d = self.go_repo("off")
        default, off = run_engine(d), run_engine(d, "--audit", "off")
        self.assertEqual(off.returncode, 0, off.stderr)
        without = [c for c in engine_cmds(default) if c not in audits(engine_cmds(default))]
        self.assertEqual(engine_cmds(off), without, "off drops the audit command and nothing else")
        self.assertEqual(audits(engine_cmds(off)), [])

    def test_audit_off_drops_the_audit_of_every_auditable_surface(self) -> None:
        d = self.npm_repo("surfaces")
        default, off = run_engine(d), run_engine(d, "--audit", "off")
        self.assertEqual(default.returncode, 0, default.stderr)
        self.assertEqual(off.returncode, 0, off.stderr)
        self.assertIn(" --go ", default.stdout, "the fixture's audit covers the go surface")
        self.assertIn(" --npm ", default.stdout, "the fixture's audit covers an npm surface")
        self.assertEqual(audits(engine_cmds(off)), [], "off drops the mixed-surface audit too")

    def test_any_other_value_is_a_usage_error(self) -> None:
        d = self.go_repo("values")
        for argv in (["--audit", "ON"], ["--audit", "false"], ["--audit", " off"], ["--audit", ""], ["--audit"]):
            c = run_engine(d, *argv)
            self.assertEqual(c.returncode, 2, "%s must be a usage error, not a silent on or off" % (argv,))
            self.assertIn("verify-scope:", c.stderr)

    def test_audit_off_runs_under_the_light_tier_too(self) -> None:
        d = self.go_repo("light")
        c = subprocess.run(["python3", ENGINE, "--json", "--light", "--base", "HEAD", "--audit", "off"], cwd=d,
                           capture_output=True, text=True, stdin=subprocess.DEVNULL)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertEqual(audits(engine_cmds(c)), [])


if __name__ == "__main__":
    unittest.main()


class VerifyAuditParamTest(unittest.TestCase):
    """verify.py's side of the audit parameter (PF_PARAM_AUDIT): the four
    values of the matrix — on, unset (and empty, its equal), off, and
    every other value a named error before any check runs. The fake scope
    engine (tests/verifyfix.py) logs its argv, so the option handed to
    the engine is asserted, not inferred."""

    def setUp(self) -> None:
        if not HAVE_GIT or not HAVE_BASH:
            self.skipTest("git and bash are required")
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def fixture(self) -> Any:
        root = tempfile.mkdtemp(prefix="fx-", dir=self.root)
        fx = Fixture(root, {"app": {"lib/a.txt": "a\n"}})
        fx.commit_in_repo("app", {"lib/a.txt": "a b\n"}, "feature")
        return fake_engine(root, {
            "app": report("scoped", cmd_of("gate", "fast", "echo plan-ok")),
            "app.full": report("full", cmd_of("gate", "fast", "echo plan-ok"),
                               cmd_of("tail", "full", "echo full-ok")),
        })

    def capsule(self, eng: str) -> str:
        return os.path.join(os.path.dirname(os.path.dirname(eng)), "capsule")

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def test_off_hands_the_engine_the_option_and_records_the_closure(self) -> None:
        eng = self.fixture()
        r = run_verify(self.capsule(eng), eng, tier="full",
                       env={"PF_PARAM_AUDIT": "off"})
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertTrue(engine_args(eng), "the engine was never asked")
        for line in engine_args(eng):
            self.assertIn("--audit off", line, "every engine invocation carries the option: %s" % line)
        d = self.doc(r)
        self.assertEqual(d.get("audit_disabled"), AUDIT_OFF_NOTE,
                         "the result must record the closure with the merged source hint")
        self.assertEqual([c["name"] for c in d["checks"] if c["status"] == "fail"], [])

    def test_on_empty_and_unset_change_nothing(self) -> None:
        for value in ("on", "", None):
            eng = self.fixture()
            env = {} if value is None else {"PF_PARAM_AUDIT": value}
            r = run_verify(self.capsule(eng), eng, tier="full", env=env)
            self.assertEqual(r.exit, 0, r.result.get("summary"))
            for line in engine_args(eng):
                self.assertNotIn("--audit", line, "on must hand the engine no audit option: %s" % line)
            d = self.doc(r)
            self.assertNotIn("audit_disabled", d, "an on run records no closure")

    def test_every_other_value_is_a_named_error_before_any_check(self) -> None:
        for value in ("ON", "false", " off", "0"):
            eng = self.fixture()
            r = run_verify(self.capsule(eng), eng, tier="full",
                           env={"PF_PARAM_AUDIT": value})
            self.assertEqual(r.exit, 2, "%r must be a named error, not a silent on or off" % value)
            self.assertIn("pf-audit-param-invalid", r.result.get("summary", ""),
                          "%r: the fixed mark names the error" % value)
            self.assertIn("PF_PARAM_AUDIT", r.result.get("summary", ""))
            self.assertIsNone(r.doc, "%r: no result.json — nothing ran" % value)
            self.assertEqual(engine_args(eng), [], "%r: the engine was never asked" % value)


if __name__ == "__main__":
    unittest.main()
