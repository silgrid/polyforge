"""revert.py's record writes (workflow contract 1): each landed revert's
item moves to reverted as its whole record read through the step's hub,
under the lane's rules — the develop lane's keys kept, fenced on the record as read;
none for a record an earlier round already moved, a refusal for one the
rules do not move. (The reverts themselves run against git and a fake gh
in internal/polyforge/stepscripts.)"""

import unittest
from typing import Any, Dict

import revert
from pfstep.hub import HubError
from pfstep.integration import RuleError

from .test_integrate import FakeHub, wi


def details(*entries: revert.RevertEntry) -> revert.Details:
    return revert.Details("batch.json", "plan.json", list(entries))


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
