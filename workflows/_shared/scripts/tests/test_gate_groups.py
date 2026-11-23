# carries: workflows/_shared/scripts/gate.py, workflows/_shared/scripts/tests/test_gate_groups.py
"""polyforge#244: the full gate's parallel groups. A repository declares the
groups in its own ``.polyforge/verify-scope.conf`` (the engine echoes them on
the report); the gate then runs each group's commands in its own container,
the groups side by side and each group in plan order, and records the result
in plan order. A repository that declares no group runs exactly as before —
the plan bytes, the execution order and the summary are the serial gate's.

The expected values come from the ticket's owner-confirmed plan review (Q1:
one container per group, each reaping its own; Q4: a failure without
``--keep-going`` starts nothing new and running commands finish and are
recorded; Q5: results and summary in plan order, log names by plan index;
Q7: fast gates serially first, the audit after the groups), never from this
implementation's own output."""

import os
import tempfile
import threading
import time
import unittest
from typing import Any, Dict, List

import gate
from pfstep import container, engine

from .fakebox import FakeRuntime
from .gatefix import HAVE_BASH, HAVE_GIT, HAVE_PY, boxed_bash, gate_clone, new_gate, run_gate

# The plan's key set of an undeclared repository, recorded from the serial
# gate before the grouped path existed: a grouped run must leave every one of
# these keys where it was, and add nothing else.
SERIAL_PLAN_KEYS = ["image", "cpus", "memory", "pids", "commands"]
SERIAL_COMMAND_KEYS = ["name", "phase", "cmd", "requires_env", "requires_cmd", "timeout"]


def cmd(name: str, phase: str, text: str, group: str = "") -> engine.EngineCommand:
    c = engine.EngineCommand(name=name, phase=phase, cmd=text)
    c.group = group
    return c


def boxed() -> container.ContainerReport:
    return container.ContainerReport(image="img:1", built=False, build_log="", name="cid", cpus="8",
                                     memory="16g", pids=4096, digest="sha256:" + "0" * 64)


def timeout_of(c: engine.EngineCommand) -> int:
    return 1200


def log_names(res: gate.GateResult) -> List[str]:
    return [os.path.basename(c.log) for c in res.commands]


class PlanIdentityTest(unittest.TestCase):
    """polyforge#244: the plan records the groups (and the parallelism) only
    when the repository declares one — an undeclared repository's plan is
    byte for byte the serial gate's."""

    def plan(self, cmds: List[engine.EngineCommand], parallel: int = 0) -> Dict[str, Any]:
        if parallel:
            return gate.plan_of("/clone", boxed(), cmds, timeout_of, parallel=parallel)
        return gate.plan_of("/clone", boxed(), cmds, timeout_of)

    def test_an_undeclared_plan_keeps_its_key_set(self) -> None:
        p = self.plan([cmd("fast-1", "fast", "echo f"), cmd("full-1", "full", "echo one")])
        self.assertEqual(list(p), SERIAL_PLAN_KEYS, "no group and no parallelism on an undeclared plan")
        for c in p["commands"]:
            self.assertEqual(list(c), SERIAL_COMMAND_KEYS, "a command keeps its serial key set")

    def test_a_declared_plan_records_the_group_and_the_parallelism(self) -> None:
        p = self.plan([cmd("fast-1", "fast", "echo f"),
                       cmd("full-1", "full", "echo one", "go"),
                       cmd("full-2", "full", "echo two", "pi-web")])
        self.assertEqual([c.get("group") for c in p["commands"]], [None, "go", "pi-web"])
        self.assertEqual(p["parallel"], 2, "the plan records the gate's parallelism when a group is bound")
        self.assertEqual(list(p)[:5], SERIAL_PLAN_KEYS, "the serial keys keep their order")
        self.assertEqual(list(p)[5], "parallel", "the parallelism comes last")

    def test_the_parallelism_is_the_effective_one(self) -> None:
        cmds = [cmd("full-1", "full", "echo one", "go")]
        self.assertEqual(self.plan(cmds)["parallel"], 2, "the default parallelism is 2")
        self.assertEqual(self.plan(cmds, parallel=1)["parallel"], 1, "an override is what the plan records")

    def test_the_digest_moves_only_when_a_group_is_declared(self) -> None:
        serial = [cmd("fast-1", "fast", "echo f"), cmd("full-1", "full", "echo one")]
        grouped = [serial[0], cmd("full-1", "full", "echo one", "go")]
        self.assertNotEqual(gate.plan_digest(self.plan(serial)), gate.plan_digest(self.plan(grouped)),
                            "a grouped plan is a different identity from the serial one")
        self.assertEqual(gate.plan_digest(self.plan(serial)),
                         gate.plan_digest(self.plan([cmd("fast-1", "fast", "echo f"),
                                                     cmd("full-1", "full", "echo one")])),
                         "the undeclared plan's identity is deterministic")


# The grouped fixture's commands, in the clone's parent directory (the clone
# itself stays clean): G1 waits for P1 to announce itself and records whether
# the two groups really ran side by side; every command records its own line
# in order.txt, so the within-group order is readable from the outside.
G1 = ("printf 'g1\\n' >> ../order.txt;"
      " for i in $(seq 1 100); do if [ -f ../p1.started ]; then printf 'overlap\\n' >> ../order.txt; exit 0; fi;"
      " sleep 0.05; done; printf 'no-overlap\\n' >> ../order.txt; exit 1")
G2 = "printf 'g2\\n' >> ../order.txt"
P1 = "sleep 0.3; : > ../p1.started; printf 'p1\\n' >> ../order.txt"
P2 = "printf 'p2\\n' >> ../order.txt"
HEAD = "printf 'head\\n' >> ../order.txt"
TAIL = "printf 'tail\\n' >> ../order.txt"

GROUPED_CONF = ("stack=none\n"
                "fast_gate=printf 'fast\\n' >> ../order.txt\n"
                "full_command=%s\n"
                "full_command=%s\n"
                "full_command=%s\n"
                "full_command=%s\n"
                "full_command=%s\n"
                "full_command=%s\n"
                "group=go|full-2\ngroup=go|full-3\n"
                "group=pi-web|full-4\ngroup=pi-web|full-5\n" % (HEAD, G1, G2, P1, P2, TAIL))
# The parallelism-1 fixture: every command only records its line, so plan
# order is readable with no command waiting on another.
ORDER_CONF = ("stack=none\n"
              "fast_gate=printf 'fast\\n' >> ../order.txt\n"
              "full_command=printf 'head\\n' >> ../order.txt\n"
              "full_command=printf 'g1\\n' >> ../order.txt\n"
              "full_command=printf 'g2\\n' >> ../order.txt\n"
              "full_command=printf 'p1\\n' >> ../order.txt\n"
              "full_command=printf 'p2\\n' >> ../order.txt\n"
              "full_command=printf 'tail\\n' >> ../order.txt\n"
              "group=go|full-2\ngroup=go|full-3\n"
              "group=pi-web|full-4\ngroup=pi-web|full-5\n")
# r1#3's fixture: the failing command polls for the pi-web group's start
# marker (bounded), the pi-web command waits for the test's release file
# (bounded), and the third command only announces itself — so it must never
# run.
FAILS = "for i in $(seq 1 600); do [ -f ../p2.started ] && break; sleep 0.01; done; exit 7"
WAITS = ": > ../p2.started; for i in $(seq 1 600); do [ -f ../release ] && break; sleep 0.01; done; exit 0"
ANNOUNCES = ": > ../p3.started"


def peak_cmd(name: str) -> str:
    """One group command that records its start and end and stays up until
    two commands have started (bounded): three groups at parallelism 2 give
    a peak of 2, and a scheduler that lost its bound gives 3."""
    return ("printf 'start %s\\n' >> ../order.txt;"
            " for i in $(seq 1 500); do [ \"$(grep -c '^start' ../order.txt)\" -ge 2 ] && break; sleep 0.01; done;"
            " printf 'end %s\\n' >> ../order.txt" % (name, name))


PEAK_CONF = ("stack=none\n"
             "full_command=%s\nfull_command=%s\nfull_command=%s\n"
             "group=go|full-1\ngroup=pi-web|full-2\ngroup=extra|full-3\n"
             "full_parallel=2\n" % (peak_cmd("a"), peak_cmd("b"), peak_cmd("c")))

SERIAL_CONF = ("stack=none\n"
               "fast_gate=printf 'fast\\n' >> ../order.txt\n"
               "full_command=printf 'one\\n' >> ../order.txt\n"
               "full_command=printf 'two\\n' >> ../order.txt\n")


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class GroupedExecutionTest(unittest.TestCase):
    """polyforge#244: a declared grouping runs the groups side by side, each
    group in plan order, and records the plan in plan order."""

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.addCleanup(self._td.cleanup)
        self.root = os.path.realpath(self._td.name)

    def clone(self, conf: str) -> str:
        return gate_clone(self.root, conf)

    def order(self) -> List[str]:
        try:
            with open(os.path.join(self.root, "order.txt"), encoding="utf-8") as f:
                return [ln for ln in f.read().splitlines() if ln]
        except FileNotFoundError:
            return []

    def fb(self) -> FakeRuntime:
        return FakeRuntime(os.path.join(self.root, "box-tmp"))

    def test_the_groups_run_side_by_side_and_each_group_in_plan_order(self) -> None:
        d = self.clone(GROUPED_CONF)
        fb = self.fb()
        r = run_gate(d, fb, os.path.join(self.root, "logs"))
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertTrue(r.res.passed, r.progress + "\n" + "\n".join(self.order()))
        self.assertEqual([c.name for c in r.res.commands],
                         ["fast-1", "full-1", "full-2", "full-3", "full-4", "full-5", "full-6"],
                         "the result stays in plan order whatever finished first")
        steps = self.order()
        self.assertIn("overlap", steps, "the two groups never ran side by side")
        self.assertLess(steps.index("g1"), steps.index("g2"), "the go group's order is the plan's")
        self.assertLess(steps.index("p1"), steps.index("p2"), "the pi-web group's order is the plan's")
        self.assertLess(steps.index("fast"), steps.index("g1"), "the fast phase runs before the groups")
        self.assertLess(steps.index("head"), steps.index("g1"), "an ungrouped command before the block runs before the groups")
        self.assertGreater(steps.index("tail"), steps.index("g1"), "the trailing ungrouped command runs after the groups")
        self.assertGreater(steps.index("tail"), steps.index("p2"), "the trailing ungrouped command runs after the groups")

    def test_a_failure_without_keep_going_starts_nothing_new(self) -> None:
        """polyforge#244: with --keep-going off a failure starts nothing new,
        while a command already running finishes and is recorded. Both
        orderings are explicit, no fixed sleep: the failing command waits
        (bounded) until the other group's first command announced itself, and
        that command waits for the release file the test writes only after the
        gate printed the failure."""
        conf = ("stack=none\n"
                "full_command=%s\nfull_command=%s\nfull_command=%s\n"
                "group=go|full-1\ngroup=pi-web|full-2\ngroup=pi-web|full-3\n" % (FAILS, WAITS, ANNOUNCES))
        d = self.clone(conf)
        lines: List[str] = []
        g = new_gate(self.fb(), lines=lines, parallel=2)
        done: List[gate.GateResult] = []

        def run_gate_here() -> None:
            done.append(g.run(d, "", os.path.join(self.root, "logs")))

        def failed() -> bool:
            return any("full-1 FAIL" in ln for ln in lines)

        t = threading.Thread(target=run_gate_here)
        t.start()
        deadline = time.monotonic() + 120
        while not failed() and time.monotonic() < deadline:
            time.sleep(0.02)
        self.assertTrue(failed(), "the failing command never failed:\n" + "\n".join(lines))
        with open(os.path.join(self.root, "release"), "w", encoding="utf-8") as f:
            f.write("go\n")
        t.join(timeout=120)
        self.assertFalse(t.is_alive(), "the gate never finished")
        r = done[0]
        self.assertEqual(r.incomplete, "", "\n".join(lines))
        self.assertFalse(r.passed)
        self.assertEqual([c.not_run for c in r.commands], [False, False, True],
                         "only the pi-web group's second command never starts")
        self.assertEqual(r.commands[0].exit, 7)
        self.assertEqual((r.commands[1].exit, r.commands[1].not_run), (0, False),
                         "a command already running finishes and is recorded")
        self.assertFalse(os.path.exists(os.path.join(self.root, "p3.started")),
                         "a command the stop reached must not have started")

    def test_keep_going_runs_every_group_command(self) -> None:
        conf = ("stack=none\nfull_command=exit 7\nfull_command=printf 'g2\\n' >> ../order.txt\n"
                "full_command=printf 'p1\\n' >> ../order.txt\nfull_command=exit 9\n"
                "group=go|full-1\ngroup=go|full-2\ngroup=pi-web|full-3\ngroup=pi-web|full-4\n")
        d = self.clone(conf)
        r = run_gate(d, self.fb(), os.path.join(self.root, "logs"), keep_going=True)
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertEqual([c.not_run for c in r.res.commands], [False, False, False, False])
        self.assertEqual([c.exit for c in r.res.commands], [7, 0, 0, 9])
        self.assertEqual(sorted(self.order()), ["g2", "p1"], "every group command ran")

    def test_log_names_follow_the_plan_index_not_the_start_order(self) -> None:
        d = self.clone(GROUPED_CONF)
        par = run_gate(d, self.fb(), os.path.join(self.root, "logs-par"))
        ser = run_gate(d, self.fb(), os.path.join(self.root, "logs-ser"), parallel=1)
        self.assertTrue(par.res.passed, par.progress)
        self.assertTrue(ser.res.passed, ser.progress)
        self.assertEqual(log_names(par.res), log_names(ser.res),
                         "a command's log name is the plan's, not the scheduler's")
        self.assertEqual(len(set(log_names(par.res))), len(par.res.commands), "the log names are unique")

    def test_a_parallel_one_run_is_strictly_serial(self) -> None:
        d = self.clone(ORDER_CONF)
        r = run_gate(d, self.fb(), os.path.join(self.root, "logs"), parallel=1)
        self.assertTrue(r.res.passed, r.progress)
        self.assertEqual(self.order(), ["fast", "head", "g1", "g2", "p1", "p2", "tail"],
                         "at parallelism 1 the plan runs in order, group member by group member")

    def test_an_undeclared_repository_runs_serially_and_keeps_its_plan(self) -> None:
        d = self.clone(SERIAL_CONF)
        r = run_gate(d, self.fb(), os.path.join(self.root, "logs"))
        self.assertTrue(r.res.passed, r.progress)
        self.assertEqual(self.order(), ["fast", "one", "two"], "an undeclared repository runs in plan order")
        self.assertEqual(list(r.res.plan or {}), SERIAL_PLAN_KEYS, "the undeclared plan keeps its key set")
        for c in (r.res.plan or {})["commands"]:
            self.assertNotIn("group", c)
        self.assertEqual([os.path.basename(c.log) for c in r.res.commands], ["3.log", "4.log", "5.log"],
                         "the undeclared run's log numbering is the serial gate's")

    def test_each_group_gets_its_own_container_and_every_one_is_removed(self) -> None:
        d = self.clone(GROUPED_CONF)
        fb = self.fb()
        run_gate(d, fb, os.path.join(self.root, "logs"))
        self.assertEqual(len(fb.starts), 3, "the gate's own container and one per group")
        self.assertEqual(sorted(fb.removes), ["cid-1", "cid-2", "cid-3"], "every container is removed")
        group_boxes = {cid for cid, _, _, argv in fb.execs if "printf" in " ".join(argv)}
        self.assertEqual(len(group_boxes), 3, "every container ran a command of its own")

    def test_at_most_the_declared_parallelism_runs_at_once(self) -> None:
        """polyforge#244: three groups at full_parallel=2 never run more than
        two commands at once (r1#4: a semaphore that stopped bounding would
        show a peak of 3)."""
        d = self.clone(PEAK_CONF)
        r = run_gate(d, self.fb(), os.path.join(self.root, "logs"))
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertTrue(r.res.passed, r.progress)
        steps = self.order()
        peak = running = 0
        for ln in steps:
            if ln.startswith("start "):
                running += 1
                peak = max(peak, running)
            elif ln.startswith("end "):
                running -= 1
        self.assertEqual(peak, 2, "three groups at parallelism 2 ran %d at once: %s" % (peak, steps))
        self.assertEqual(len([ln for ln in steps if ln.startswith("end ")]), 3, "every group command ran: %s" % steps)

    def test_group_containers_write_no_log_the_gate_itself_owns(self) -> None:
        """polyforge#244 r1#1: a group's container must not overwrite the
        gate's own image build log or its mount-probe log (each group writes
        into a log directory of its own)."""
        d = self.clone(ORDER_CONF)
        logs = os.path.join(self.root, "logs")
        r = run_gate(d, self.fb(), logs, parallel=2)
        self.assertTrue(r.res.passed, r.progress)
        with open(os.path.join(logs, "image-build.log"), encoding="utf-8") as f:
            build = f.read()
        self.assertIn("Step 1/1", build, "the gate's own image-build.log was overwritten by a group container")
        self.assertNotIn("present", build, "a group container's 'image is present' note replaced the build log")
        with open(os.path.join(logs, "1.log"), encoding="utf-8") as f:
            probe = f.read()
        self.assertIn("cat ", probe, "the gate's own mount-probe log was overwritten by a group container")
        for n in ("1", "2"):
            own = os.path.join(logs, "group-%s" % n, "1.log")
            self.assertTrue(os.path.isfile(own), "group %s's container logs need a directory of their own (%s)" % (n, own))


if __name__ == "__main__":
    unittest.main()
