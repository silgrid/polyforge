# carries: workflows/_shared/scripts/verify-scope, workflows/_shared/scripts/tests/test_verify_scope_groups.py
"""polyforge#244: the repository declares the full gate's parallel groups in
its own ``.polyforge/verify-scope.conf`` — ``group=<name>|<full-N|command>``
binds one full-phase command to a group, ``full_parallel=<N>`` is the
parallelism (default 2). The engine reports both in its JSON report, so the
gate can schedule the plan; a repository that declares neither keeps its
report byte for byte (the ``group`` key absent, no ``full_parallel``).

The expected values come from the ticket's owner-confirmed plan review
(Q2: the two keys and the serial fallback for an invalid parallelism;
Q7: the Go / pi-web groups), never from this implementation's output."""

import json
import os
import subprocess
import unittest
from typing import Any, Dict, List, cast

from .gitworld import GitCase, Repo

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ENGINE = os.path.join(SCRIPTS, "verify-scope")


def run_engine(d: str, *flags: str) -> "subprocess.CompletedProcess[str]":
    return subprocess.run(["python3", ENGINE, "--json", "--full", "--base", "HEAD", *flags], cwd=d,
                          capture_output=True, text=True, stdin=subprocess.DEVNULL)


def report_of(c: "subprocess.CompletedProcess[str]") -> Dict[str, Any]:
    return cast(Dict[str, Any], json.loads(c.stdout))


def cmds_of(c: "subprocess.CompletedProcess[str]") -> List[Dict[str, Any]]:
    return cast(List[Dict[str, Any]], json.loads(c.stdout)["commands"])


CONF = "stack=none\nfast_gate=echo fast-one\nfull_command=echo full-one\nfull_command=echo full-two\nfull_command=echo full-three\n"


class ScopeGroupDeclarationTest(GitCase):
    """verify-scope's group declaration: the keys, their echo and their
    serial fallback."""

    def repo(self, name: str, extra: str) -> str:
        d = self.new_dir(name)
        Repo(d, {".polyforge/verify-scope.conf": CONF + extra, "README": "x\n"})
        return d

    def test_declared_groups_are_echoed_on_full_commands(self) -> None:
        d = self.repo("declared", "group=go|full-1\ngroup=pi-web|full-3\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        cmds = cmds_of(c)
        self.assertEqual([x["name"] for x in cmds], ["fast-1", "full-1", "full-2", "full-3"])
        self.assertEqual(cmds[1]["group"], "go")
        self.assertNotIn("group", cmds[2], "full-2 is bound to no group and carries no key")
        self.assertEqual(cmds[3]["group"], "pi-web")
        self.assertNotIn("group", cmds[0], "a fast-phase command is never grouped")
        self.assertEqual(report_of(c)["full_parallel"], 2, "the default parallelism is 2")

    def test_a_group_may_name_a_command_text(self) -> None:
        d = self.repo("text", "group=go|echo full-two\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertEqual([x.get("group") for x in cmds_of(c)], [None, None, "go", None])

    def test_declared_parallelism_is_echoed(self) -> None:
        d = self.repo("parallel", "group=go|full-1\nfull_parallel=3\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertEqual(report_of(c)["full_parallel"], 3)

    def test_a_repository_that_declares_no_group_keeps_its_report_bytes(self) -> None:
        d = self.repo("plain", "")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertNotIn("full_parallel", report_of(c), "an undeclared repository records no parallelism")
        for x in cmds_of(c):
            self.assertNotIn("group", x, "an undeclared repository's commands carry no group key")
        self.assertNotIn("group", c.stderr, "an undeclared repository is diagnosed about no group")
        self.assertNotIn("full_parallel", c.stderr, "an undeclared repository is diagnosed about no parallelism")

    def test_an_invalid_parallelism_is_diagnosed_and_falls_back_to_serial(self) -> None:
        for bad in ("0", "-1", "x", ""):
            with self.subTest(bad):
                d = self.repo("bad-" + (bad or "empty"), "group=go|full-1\nfull_parallel=%s\n" % bad)
                c = run_engine(d)
                self.assertEqual(c.returncode, 0, c.stderr)
                self.assertIn("full_parallel", c.stderr, "an invalid parallelism is diagnosed")
                self.assertEqual(report_of(c)["full_parallel"], 1, "an invalid parallelism falls back to serial")
                self.assertEqual(cmds_of(c)[1]["group"], "go", "the group binding itself stays valid")

    def test_the_first_valid_parallelism_wins(self) -> None:
        """polyforge#244 self-review#1: an invalid line never shadows a later
        valid one, and two valid lines keep the first."""
        d = self.repo("valid-wins", "group=go|full-1\nfull_parallel=0\nfull_parallel=3\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertEqual(report_of(c)["full_parallel"], 3, "the first valid declaration wins over an earlier invalid one")
        d = self.repo("first-valid", "group=go|full-1\nfull_parallel=3\nfull_parallel=7\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertIn("second full_parallel", c.stderr)
        self.assertEqual(report_of(c)["full_parallel"], 3, "a second valid declaration is diagnosed and ignored")

    def test_a_non_full_report_is_silent_about_its_groups(self) -> None:
        """polyforge#244 r1#2: only the full plan carries full-phase commands,
        so a valid group= declaration is no diagnostic in the light, scoped or
        micro reports — otherwise every polyforge work item's verification
        report would accuse the conf of naming nothing."""
        d = self.new_dir("non-full")
        Repo(d, {"go.mod": "module example.invalid/nonfull\n\ngo 1.22\n", "src/a.go": "package src\n",
                 ".polyforge/verify-scope.conf": "stack=go\nfast_gate=echo f\n"
                 "full_command=echo one\nfull_command=echo two\ngroup=go|full-1\ngroup=go|full-2\n"})
        for flags in (["--light"], ["--micro"]):
            with self.subTest(flags):
                c = subprocess.run(["python3", ENGINE, "--json", *flags, "--base", "HEAD"], cwd=d,
                                   capture_output=True, text=True, stdin=subprocess.DEVNULL)
                self.assertEqual(c.returncode, 0, c.stderr)
                self.assertNotIn("group", c.stderr, "a legal declaration is diagnosed in %s" % (flags,))
                self.assertNotIn("full_parallel", report_of(c))
                for x in cmds_of(c):
                    self.assertNotIn("group", x)

    def test_a_group_naming_no_full_command_is_diagnosed_and_dropped(self) -> None:
        d = self.repo("unbound", "group=go|full-9\ngroup=go|no-such-command\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertIn("group", c.stderr)
        self.assertNotIn("full_parallel", report_of(c), "no bound command means no parallelism is recorded")
        for x in cmds_of(c):
            self.assertNotIn("group", x)

    def test_a_fast_phase_command_is_never_grouped(self) -> None:
        d = self.repo("fast", "group=go|fast-1\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertIn("fast-1", c.stderr, "binding a fast-phase command is diagnosed")
        self.assertNotIn("group", cmds_of(c)[0])

    def test_a_second_binding_of_one_command_is_diagnosed(self) -> None:
        d = self.repo("twice", "group=go|full-1\ngroup=pi-web|full-1\n")
        c = run_engine(d)
        self.assertEqual(c.returncode, 0, c.stderr)
        self.assertIn("already in group", c.stderr)
        self.assertEqual(cmds_of(c)[1]["group"], "go", "the first binding wins")

    def test_a_malformed_group_line_is_diagnosed(self) -> None:
        for bad in ("group=go\n", "group=|full-1\n", "group=go|\n", "group=go|full-1|extra\n"):
            with self.subTest(bad=bad.strip()):
                d = self.repo("malformed-" + str(abs(hash(bad))), bad)
                c = run_engine(d)
                self.assertEqual(c.returncode, 0, c.stderr)
                self.assertIn("group", c.stderr, "a malformed group line is diagnosed")
                self.assertNotIn("full_parallel", report_of(c))


if __name__ == "__main__":
    unittest.main()
