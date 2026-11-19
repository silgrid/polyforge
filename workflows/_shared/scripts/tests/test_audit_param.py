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

import datetime
import json
import os
import subprocess
import tempfile
import unittest
from typing import Any, Dict, List, cast

import gate
from .fakebox import FakeRuntime
from .gatefix import (HAVE_BASH, HAVE_GIT, HAVE_PY, IDENT, commit_all, gate_clone, git_t, green_clone,
                      run_gate, run_main, write_files)
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


NOW = datetime.datetime(2026, 11, 8, 9, 30, 0, tzinfo=datetime.timezone.utc)

# The gate fixtures' conf: a go stack (an analyzer, so the engine appends
# the audit over the root go.mod) with the commands the fake container
# runs as plain echoes.
GATE_CONF = "stack=go\nfast_gate=echo one\nfull_command=echo full-ok\n"


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class GateAuditParamTest(unittest.TestCase):
    """gate.py's side of the audit parameter: the four values of the
    matrix over the tree's own real scope engine (gatefix's clone carries
    it), the plan and the result judged through the fake container."""

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.addCleanup(self._td.cleanup)
        self.root = os.path.realpath(self._td.name)
        self.fb = FakeRuntime(os.path.join(self.root, "box-tmp"))

    def clone(self, name: str = "app") -> str:
        d = gate_clone(self.root, GATE_CONF, name=name)
        write_files(d, {"go.mod": "module example.invalid/app\n"})
        commit_all(d, "the auditable surface")
        return d

    def test_off_plans_without_the_audit_and_records_the_closure(self) -> None:
        d = self.clone("off")
        r = run_gate(d, self.fb, os.path.join(self.root, "logs"), env={"PF_PARAM_AUDIT": "off"})
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertTrue(r.res.passed, r.progress)
        self.assertIn("--audit off", r.res.engine, "the engine invocation carries the option")
        for c in r.res.plan["commands"] if r.res.plan else []:
            self.assertNotIn("audit-deps", c["cmd"], "off plans no audit command: %s" % c["cmd"])
        self.assertEqual([c.name for c in r.res.commands], ["fast-1", "full-1"], r.progress)
        self.assertEqual(r.res.audit_off, AUDIT_OFF_NOTE)
        self.assertEqual(r.res.as_dict()["audit_off"], AUDIT_OFF_NOTE)

    def test_on_and_unset_keep_the_audit_and_add_no_field(self) -> None:
        for i, env in enumerate(({}, {"PF_PARAM_AUDIT": "on"}, {"PF_PARAM_AUDIT": ""})):
            d = self.clone("on-%d" % i)
            r = run_gate(d, self.fb, os.path.join(self.root, "logs"), env=env)
            self.assertEqual(r.res.incomplete, "", r.progress)
            # the audit command itself is the real audit-deps over the
            # fixture's go.mod — planned and attempted exactly as today,
            # whether it can run on this machine is not this test's point
            self.assertNotIn("--audit", r.res.engine, "on hands the engine no audit option")
            self.assertTrue(any("audit-deps" in c.cmd for c in r.res.commands),
                            "the audit still closes the plan: %s" % [c.name for c in r.res.commands])
            self.assertEqual([c.name for c in r.res.commands], ["fast-1", "full-1", "audit"], r.progress)
            self.assertEqual(r.res.audit_off, "")
            self.assertNotIn("audit_off", r.res.as_dict(), "an on run adds no field")

    def test_every_other_value_is_a_named_error_before_the_container(self) -> None:
        d = self.clone("values")
        for value in ("ON", "false", " off", "0"):
            out = os.path.join(self.root, "gate-%s.json" % value.strip() or "x")
            m = run_main(["--isolation", "container", "--json", "--out", out], d,
                         env={"PF_PARAM_AUDIT": value}, runtime=self.fb)
            self.assertEqual(m.exit, 2, "%r must be a named error, not a silent on or off" % value)
            self.assertIn("pf-audit-param-invalid", m.stdout + m.stderr, "%r: the fixed mark names the error" % value)
            self.assertIn("PF_PARAM_AUDIT", m.stdout + m.stderr)
            self.assertEqual(self.fb.starts, [], "%r: no container was ever started" % value)


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class AuditEvidenceTest(unittest.TestCase):
    """The closure's evidence chain: a green tag of an off run carries the
    note, an on tag never lends itself to an off run (a tree without an
    auditable surface plans the same either way, so only the note keeps
    the two states' tags apart), and a tree engine predating --audit is
    blocked by name, never silently audited nor switched to the lane's."""

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.addCleanup(self._td.cleanup)
        self.root = os.path.realpath(self._td.name)
        self.fb = FakeRuntime(os.path.join(self.root, "box-tmp"))
        self.g = gate.Git(dict(os.environ, **IDENT))

    def record(self, origin: str, name: str) -> Dict[str, str]:
        body = git_t(origin, "for-each-ref", "--format=%(contents)", "refs/tags/" + name)
        return cast(Dict[str, str], json.loads(body))

    def test_a_green_tag_of_an_off_run_carries_the_closure_note(self) -> None:
        d = green_clone(self.root, GATE_CONF)
        write_files(d, {"go.mod": "module example.invalid/app\n"})
        commit_all(d, "the auditable surface")
        r = run_gate(d, self.fb, os.path.join(self.root, "logs"), env={"PF_PARAM_AUDIT": "off"})
        self.assertTrue(r.res.passed, r.progress)
        self.assertEqual([c.name for c in r.res.commands], ["fast-1", "full-1"], r.progress)
        name, created = gate.mark_green(self.g, d, "origin", "s", r.res, now=NOW)
        self.assertEqual((name, created), ("green/20261108-1", True))
        rec = self.record(os.path.join(self.root, "origin.git"), name)
        self.assertEqual(rec.get("audit_off"), AUDIT_OFF_NOTE, "the tag's record carries the closure note")
        # the same off record twice: the tag is reused, nothing pushed
        again, created = gate.mark_green(self.g, d, "origin", "s", r.res, now=NOW)
        self.assertEqual((again, created), (name, False))

    def test_an_on_tag_is_never_reused_for_an_off_run(self) -> None:
        # stack=none: no analyzer, no audit surface — on and off plan the
        # very same commands and the same digest; only the audit note
        # keeps the two states' evidence apart
        d = green_clone(self.root, "stack=none\nfast_gate=echo one\n")
        on = run_gate(d, self.fb, os.path.join(self.root, "logs"))
        self.assertTrue(on.res.passed, on.progress)
        name, created = gate.mark_green(self.g, d, "origin", "s", on.res, now=NOW)
        self.assertEqual((name, created), ("green/20261108-1", True))
        off = run_gate(d, self.fb, os.path.join(self.root, "logs"), env={"PF_PARAM_AUDIT": "off"})
        self.assertTrue(off.res.passed, off.progress)
        self.assertEqual(off.res.plan_digest, on.res.plan_digest, "the fixture's plan carries no audit surface")
        name2, created2 = gate.mark_green(self.g, d, "origin", "s", off.res, now=NOW)
        self.assertEqual((name2, created2), ("green/20261108-2", True),
                         "the on tag never lends itself to the off run")
        self.assertEqual(self.record(os.path.join(self.root, "origin.git"), name2).get("audit_off"), AUDIT_OFF_NOTE)
        # and the other way round: an off tag is not the on run's either
        on_again = run_gate(d, self.fb, os.path.join(self.root, "logs"))
        name3, created3 = gate.mark_green(self.g, d, "origin", "s", on_again.res, now=NOW)
        self.assertEqual((name3, created3), ("green/20261108-1", False), "the on record's own tag, reused")

    def test_a_tree_engine_predating_audit_is_blocked_by_name(self) -> None:
        d = gate_clone(self.root, GATE_CONF)
        # the tree's engine is a stub of the era before polyforge#138:
        # its --help answers a usage that names no --audit option
        with open(os.path.join(d, gate.GATE_ENGINE_REL), "w", encoding="utf-8") as f:
            f.write("#!/usr/bin/env python3\n"
                    "import sys\n"
                    "if \"--help\" in sys.argv:\n"
                    "    print(\"usage: verify-scope [--json] [--full | --micro | --light] [--base <rev>]\","
                    " file=sys.stderr)\n"
                    "    sys.exit(0)\n"
                    "sys.stderr.write(\"the stub plans nothing\\n\")\n"
                    "sys.exit(3)\n")
        os.chmod(os.path.join(d, gate.GATE_ENGINE_REL), 0o755)
        write_files(d, {"go.mod": "module example.invalid/app\n"})
        commit_all(d, "the auditable surface")
        r = run_gate(d, self.fb, os.path.join(self.root, "logs"), env={"PF_PARAM_AUDIT": "off"})
        self.assertFalse(r.res.passed)
        self.assertIn("PF_PARAM_AUDIT=off", r.res.incomplete)
        self.assertIn("predates the --audit option", r.res.incomplete)
        self.assertIn("not judged", r.res.incomplete)
        self.assertEqual(self.fb.starts, [], "no container was started for a blocked gate")
        self.assertEqual(r.res.audit_off, AUDIT_OFF_NOTE)

    def test_the_lanes_engine_serves_an_off_gate_of_a_tree_without_one(self) -> None:
        d = green_clone(self.root, GATE_CONF, engine=False)
        write_files(d, {"go.mod": "module example.invalid/app\n"})
        commit_all(d, "the auditable surface")
        r = run_gate(d, self.fb, os.path.join(self.root, "logs"), env={"PF_PARAM_AUDIT": "off"})
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertTrue(r.res.passed, r.progress)
        self.assertEqual(r.res.engine_source, "lane")
        self.assertIn("--audit off", r.res.engine)
        self.assertEqual([c.name for c in r.res.commands], ["fast-1", "full-1"], r.progress)


if __name__ == "__main__":
    unittest.main()
