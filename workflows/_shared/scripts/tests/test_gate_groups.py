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

import unittest
from typing import Any, Dict, List

import gate
from pfstep import container, engine

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


if __name__ == "__main__":
    unittest.main()
