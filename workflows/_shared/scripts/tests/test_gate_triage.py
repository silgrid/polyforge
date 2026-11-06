"""gate.py --triage's library: the gate's container held open on a red
clone (its failing units located with their targeted re-runs, probed one
at a time), the pre-existing rule, a unit's candidates by area, the
bisection, the baseline, the attribution without a re-run, and the pool
read through the step's hub. The Go side's twins were verifyrun's
w404_gate_session_test.go and integrate's attribute_test.go,
bisect_test.go, merges_test.go (the baseline) and w404_triage_rules_test.go;
the command end to end is internal/polyforge/stepscripts' gate_test.go and
w404_rules_test.go."""

import json
import os
import tempfile
import unittest
from typing import Any, Dict, List

import gate
from pfstep.engine import EngineReport
from pfstep.preexisting import ABSENT, PRESENT, UNKNOWN

from .fakebox import FakeRuntime
from .gatefix import HAVE_BASH, HAVE_GIT, HAVE_PY, commit_all, gate_clone, git_t, new_gate, run_gate, write_files

E2E = """#!/bin/sh
if [ $# -eq 0 ]; then
  echo "===== PASS e2e/a.mjs (1s) ====="
  echo "===== failures ====="
  echo "FAIL e2e/b.mjs: expected 1 got 2"
  exit 1
fi
if [ -e "$PWD/.fail-b" ]; then echo "===== failures ====="; echo "FAIL $1: still red"; exit 1; fi
echo "===== PASS $1 (1s) ====="
"""


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class SessionTest(unittest.TestCase):
    def test_a_failing_suite_reruns_by_its_template(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            root = os.path.realpath(td)
            d = gate_clone(root, "stack=none\nfull_command=sh run-e2e.sh\nrerun=full-1|sh run-e2e.sh {unit}\n")
            write_files(d, {"run-e2e.sh": E2E, "e2e/b.mjs": "// carries: web/Panel*\nconsole.log(1)\n"})
            commit_all(d, "e2e")
            fb = FakeRuntime(os.path.join(root, "box-tmp"))
            r = run_gate(d, fb, os.path.join(root, "logs"))
            self.assertFalse(r.res.passed)
            self.assertEqual(r.res.incomplete, "", r.progress)
            g = new_gate(fb)
            s = gate.Session.open(g, d, "app", os.path.join(root, "triage"))
            try:
                us = s.units(r.res)
                self.assertEqual([(u.kind, u.name, u.file) for u in us], [("e2e-suite", "e2e/b.mjs", "e2e/b.mjs")])
                self.assertEqual((us[0].rerun, us[0].carries), ("sh run-e2e.sh e2e/b.mjs", ["web/Panel*"]))
                self.assertEqual(us[0].timeout, gate.DEFAULT_COMMAND_TIMEOUT)
                p = s.probe(us[0])
                self.assertEqual((p.passed, p.presence), (True, PRESENT), "the re-run passes: flaky")
                write_files(d, {".fail-b": "x\n"})
                p = s.probe(us[0])
                self.assertEqual((p.passed, p.failed), (False, True), "still red stays red")
                rep = s.select("HEAD~1")
                self.assertIsInstance(rep, EngineReport)
            finally:
                s.close()
            self.assertEqual(fb.removes, ["cid-1", "cid-2"], "the session removes its container")

    def test_a_command_whose_output_locates_nothing_is_one_unit_rerun_whole(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            root = os.path.realpath(td)
            d = gate_clone(root, "stack=none\nfull_command=echo nothing to see; exit 5\n")
            fb = FakeRuntime(os.path.join(root, "box-tmp"))
            r = run_gate(d, fb, os.path.join(root, "logs"))
            s = gate.Session.open(new_gate(fb), d, "app", os.path.join(root, "triage"))
            try:
                us = s.units(r.res)
                self.assertEqual([(u.kind, u.name, u.rerun) for u in us], [("command", "app:full-1", "echo nothing to see; exit 5")])
                self.assertIn("nothing to see", us[0].excerpt)
                p = s.probe(us[0])
                self.assertEqual((p.passed, p.failed), (False, True))
            finally:
                s.close()


class PreExistingRuleTest(unittest.TestCase):
    def test_pre_existing_is_the_verifications_rule(self) -> None:
        u = gate.GateUnit(command="full-1", kind="go", name="TestX", file="x_test.go", signature="x_test.go: want 1 got 2")
        head = gate.UnitProbe(presence=PRESENT, failed=True, signature=u.signature)
        for name, h, b, pre in (
            ("same failure at the baseline", head, gate.UnitProbe(presence=PRESENT, failed=True, signature=u.signature), True),
            ("absent at the baseline", head, gate.UnitProbe(presence=ABSENT, presence_why="x_test.go does not exist"), False),
            ("passes at the baseline", head, gate.UnitProbe(presence=PRESENT, passed=True), False),
            ("fails differently", head, gate.UnitProbe(presence=PRESENT, failed=True, signature="other"), False),
            ("no existence proof", head, gate.UnitProbe(presence=UNKNOWN, failed=True, signature=u.signature), False),
            ("head fails otherwise", gate.UnitProbe(presence=PRESENT, failed=True, signature="other"),
             gate.UnitProbe(presence=PRESENT, failed=True, signature="other"), False),
        ):
            with self.subTest(name):
                got, why = gate.gate_pre_existing(u, h, b)
                self.assertEqual(got, pre, why)


def unit(kind: str, name: str, file: str = "", carries: Any = None) -> gate.GateUnit:
    return gate.GateUnit(command="c", kind=kind, name=name, file=file, carries=list(carries or []))


class AttributionTest(unittest.TestCase):
    def test_candidates_by_area_and_the_engines_selection(self) -> None:
        changes = [
            gate.Change(item="i1", slug="", repo="r", merge="m1", paths=["internal/a/a.go"]),
            gate.Change(item="i2", slug="", repo="r", merge="m2", paths=["internal/lib/x.go"],
                        selection=gate.Selection(commands=["go test ./internal/lib/... ./internal/b"])),
            gate.Change(item="i3", slug="", repo="r", merge="m3", paths=["web/Panel.tsx"]),
        ]
        cs = gate.candidates(unit("go", "TestB", "internal/b/b_test.go"), "r", changes)
        self.assertEqual([c.item for c in cs], ["i2"])
        self.assertIn("scope engine", cs[0].why)
        self.assertEqual([c.item for c in gate.candidates(unit("e2e-suite", "e2e/panel.mjs", "e2e/panel.mjs", ["web/Panel*"]), "r", changes)], ["i3"])
        self.assertEqual([c.item for c in gate.candidates(unit("go", "TestA", "internal/a/a_test.go"), "r", changes)], ["i1"])
        self.assertEqual(gate.candidates(unit("go", "TestZ", "internal/z/z_test.go"), "r", changes), [])
        self.assertEqual(gate.candidates(unit("go", "TestA", "internal/a/a_test.go"), "other", changes), [], "another repo's changes")
        full = [gate.Change(item="i9", slug="", repo="r", merge="m9", paths=["README"], selection=gate.Selection(full=True))]
        self.assertIn("full tier", gate.candidates(unit("go", "TestZ", "internal/z/z_test.go"), "r", full)[0].why)

    def test_candidates_name_each_merge_of_an_item(self) -> None:
        changes = [
            gate.Change(item="i1", slug="", repo="r", merge="m1", paths=["internal/a/a.go"]),
            gate.Change(item="i2", slug="", repo="r", merge="m2", paths=["docs/x.md"]),
            gate.Change(item="i1", slug="", repo="r", merge="m3", paths=["internal/a/b.go"]),
            gate.Change(item="i1", slug="", repo="r", merge="m4", paths=["README"]),
        ]
        cs = gate.candidates(unit("go", "TestA", "internal/a/a_test.go"), "r", changes)
        self.assertEqual(len(cs), 1)
        self.assertEqual([m for m, _ in cs[0].within], ["m1", "m3"])
        self.assertIn("internal/a/b.go", cs[0].within[1][1])
        self.assertEqual(cs[0].why, cs[0].within[0][1])
        self.assertEqual(cs[0].why_at("m4"), cs[0].why + " (at another merge of it)")

    def test_package_dirs_and_named_packages(self) -> None:
        self.assertEqual(gate.pkg_dir("github.com/o/r/internal/x"), "internal/x")
        self.assertEqual(gate.pkg_dir("./internal/x"), "internal/x")
        self.assertEqual(gate.pkg_dir("."), "")
        self.assertEqual(gate.unit_dir("go-pkg", "example.invalid/demo/pkg", "", ""), "example.invalid/demo/pkg")
        self.assertTrue(gate.names_package("go test ./internal/...", "internal/b"))
        self.assertTrue(gate.names_package("go test ./...", "internal/b"))
        self.assertTrue(gate.names_package("go test './internal/b/'", "internal/b"))
        self.assertFalse(gate.names_package("go test ./internal/bb", "internal/b"))
        self.assertFalse(gate.names_package("go test ./web/...", "internal/b"))

    def test_the_bisection_tests_both_ends(self) -> None:
        commits = ["c1", "c2", "c3", "c4", "head"]

        def fail_from(first: str) -> Any:
            return lambda c: c in commits and commits.index(c) >= commits.index(first)

        self.assertEqual(gate.bisect("base", commits, fail_from("c3")), 2)
        self.assertEqual(gate.bisect("base", commits, fail_from("head")), 4)
        self.assertEqual(gate.bisect("base", commits, lambda c: True), -1, "red at the base predates the window")
        tested: List[str] = []

        def never(c: str) -> bool:
            tested.append(c)
            return False

        with self.assertRaises(gate.TriageError):
            gate.bisect("base", commits, never)
        self.assertIn("head", tested, "the red head was run")
        with self.assertRaises(gate.TriageError):
            gate.bisect("base", [], never)

    def test_root_of(self) -> None:
        items = [{"id": "wi_src", "slug": "p#1", "integration": {"fix_tickets": ["p#7"]}},
                 {"id": "wi_fix", "slug": "p#7", "integration": {}}]
        self.assertEqual(gate.root_of("wi_fix", items), "wi_src", "a fix ticket's source")
        self.assertEqual(gate.root_of("wi_src", items), "wi_src")


@unittest.skipUnless(HAVE_GIT, "git needed")
class BaselineTest(unittest.TestCase):
    def test_the_baseline_is_the_oldest_merges_parent_or_the_newest_green_tag(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            d = os.path.join(td, "r")
            os.makedirs(d)
            git_t(d, "init", "-q", "-b", "main")
            write_files(d, {"README": "base\n"})
            commit_all(d, "base")

            def merge(name: str) -> str:
                git_t(d, "checkout", "-q", "-b", "feat-" + name)
                write_files(d, {name + ".txt": name + "\n"})
                commit_all(d, name)
                git_t(d, "checkout", "-q", "main")
                git_t(d, "merge", "-q", "--no-ff", "-m", "Merge " + name, "feat-" + name)
                return git_t(d, "rev-parse", "HEAD")

            m1 = merge("m1")
            write_files(d, {"human.txt": "x\n"})
            commit_all(d, "a human commit between the merges")
            m2 = merge("m2")
            parent = git_t(d, "rev-parse", m1 + "^1")
            g = gate.Git()
            head = git_t(d, "rev-parse", "HEAD")
            for name, merges in (("in merge order", [m1, m2]), ("listed newest first", [m2, m1])):
                with self.subTest(name):
                    rev, label = gate.triage_baseline(g, d, head, merges)
                    self.assertEqual(rev, parent)
                    self.assertIn(gate.short(m1), label)
            git_t(d, "tag", "-a", "-m", "{}", "green/20261102-1", m1)
            self.assertEqual(gate.triage_baseline(g, d, head, [m2]), ("refs/tags/green/20261102-1", "green/20261102-1"))
            e = os.path.join(td, "e")
            os.makedirs(e)
            git_t(e, "init", "-q", "-b", "main")
            write_files(e, {"README": "x\n"})
            commit_all(e, "x")
            self.assertEqual(gate.triage_baseline(g, e, "HEAD", []), ("", ""), "no tag and no merge: the history is bisected")


class NoRerunSession:
    """A gate session whose failing unit has no targeted re-run and whose
    engine selects nothing."""

    def units(self, res: gate.GateResult) -> List[gate.GateUnit]:
        return [gate.GateUnit(command="full-1", kind="go", name="TestBroken", note="no targeted re-run")]

    def probe(self, u: gate.GateUnit) -> gate.UnitProbe:
        raise gate.TriageError("no targeted re-run")

    def select(self, base: str) -> EngineReport:
        raise gate.TriageError("no engine")


@unittest.skipUnless(HAVE_GIT, "git needed")
class TriageRulesTest(unittest.TestCase):
    def test_without_a_rerun_only_the_windows_only_commit_is_blamed(self) -> None:
        for human in (False, True):
            with self.subTest("beside a human commit" if human else "the only commit"), tempfile.TemporaryDirectory() as td:
                d = os.path.join(td, "app")
                os.makedirs(d)
                git_t(d, "init", "-q", "-b", "main")
                write_files(d, {"README": "base\n"})
                commit_all(d, "base")
                git_t(d, "tag", "-a", "-m", "{}", "green/20261101-1")
                if human:
                    write_files(d, {"human.txt": "x\n"})
                    commit_all(d, "a human commit")
                git_t(d, "checkout", "-q", "-b", "feat-r")
                write_files(d, {"broken": "x\n"})
                commit_all(d, "the item")
                git_t(d, "checkout", "-q", "main")
                git_t(d, "merge", "-q", "--no-ff", "-m", "Merge wi_r", "feat-r")
                merge = git_t(d, "rev-parse", "HEAD")
                items: List[Dict[str, Any]] = [{"id": "wi_r", "slug": "p#1", "integration": {"state": "landed", "merge_commits": {"app": [merge]}}}]
                units, base = gate.triage(gate.Git(), NoRerunSession(), gate.RedRepo(
                    repo="app", dir=d, head=merge, result=gate.GateResult(), batch_merges=[merge], items=items))
                self.assertEqual(base, "green/20261101-1")
                self.assertEqual(len(units), 1)
                u = units[0]
                if human:
                    self.assertEqual((u.item, u.klass()), ("", gate.CLASS_UNATTRIBUTED), u)
                else:
                    self.assertEqual(u.item, "wi_r", u)
                    self.assertIn("the only commit", u.why)
                self.assertEqual(git_t(d, "rev-parse", "HEAD"), merge, "the clone is left at the red head")


FAKE_PF = """#!/bin/sh
# a fake pf: `pf --json step hub list ...` answers $PF_FAKE_HUB, or refuses
echo "$*" >> "$PF_FAKE_LOG"
if [ -n "${PF_FAKE_REFUSE:-}" ]; then
  echo 'pf: refused' >&2
  echo '{"code":"daemon.hub_unavailable","detail":"aihub unreachable: dial tcp"}' >&2
  exit 1
fi
cat "$PF_FAKE_HUB"
"""


class HubPoolTest(unittest.TestCase):
    def test_the_pool_read_through_the_steps_hub(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            bindir = os.path.join(td, "bin")
            write_files(bindir, {"pf": FAKE_PF}, 0o755)
            hub = os.path.join(td, "hub.json")
            with open(hub, "w", encoding="utf-8") as f:
                json.dump({"items": [{"id": "wi_a", "slug": "p#1", "status": "wrapped", "closed_at": "2026-11-10T00:00:00Z",
                                      "attrs": {"integration": {"state": "landed", "merge_commits": {"app": ["m1"]}}}},
                                     {"id": "wi_b", "slug": "", "status": "wrapped", "attrs": {}}]}, f)
            log = os.path.join(td, "calls.log")
            env = {"PATH": bindir + os.pathsep + "/usr/bin:/bin", "PF_FAKE_HUB": hub, "PF_FAKE_LOG": log}
            items = gate.hub_landed_items(env)
            self.assertEqual([(it["id"], it["integration"].get("merge_commits")) for it in items],
                             [("wi_a", {"app": ["m1"]}), ("wi_b", None)])
            with open(log, encoding="utf-8") as f:
                self.assertEqual(f.read().strip(), "--json step hub list --status wrapped --attr integration.state=landed --limit 1000")
            env["PF_FAKE_REFUSE"] = "1"
            with self.assertRaises(gate.TriageError) as cm:
                gate.hub_landed_items(env)
            self.assertIn("aihub unreachable", str(cm.exception))
            self.assertIn("through the step's hub", str(cm.exception))
            with self.assertRaises(gate.TriageError):
                gate.hub_landed_items({"PATH": os.path.join(td, "none")})

    def test_a_pool_file(self) -> None:
        with tempfile.TemporaryDirectory() as td:
            p = os.path.join(td, "pool.json")
            with open(p, "w", encoding="utf-8") as f:
                json.dump({"items": [{"id": "wi_a", "integration": {"state": "landed"}}], "red_heads": {}}, f)
            self.assertEqual([it["id"] for it in gate.read_pool_file(p)], ["wi_a"])
            with open(p, "w", encoding="utf-8") as f:
                f.write("[]")
            with self.assertRaises(gate.TriageError):
                gate.read_pool_file(p)


if __name__ == "__main__":
    unittest.main()
