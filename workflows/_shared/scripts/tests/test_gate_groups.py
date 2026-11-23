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
import unittest
from typing import Any, Dict, List

import gate
from pfstep import container, engine

from .fakebox import FakeRuntime
from .gatefix import HAVE_BASH, HAVE_GIT, HAVE_PY, boxed_bash, gate_clone, run_gate

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
        conf = ("stack=none\nfull_command=exit 7\nfull_command=printf 'g2\\n' >> ../order.txt\n"
                "full_command=sleep 0.4; printf 'p1\\n' >> ../order.txt\n"
                "full_command=printf 'p2\\n' >> ../order.txt\n"
                "group=go|full-1\ngroup=go|full-2\ngroup=pi-web|full-3\ngroup=pi-web|full-4\n")
        d = self.clone(conf)
        fb = self.fb()
        r = run_gate(d, fb, os.path.join(self.root, "logs"))
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertFalse(r.res.passed)
        self.assertEqual([c.not_run for c in r.res.commands], [False, True, False, True],
                         "the failing group's second command and the other group's second command never start")
        self.assertEqual(r.res.commands[0].exit, 7)
        self.assertEqual((r.res.commands[2].exit, r.res.commands[2].not_run), (0, False),
                         "a command already running finishes and is recorded")

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
        self.assertEqual(sorted(fb.removes), sorted(c for c in fb.containers) or ["cid-1", "cid-2", "cid-3"],
                         "every container is removed")
        group_boxes = {cid for cid, _, _, _ in fb.execs if "printf" in " ".join(_)}
        self.assertEqual(len(group_boxes), 3, "the group commands run in the group containers too")


if __name__ == "__main__":
    unittest.main()
