# carries: workflows/_shared/scripts/fast_checks.py
"""fast_checks.py's grouped parallelism (polyforge#254): the fast and
close-scoped plans run their commands group by group — the groups side by
side (at most the plan's parallelism at once, two by default), each
group's own commands in plan order — while the result file, its log
numbers, the reuse keys, the flaky re-run and the baseline triage keep
their meaning; a plan with one group, or ``--parallel 1``, takes the
serial path exactly as before.

The fixtures are real git clones with a repository root and a nested
engine root, each contributing its own fast gates: two groups. The
expected orders derive from the gates' own events (each command appends
its start and end to a file the test reads) and from the plan order the
conf declares, never from the implementation's output.
"""

import json
import os
import re
import subprocess
import sys
import typing
import unittest
from typing import Any, Dict, List, Optional, Tuple

from .gitworld import GitCase, Repo, git, write
from .test_fast_checks import env_without

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPTS = os.path.dirname(HERE)
ENGINE = os.path.join(SCRIPTS, "verify-scope")
FAST_CHECKS = os.path.join(SCRIPTS, "fast_checks.py")

GO_MOD = "module demo\n\ngo 0.0.1\n"
APP = "package app\n\nfunc Answer() int {\n\treturn 42\n}\n"


def gate(events: str, name: str, sleep: str = "", wait_for: str = "") -> str:
    """One fast gate that appends its start and end to the events file:
    it waits (bounded) for wait_for's start line first and sleeps between
    the two lines."""
    wait = ""
    if wait_for:
        wait = ("n=0; while [ $n -lt 200 ] && ! grep -qx %s %s; do sleep 0.05; n=$((n+1)); done; "
                % (wait_for, events))
    nap = ("sleep %s; " % sleep) if sleep else ""
    return "echo %s >> %s; %s%s echo %s.end >> %s" % (name + ".start", events, wait, nap, name, events)


class ParallelCase(GitCase):
    """A capsule with one task repo that has a repository root and a
    nested engine root, each with two fast gates writing to one events
    file; the change touches both roots, so both are planned."""

    def setUp(self) -> None:
        super().setUp()
        self.capsule = self.new_dir("capsule")
        self.out = os.path.join(self.capsule, "fast-checks.json")
        self.events = os.path.join(self.capsule, "events.txt")
        self.conf = (
            "stack=go\n"
            "fast_gate=%s\n"
            "fast_gate=%s\n"
            "full_command=echo full-ran > full.txt\n"
        ) % (gate(self.events, "A1", sleep="1"), gate(self.events, "A2"))
        self.nested_conf = (
            "stack=go\n"
            "fast_gate=%s\n"
            "fast_gate=%s\n"
            "full_command=echo nested-full > nested-full.txt\n"
        ) % (gate(self.events, "B1", wait_for="A1.start"), gate(self.events, "B2"))

    def two_root_repo(self, conf: str = "", nested_conf: str = "",
                      change: Optional[Dict[str, str]] = None) -> str:
        """The clone, one commit ahead of its origin; the change touches
        the root's app/ and the nested root's pi/."""
        bare = os.path.join(self.tmp, "app.git")
        git(self.tmp, "init", "-q", "--bare", "-b", "main", "app.git")
        clone = Repo(os.path.join(self.capsule, "repos", "app"), {
            "go.mod": GO_MOD,
            ".polyforge/verify-scope.conf": conf or self.conf,
            "app/app.go": APP,
            "pi/.polyforge/verify-scope.conf": nested_conf or self.nested_conf,
            "pi/note.txt": "pi\n",
        })
        git(clone.dir, "remote", "add", "origin", bare)
        git(clone.dir, "push", "-q", "origin", "main")
        git(clone.dir, "remote", "set-head", "origin", "-a")
        clone.commit("the change", change or {"app/app.go": APP.replace("42", "43"), "pi/note.txt": "pi 2\n"})
        return clone.dir

    def run_checks(self, extra: Tuple[str, ...] = (), out: str = "") -> Tuple[int, Dict[str, Any]]:
        """fast_checks.py --repo app --phase fast, as a skill runs it."""
        out = out or self.out
        argv = [sys.executable, FAST_CHECKS, "--capsule", self.capsule, "--engine", ENGINE,
                "--repo", "app", "--phase", "fast", "--deadline", "10m", "--out", out, *extra]
        c = subprocess.run(argv, capture_output=True, text=True, env=env_without("PF_CAPSULE"))
        if not os.path.exists(out):
            raise AssertionError("fast_checks.py wrote no result file (exit %d): %s"
                                 % (c.returncode, c.stderr[-800:]))
        with open(out, encoding="utf-8") as f:
            return c.returncode, json.load(f)

    def events_of(self) -> List[str]:
        try:
            with open(self.events, encoding="utf-8") as f:
                return [ln for ln in f.read().splitlines() if ln]
        except OSError:
            return []

    def commands_of(self, res: Dict[str, Any]) -> List[Dict[str, Any]]:
        return typing.cast(List[Dict[str, Any]], res["details"]["commands"])

    def log_seq(self, entry: Dict[str, Any]) -> int:
        """The number of the command's log directory (<n>/command.log)."""
        m = re.search(r"/(\d+)/command\.log$", entry.get("log", ""))
        if m is None:
            raise AssertionError("no numbered command log in %r" % entry.get("log"))
        return int(m.group(1))


class HostGroupsTest(ParallelCase):
    """AC1: the two report roots' fast gates run side by side, each
    group's own commands in plan order, the result file and its log
    numbers in plan order; one group or --parallel 1 is the serial plan."""

    def test_groups_run_concurrently_order_kept(self) -> None:
        self.two_root_repo()
        code, res = self.run_checks()
        self.assertEqual(code, 0, res.get("summary"))
        events = self.events_of()
        # each group's own commands keep plan order
        self.assertLess(events.index("A1.end"), events.index("A2.start"), events)
        self.assertLess(events.index("B1.end"), events.index("B2.start"), events)
        # and the groups really overlap: B1 starts inside A1's run
        self.assertLess(events.index("B1.start"), events.index("A1.end"), events)
        cmds = self.commands_of(res)
        self.assertEqual([(c["name"], c["root"]) for c in cmds],
                         [("fast-1", "repos/app"), ("fast-2", "repos/app"),
                          ("pi/fast-1", "repos/app/pi"), ("pi/fast-2", "repos/app/pi")])
        self.assertEqual([c["phase"] for c in cmds], ["fast"] * 4)
        # the log numbers are the plan's, never the completion order's
        self.assertEqual([self.log_seq(c) for c in cmds], sorted(self.log_seq(c) for c in cmds))
        # the full phase never ran in either root
        self.assertFalse(os.path.exists(os.path.join(self.capsule, "repos", "app", "full.txt")))
        self.assertFalse(os.path.exists(os.path.join(self.capsule, "repos", "app", "pi", "nested-full.txt")))

    def test_parallel_1_is_the_serial_plan(self) -> None:
        self.two_root_repo()
        code, res = self.run_checks(("--parallel", "1"))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(self.events_of(),
                         ["A1.start", "A1.end", "A2.start", "A2.end",
                          "B1.start", "B1.end", "B2.start", "B2.end"])
        cmds = self.commands_of(res)
        self.assertEqual([c["name"] for c in cmds], ["fast-1", "fast-2", "pi/fast-1", "pi/fast-2"])
        self.assertEqual([self.log_seq(c) for c in cmds], sorted(self.log_seq(c) for c in cmds))

    def test_a_single_group_takes_the_serial_path(self) -> None:
        """--root . narrows the plan to the repository root's one group:
        the serial plan, unchanged."""
        self.two_root_repo()
        code, res = self.run_checks(("--root", "."))
        self.assertEqual(code, 0, res.get("summary"))
        self.assertEqual(self.events_of(), ["A1.start", "A1.end", "A2.start", "A2.end"])
        self.assertEqual([c["name"] for c in self.commands_of(res)], ["fast-1", "fast-2"])

    def test_an_invalid_parallel_flag_is_a_usage_error(self) -> None:
        self.two_root_repo()
        for bad in ("0", "-1", "two", "2.5"):
            c = subprocess.run([sys.executable, FAST_CHECKS, "--capsule", self.capsule, "--engine", ENGINE,
                                "--repo", "app", "--phase", "fast", "--deadline", "10m",
                                "--out", self.out, "--parallel", bad],
                               capture_output=True, text=True, env=env_without("PF_CAPSULE"))
            self.assertEqual(c.returncode, 2, (bad, c.stderr[-400:]))
            self.assertIn("--parallel", c.stderr)


class HostSemanticsTest(ParallelCase):
    """AC1: the parallel path keeps the serial path's meaning — a green
    run is still reused (never executed, the original log kept), a skipped
    command still takes no log number, one command's failure or timeout
    stops neither its group nor the other one, and heads, entries and
    summary are the serial run's."""

    def test_reuse_still_skips_the_execution(self) -> None:
        self.two_root_repo()
        code, first = self.run_checks()
        self.assertEqual(code, 0, first.get("summary"))
        events = self.events_of()
        code, second = self.run_checks()
        self.assertEqual(code, 0, second.get("summary"))
        cmds = self.commands_of(second)
        self.assertEqual([c.get("reused") for c in cmds], [True] * 4, cmds)
        self.assertEqual(self.events_of(), events, "a reused command is not executed")
        self.assertEqual([c["log"] for c in cmds],
                         [c["log"] for c in self.commands_of(first)], "a reused entry keeps its original log")
        self.assertIn("4 reused", second["summary"])

    def test_skip_takes_no_log_number(self) -> None:
        """A gate missing its declared prerequisite is skipped with its
        reason, and the commands that run keep the plan's contiguous
        numbers (the skip takes none)."""
        self.two_root_repo(
            conf=("stack=go\n"
                  "check=first|fast|echo first >> %(e)s\n"
                  "check=absent|fast|echo absent >> %(e)s\n"
                  "check_requires_cmd=absent|no-such-bin-pf254\n"
                  "check=last|fast|echo last >> %(e)s\n") % {"e": self.events},
            nested_conf=self.nested_conf)
        code, res = self.run_checks()
        self.assertEqual(code, 0, res.get("summary"))
        cmds = self.commands_of(res)
        self.assertEqual([c["name"] for c in cmds], ["first", "absent", "last", "pi/fast-1", "pi/fast-2"])
        self.assertTrue(cmds[1]["skipped"])
        self.assertIn("no-such-bin-pf254", cmds[1]["skip_reason"])
        self.assertNotIn("log", cmds[1])
        ran = [self.log_seq(c) for c in cmds if not c.get("skipped")]
        self.assertEqual(ran, list(range(ran[0], ran[0] + len(ran))), "a skip takes no log number")

    def test_one_groups_failure_does_not_stop_the_other(self) -> None:
        self.two_root_repo(
            conf=("stack=go\n"
                  "check=fail|fast|exit 3\n"
                  "check=after|fast|echo after.done >> %(e)s\n") % {"e": self.events},
            nested_conf=self.nested_conf)
        code, res = self.run_checks()
        self.assertEqual(code, 1, res.get("summary"))
        cmds = self.commands_of(res)
        self.assertEqual([c["name"] for c in cmds], ["fail", "after", "pi/fast-1", "pi/fast-2"])
        self.assertEqual([c["exit_code"] for c in cmds], [3, 0, 0, 0])
        events = self.events_of()
        # the failing group's own next command ran, and so did the other group
        self.assertIn("after.done", events)
        self.assertIn("B1.end", events)
        self.assertIn("B2.end", events)
        self.assertIn("`exit 3`", res["summary"])

    def test_one_commands_timeout_does_not_stop_its_group(self) -> None:
        self.two_root_repo(
            conf=("stack=go\n"
                  "check=slow|fast|sleep 5; echo slow.done >> %(e)s\n"
                  "check_timeout=slow|100ms\n"
                  "check=after|fast|echo after.done >> %(e)s\n") % {"e": self.events},
            nested_conf=self.nested_conf)
        code, res = self.run_checks()
        self.assertEqual(code, 1, res.get("summary"))
        by_name = {c["name"]: c for c in self.commands_of(res)}
        self.assertTrue(by_name["slow"]["timed_out"], by_name["slow"])
        self.assertEqual(by_name["after"]["exit_code"], 0, "the group ran on after the timeout")
        self.assertEqual(by_name["pi/fast-2"]["exit_code"], 0, "the other group ran too")
        events = self.events_of()
        self.assertIn("after.done", events)
        self.assertNotIn("slow.done", events, "the timeout killed the command whole")

    def test_heads_and_reds_are_the_serial_ones(self) -> None:
        """The same tree and plan: the serial and the parallel run record
        the same commands, exit codes, heads and summary — only the
        durations and the log directories differ."""
        self.two_root_repo()
        serial_out = os.path.join(self.capsule, "serial.json")
        parallel_out = os.path.join(self.capsule, "parallel.json")
        code_s, serial = self.run_checks(("--parallel", "1"), out=serial_out)
        code_p, parallel = self.run_checks(out=parallel_out)
        self.assertEqual((code_s, code_p), (0, 0), (serial.get("summary"), parallel.get("summary")))
        self.assertEqual(serial["heads"], parallel["heads"])
        self.assertEqual(serial["summary"], parallel["summary"])
        fields = ("name", "phase", "root", "cmd", "exit_code", "skipped", "reused", "source")
        self.assertEqual([{k: c.get(k) for k in fields} for c in self.commands_of(serial)],
                         [{k: c.get(k) for k in fields} for c in self.commands_of(parallel)])
        self.assertEqual([p["root"] for p in serial["details"]["reports"]],
                         [p["root"] for p in parallel["details"]["reports"]])
