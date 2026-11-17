"""revert.py's record writes (workflow contract 1): each landed revert's
item moves to reverted as its whole record read through the step's hub,
under the lane's rules — the develop lane's keys kept, fenced on the record as read;
none for a record an earlier round already moved, a refusal for one the
rules do not move. (The reverts themselves run against git and a fake gh
in internal/polyforge/stepscripts.)"""

import json
import os
import unittest
from typing import Any, Dict

import revert
from pfstep.hub import HubError
from pfstep.integration import RuleError

from .test_integrate import FakeHub, wi


def details(*entries: revert.RevertEntry) -> revert.Details:
    return revert.Details("batch.json", "plan.json", list(entries), [])


def landed(wid: str, slug: str, **rec: Any) -> Dict[str, Any]:
    r: Dict[str, Any] = {"state": "landed", "merge_commits": {"app": ["m-" + wid]}, "repos": [{"repo": "app", "pr": "https://forge.invalid/pr/1"}]}
    r.update(rec)
    return wi(wid, slug=slug, attrs={"integration": r})


def entry(wid: str, name: str, *repos: revert.RevertRepo, done: bool = True) -> revert.RevertEntry:
    return revert.RevertEntry(wi=wid, name=name, group=[wid], repos=list(repos), done=done)


class RevertWritesTest(unittest.TestCase):
    def test_the_whole_record_moves_to_reverted(self) -> None:
        hub = FakeHub([landed("wi_src", "p#1"), landed("wi_old", "p#2", state="reverted", batch="wi_batch",
                                                       revert_merges={"app": "rm2"}, revert_prs=["pr2"])])
        d = details(entry("wi_src", "p#1", revert.RevertRepo("app", ["m"], "pr1", "rm1"), revert.RevertRepo("lib", ["l"], "pr1l", "rl1")),
                    entry("wi_none", "p#3"),  # nothing of it was on a default branch
                    entry("wi_open", "p#4", revert.RevertRepo("app", ["m"], "pr4", ""), done=False),
                    entry("wi_old", "p#2", revert.RevertRepo("app", ["m"], "pr2", "rm2")))  # an earlier round's, moved
        ops = revert.revert_writes(d, "wi_batch", hub)
        self.assertEqual(hub.calls, ["get wi_src", "get wi_old"])
        self.assertEqual(ops, [{"verb": "attrs", "target": "wi_src", "name": "integration", "if": {"integration": hub.items[0]["attrs"]["integration"]},
                                "value": {"state": "reverted", "batch": "wi_batch", "revert_prs": ["pr1", "pr1l"],
                                          "revert_merges": {"app": "rm1", "lib": "rl1"}, "merge_commits": {"app": ["m-wi_src"]},
                                          "repos": [{"repo": "app", "pr": "https://forge.invalid/pr/1"}]}}])

    def test_refusals(self) -> None:
        d = details(entry("wi_src", "p#1", revert.RevertRepo("app", ["m"], "pr1", "rm1")))
        for name, items, want in (
            ("merged by another batch", [landed("wi_src", "p#1", state="merged", green_tag="green/20261101-1")], "INTEGRATION_FENCE"),
            ("not wrapped", [wi("wi_src", status="running", slug="p#1", attrs={"integration": {"state": "landed"}})], "NOT_WRAPPED"),
            ("no hand-off", [wi("wi_src", slug="p#1")], "NO_HANDOFF"),
        ):
            with self.subTest(name):
                with self.assertRaises(RuleError) as cm:
                    revert.revert_writes(d, "wi_batch", FakeHub(items))
                self.assertEqual(cm.exception.reason, want)
        with self.assertRaises(HubError):
            revert.revert_writes(d, "wi_batch", FakeHub([]))


if __name__ == "__main__":
    unittest.main()


class RepairPlanTest(unittest.TestCase):
    """revert.py's --repair input (the repair step's plan file): a missing
    file is a step that never ran (no fix), a malformed one is refused
    naming the entry, and a fix whose blamed item a revert of the same
    run carries is not landed."""

    def write(self, doc: Any) -> str:
        import tempfile
        fd, path = tempfile.mkstemp(suffix=".json")
        with os.fdopen(fd, "w") as f:
            json.dump(doc, f)
        return path

    def test_a_missing_file_is_a_step_that_never_ran(self) -> None:
        self.assertEqual(revert.read_repair("no-such-repair-plan.json"), [])

    def test_the_plan_reads(self) -> None:
        p = self.write({"round": 1, "repos": [{"name": "app", "branch": "integrate/repair-b",
                                               "base": "pin", "head": "head", "units": ["TestStale"], "items": ["wi_src"]}]})
        fixes = revert.read_repair(p)
        self.assertEqual(len(fixes), 1)
        f = fixes[0]
        self.assertEqual((f.name, f.branch, f.base, f.head, f.units, f.items, f.pr, f.merge, f.why),
                         ("app", "integrate/repair-b", "pin", "head", ["TestStale"], ["wi_src"], "", "", ""))

    def test_a_malformed_plan_is_refused(self) -> None:
        for name, doc in (
            ("not an object", []),
            ("no repos", {"round": 1}),
            ("repos not a list", {"round": 1, "repos": {}}),
            ("an entry not an object", {"round": 1, "repos": ["app"]}),
            ("no name", {"round": 1, "repos": [{"branch": "b", "units": ["u"], "items": ["wi"]}]}),
            ("no branch", {"round": 1, "repos": [{"name": "app", "units": ["u"], "items": ["wi"]}]}),
            ("no units", {"round": 1, "repos": [{"name": "app", "branch": "b", "items": ["wi"]}]}),
            ("no items", {"round": 1, "repos": [{"name": "app", "branch": "b", "units": ["u"]}]}),
            ("a repo twice", {"round": 1, "repos": [{"name": "app", "branch": "b", "units": ["u"], "items": ["wi"]},
                                                    {"name": "app", "branch": "b2", "units": ["u"], "items": ["wi"]}]}),
            ("not JSON", "not json"),
        ):
            with self.subTest(name):
                if name == "not JSON":
                    import tempfile
                    fd, path = tempfile.mkstemp(suffix=".json")
                    with os.fdopen(fd, "w") as f:
                        f.write("not json")
                else:
                    path = self.write(doc)
                with self.assertRaises(revert._Bad):
                    revert.read_repair(path)

    def test_a_fix_of_a_reverted_item_is_blamed(self) -> None:
        f = revert.FixRepo(name="app", branch="b", units=["u"], items=["wi_src", "wi_2"])
        self.assertTrue(revert.fix_blamed(f, {"wi_src"}))
        self.assertFalse(revert.fix_blamed(f, {"wi_other"}))
        self.assertFalse(revert.fix_blamed(f, set()))
