# carries: workflows/_shared/scripts/verify.py, workflows/_shared/scripts/tests/verifyfix.py
"""verify.py's tier: the line's --tier auto reads the work item's attrs —
an integration_fix ticket (the marker an integration batch's full-gate
writes) runs the FULL bar (the engine asked --full, its fast and full
commands; the whole tool window is the budget), every other ticket the
light one; light and full are the hand run's explicit overrides, and the
result names the requested tier, the effective one and why."""

import json
import os
import tempfile
import unittest
from typing import Any, Dict, Tuple

import verify
from pfstep.engine import TIER_LIGHT

from .verifyfix import (
    HAVE_BASH,
    HAVE_GIT,
    Fixture,
    Run,
    cmd_of,
    engine_args,
    fake_engine,
    report,
    run_verify,
)


def work_item(fx: Fixture, attrs: Dict[str, Any]) -> None:
    """The capsule's materialized work item with attrs."""
    d = os.path.join(fx.capsule, "runtime-state")
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "work-item.json"), "w", encoding="utf-8") as f:
        f.write(json.dumps({"id": "wi_fix1", "slug": "app#9", "project": "app", "goal": "fix",
                           "content": "## Acceptance\n- the gate is green\n", "attrs": attrs}) + "\n")


def tier_fixture(root: str, full_cmd: str = "echo full-ok") -> Tuple[Fixture, str]:
    """The app repo with a change; the fake engine answers a scoped light
    report and a full one (fast plus full tail)."""
    fx = Fixture(root, {"app": {"lib/a.txt": "a\n"}})
    fx.commit_in_repo("app", {"lib/a.txt": "a b\n"}, "feature")
    eng = fake_engine(root, {
        "app": report("scoped", cmd_of("gate", "fast", "echo plan-ok")),
        "app.full": report("full", cmd_of("gate", "fast", "echo plan-ok"),
                           cmd_of("tail", "full", full_cmd)),
    })
    return fx, eng


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are required")
class TiersTest(unittest.TestCase):
    def setUp(self) -> None:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.root = os.path.realpath(td.name)

    def doc(self, r: Run) -> Dict[str, Any]:
        self.assertIsNotNone(r.doc, "no result.json: %s\n%s" % (json.dumps(r.result, indent=1), r.stderr))
        assert r.doc is not None
        return r.doc

    def test_auto_without_the_marker_is_light(self) -> None:
        fx, eng = tier_fixture(self.root)
        work_item(fx, {})
        r = run_verify(fx.capsule, eng, tier="auto")
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(self.doc(r)["tier"]["effective"], "light")
        self.assertTrue(any(("--light" in ln and "--full" not in ln) for ln in engine_args(eng)),
                        engine_args(eng))
        self.assertEqual(r.check("app:gate")["status"], "pass")

    def test_auto_with_the_marker_runs_the_full_bar(self) -> None:
        fx, eng = tier_fixture(self.root)
        work_item(fx, {"integration_fix": True})
        r = run_verify(fx.capsule, eng, tier="auto")
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        d = self.doc(r)
        self.assertEqual(d["tier"]["requested"], "auto")
        self.assertEqual(d["tier"]["effective"], "full")
        self.assertTrue(any("integration_fix" in why for why in d["tier"].get("reasons", [])),
                        d["tier"])
        # the engine was asked for the full plan and the full tail ran
        self.assertTrue(any(("--full" in ln and "--light" not in ln) for ln in engine_args(eng)),
                        engine_args(eng))
        self.assertEqual(r.check("app:gate")["status"], "pass")
        self.assertEqual(r.check("app:tail")["status"], "pass")
        self.assertIn("full verification passed", r.result.get("summary", ""))

    def test_explicit_full_without_the_marker(self) -> None:
        fx, eng = tier_fixture(self.root)
        work_item(fx, {})
        r = run_verify(fx.capsule, eng, tier="full")
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(self.doc(r)["tier"]["effective"], "full")
        self.assertEqual(r.check("app:tail")["status"], "pass")

    def test_explicit_light_overrides_the_marker(self) -> None:
        fx, eng = tier_fixture(self.root)
        work_item(fx, {"integration_fix": True})
        r = run_verify(fx.capsule, eng, tier="light")
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(self.doc(r)["tier"]["effective"], "light")
        self.assertTrue(any(("--light" in ln and "--full" not in ln) for ln in engine_args(eng)),
                        engine_args(eng))

    def test_a_string_marker_does_not_switch(self) -> None:
        """The marker is the boolean the batch writes, never any string: a
        string value (even a truthy-looking one) leaves the ticket light."""
        for name, attrs in (("a string", {"integration_fix": "yes"}),
                            ("a number", {"integration_fix": 1}),
                            ("false", {"integration_fix": False})):
            with self.subTest(name):
                d = os.path.join(self.root, name.replace(" ", "-"))
                os.makedirs(d)
                fx, eng = tier_fixture(d)
                work_item(fx, attrs)
                r = run_verify(fx.capsule, eng, tier="auto")
                self.assertEqual(r.exit, 0, r.result.get("summary"))
                self.assertEqual(self.doc(r)["tier"]["effective"], "light", self.doc(r)["tier"])

    def test_no_work_item_doc_is_light(self) -> None:
        fx, eng = tier_fixture(self.root)
        r = run_verify(fx.capsule, eng, tier="auto")
        self.assertEqual(r.exit, 0, r.result.get("summary"))
        self.assertEqual(self.doc(r)["tier"]["effective"], "light")

    def test_an_unknown_tier_is_refused(self) -> None:
        fx, eng = tier_fixture(self.root)
        r = run_verify(fx.capsule, eng, tier="scoped")
        self.assertEqual(r.exit, 2, r.result.get("summary"))
        self.assertIn("light, full or auto", r.result.get("summary", ""))

    def test_a_target_branch_file_changes_no_tier(self) -> None:
        """A capsule's fixed target branch never enters the tier
        resolution (polyforge#88, AC5): --tier auto reads the work item's
        attrs exactly as before, the target-branch file valid or not —
        the sub-ticket runs the light tier like any other ticket."""
        d = os.path.join(self.root, "tb-capsule")
        os.makedirs(os.path.join(d, "runtime-state"), exist_ok=True)
        with open(os.path.join(d, "runtime-state", "work-item.json"), "w", encoding="utf-8") as f:
            f.write(json.dumps({"id": "wi_sub", "slug": "app#2", "project": "app", "goal": "sub",
                               "content": "## Acceptance\n- the gate is green\n", "attrs": {}}) + "\n")
        for body in ('{"target": "epic/1", "parent": "epic#1"}', "{not json"):
            with self.subTest(body):
                with open(os.path.join(d, "runtime-state", "target-branch.json"), "w", encoding="utf-8") as f:
                    f.write(body)
                self.assertEqual(verify.resolve_tier(d, "auto"), (TIER_LIGHT, ""), body)


if __name__ == "__main__":
    unittest.main()
