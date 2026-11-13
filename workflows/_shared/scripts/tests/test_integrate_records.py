"""integrate.py records (workflow contract 5): a step's decisions on the
integration records — collect's fix and clear rulings, full-gate's
attributions — become the hub writes it registers, each the whole record
built under the lane's rules from the record the step's hub reads now; a
rule's refusal writes nothing (exit 1), an unreadable input or hub is not
judged (exit 2)."""

import json
import os
import tempfile
import unittest
from typing import Any, Dict, List, Optional

import integrate
from pfstep.hub import HubError

from .test_integrate import FakeHub, wi

BATCH = "wi_batch"
OWNER_AT = "2026-11-11T10:00:00Z"
WORDS = "wi_h 的红是环境问题，清掉它。\n  原样照抄。"


def held_item(wid: str, slug: str, **rec: Any) -> Dict[str, Any]:
    r: Dict[str, Any] = {"state": "landed", "repos": [{"repo": "app", "pr": "https://forge.invalid/pr/1", "head": "h"}],
                         "merge_commits": {"app": ["m-" + wid]}}
    r.update(rec)
    return wi(wid, slug=slug, attrs={"integration": r, "keep": "me"})


class RecordsCase(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = tempfile.mkdtemp(prefix="pfstep-records-")
        self.addCleanup(lambda: __import__("shutil").rmtree(self.dir, ignore_errors=True))
        self.write("capsule.json", {"work_item_id": BATCH, "project": "p", "bundle_name": "integrate"})
        self.hub = FakeHub([
            held_item("wi_h", "p#2", red_rounds=1, fix_tickets=["p#9"]),
            held_item("wi_n", "p#3"),
            held_item("wi_m", "p#4", state="merged", green_tag="green/20261101-1"),
            wi("wi_q", status="running", slug="p#5", attrs={"integration": {"state": "landed", "red_rounds": 1}}),
        ], batches=[wi("wi_old", status="wrapped", slug="p#50"), wi(BATCH, status="running", slug="p#51")])
        self.asks([{"at": "2026-11-11T09:00:00Z", "step": "collect", "spawn": 1, "kind": "ask", "text": "清还是修？"},
                   {"at": OWNER_AT, "step": "collect", "spawn": 1, "kind": "reply", "text": WORDS, "by": "u_alice"},
                   {"at": "2026-11-11T10:05:00Z", "step": "close", "spawn": 1, "kind": "message", "text": "another step's"}])

    def write(self, rel: str, doc: Any) -> str:
        p = os.path.join(self.dir, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            json.dump(doc, f)
        return p

    def asks(self, lines: List[Dict[str, Any]]) -> None:
        p = os.path.join(self.dir, "runtime-state", "asks.jsonl")
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write("".join(json.dumps(x) + "\n" for x in lines) + '{"at": "torn')

    def run_records(self, entries: Any, *extra: str, env: Optional[Dict[str, str]] = None) -> "tuple[int, Optional[List[Dict[str, Any]]], Dict[str, Any]]":
        inp = self.write("in.json", entries)
        ops = os.path.join(self.dir, "ops.json")
        out = os.path.join(self.dir, "records.json")
        environ = {"PATH": os.environ.get("PATH", ""), "PF_WI_ID": BATCH, "PF_STEP": "collect"} if env is None else env
        code = integrate.main(["records", "--in", inp, "--ops-out", ops, "--capsule", self.dir, "--out", out] + list(extra), environ=environ, hub=self.hub)
        got: Optional[List[Dict[str, Any]]] = None
        if os.path.exists(ops):
            with open(ops) as f:
                got = json.load(f)
        res: Dict[str, Any] = {}
        if os.path.exists(out):
            with open(out) as f:
                res = json.load(f)
        return code, got, res


class RecordsFixAndClearTest(RecordsCase):
    def test_fix_and_clear(self) -> None:
        self.hub.items[0], self.hub.items[1] = held_item("wi_h", "p#2", red_rounds=1, fix_tickets=["p#9"]), held_item("wi_n", "p#3", red_rounds=2)
        ticket = {"verb": "file", "key": "hold-fix/wi_n/0", "lane": "develop", "goal": "fix it", "content": "c", "priority": "high"}
        code, ops, res = self.run_records([ticket, {"item": "p#3", "fix": ["key:hold-fix/wi_n/0"]}, {"item": "wi_h", "clear": OWNER_AT}])
        self.assertEqual(code, 0, res.get("summary"))
        assert ops is not None
        self.assertEqual(ops[0], ticket, "a file write is passed on as written, in its place")
        fix = ops[1]
        self.assertEqual((fix["verb"], fix["target"], fix["name"], fix["if"]), ("attrs", "wi_n", "integration", {"integration": self.hub.items[1]["attrs"]["integration"]}))
        self.assertEqual(fix["value"]["fix_tickets"], ["key:hold-fix/wi_n/0"])
        self.assertEqual(fix["value"]["red_rounds"], 2)
        self.assertEqual(fix["value"]["merge_commits"], {"app": ["m-wi_n"]}, "the develop lane's keys kept")
        self.assertNotIn("per_owner", fix)
        clear = ops[2]
        self.assertEqual((clear["target"], clear["per_owner"]), ("wi_h", OWNER_AT))
        rec = clear["value"]
        self.assertEqual(rec["state"], "merged")
        self.assertEqual(rec["fix_tickets"], ["p#9"])
        hc = rec["hold_cleared"]
        self.assertEqual({k: hc[k] for k in ("reason", "by", "answered_by", "answered_at", "cleared_in")},
                         {"reason": WORDS, "by": "u_alice", "answered_by": "u_alice", "answered_at": OWNER_AT, "cleared_in": BATCH})
        self.assertTrue(hc["at"])
        self.assertEqual(res["details"]["writes"], [{"item": "wi_n", "kind": "fix", "state": "landed"}, {"item": "wi_h", "kind": "clear", "state": "merged"}])
        self.assertIn("list --bundle integrate --limit 200", self.hub.calls, "the clear read the lane's batches")

    def test_a_fix_adds_to_the_holds_tickets(self) -> None:
        code, ops, res = self.run_records([{"item": "wi_h", "fix": ["p#9", "key:hold-fix/wi_h/1"]}])
        self.assertEqual(code, 0, res.get("summary"))
        assert ops is not None
        self.assertEqual(ops[0]["value"]["fix_tickets"], ["p#9", "key:hold-fix/wi_h/1"])

    def test_refusals_write_nothing(self) -> None:
        for name, entries, reason in (
            ("a fix on no hold", [{"item": "wi_n", "fix": ["p#10"]}], "NOT_HELD"),
            ("a clear of no hold", [{"item": "wi_n", "clear": OWNER_AT}], "NOT_HELD"),
            ("a clear of a merged record", [{"item": "wi_m", "clear": OWNER_AT}], "NOT_HELD"),
            ("a fix on a work item not wrapped", [{"item": "wi_q", "fix": ["p#10"]}], "NOT_HELD"),
            ("a clear on no owner message", [{"item": "wi_h", "clear": "2026-11-11T09:00:00Z"}], "NO_REASON"),
            ("a clear on another step's message", [{"item": "wi_h", "clear": "2026-11-11T10:05:00Z"}], "NO_REASON"),
            ("an unknown work item", [{"item": "p#404", "fix": ["p#10"]}], "NO_HANDOFF"),
            ("one good, one refused", [{"item": "wi_h", "fix": ["p#10"]}, {"item": "wi_n", "fix": ["p#10"]}], "NOT_HELD"),
        ):
            with self.subTest(name):
                code, ops, res = self.run_records(entries)
                self.assertEqual(code, 1, res.get("summary"))
                self.assertIsNone(ops, "nothing is written")
                self.assertEqual([r["reason"] for r in res["details"]["refused"]], [reason])
                self.assertIn("refused, nothing written", res["summary"])

    def test_no_clear_while_another_batch_runs(self) -> None:
        self.hub.batches.append(wi("wi_other", status="queued", slug="p#52"))
        code, ops, res = self.run_records([{"item": "wi_h", "clear": OWNER_AT}])
        self.assertEqual(code, 1, res.get("summary"))
        self.assertIsNone(ops)
        self.assertEqual(res["details"]["refused"][0]["reason"], "BATCH_RUNNING")
        self.assertIn("p#52 (queued)", res["summary"])
        # a batch that is not running clears nothing in its own name
        self.hub.batches = [wi(BATCH, status="wrapped", slug="p#51")]
        code, _, res = self.run_records([{"item": "wi_h", "clear": OWNER_AT}])
        self.assertEqual((code, res["details"]["refused"][0]["reason"]), (1, "BATCH_NOT_RUNNING"))

    def test_a_clear_this_batch_made_is_carried(self) -> None:
        self.hub.items[0] = held_item("wi_h", "p#2", state="merged", red_rounds=1, hold_cleared={"reason": "w", "cleared_in": BATCH})
        code, ops, res = self.run_records([{"item": "wi_h", "clear": OWNER_AT}])
        self.assertEqual((code, ops), (0, []), res.get("summary"))
        self.assertEqual(res["details"]["unchanged"], ["wi_h"])


class RecordsBlameTest(RecordsCase):
    def pins(self) -> str:
        return self.write("revert.json", {"schema": "pf-step-result/1", "command": "revert.py", "version": "", "argv": [], "round": "",
                                          "started_at": "", "ended_at": "", "exit_code": 0, "verdict": "pass",
                                          "heads": {"lib": "l-red", "app": "a-red"}, "summary": "", "details": {}, "logs": []})

    def test_blame(self) -> None:
        env = {"PATH": os.environ.get("PATH", ""), "PF_WI_ID": BATCH, "PF_STEP": "full-gate"}
        code, ops, res = self.run_records([{"item": "wi_h", "blame": ["key:wi_batch/fix/wi_h/0", "key:wi_batch/fix/wi_h/1"]},
                                           {"item": "p#3", "blame": ["key:wi_batch/fix/wi_n/0"]}], "--pins", self.pins(), env=env)
        self.assertEqual(code, 0, res.get("summary"))
        assert ops is not None
        h, n = ops[0]["value"], ops[1]["value"]
        self.assertEqual((h["state"], h["batch"], h["red_rounds"], h["red_heads"]), ("landed", BATCH, 2, {"app": "a-red", "lib": "l-red"}))
        self.assertEqual(h["fix_tickets"], ["key:wi_batch/fix/wi_h/0", "key:wi_batch/fix/wi_h/1"], "the batch's tickets replace the earlier ones")
        self.assertEqual((n["red_rounds"], n["fix_tickets"]), (1, ["key:wi_batch/fix/wi_n/0"]))
        self.assertEqual(ops[0]["if"], {"integration": self.hub.items[0]["attrs"]["integration"]})
        # a blame without the pins is the invocation's
        code, ops, _ = self.run_records([{"item": "wi_h", "blame": ["p#10"]}], env=env)
        self.assertEqual((code, ops), (2, None))
        # a blame on a merged record: the fence
        code, ops, res = self.run_records([{"item": "wi_m", "blame": ["p#10"]}], "--pins", self.pins(), env=env)
        self.assertEqual((code, ops, res["details"]["refused"][0]["reason"]), (1, None, "INTEGRATION_FENCE"))


class RecordsInputsTest(RecordsCase):
    def test_not_judged(self) -> None:
        for name, entries in (
            ("not an array", {"item": "wi_h"}),
            ("an entry of two kinds", [{"item": "wi_h", "fix": ["p#9"], "clear": OWNER_AT}]),
            ("an entry of no kind", [{"item": "wi_h"}]),
            ("an extra key", [{"item": "wi_h", "fix": ["p#9"], "why": "x"}]),
            ("an empty fix", [{"item": "wi_h", "fix": []}]),
            ("a fix of no string", [{"item": "wi_h", "fix": [3]}]),
            ("a clear naming no stamp", [{"item": "wi_h", "clear": ""}]),
            ("an item twice", [{"item": "wi_h", "fix": ["p#9"]}, {"item": "wi_h", "clear": OWNER_AT}]),
            ("a removed verb", [{"verb": "move", "target": "wi_h", "from": "landed", "set": {"state": "merged"}}]),
        ):
            with self.subTest(name):
                code, ops, res = self.run_records(entries)
                self.assertEqual((code, ops), (2, None), res.get("summary"))
        self.hub.fail = HubError("DAEMON-403", "pf step hub get refused: step collect may not call it")
        code, ops, res = self.run_records([{"item": "wi_h", "fix": ["p#9"]}])
        self.assertEqual((code, ops), (2, None))
        self.assertIn("may not call it", res["summary"])
        self.hub.fail = None
        code, ops, _ = self.run_records([{"item": "wi_h", "fix": ["p#9"]}], env={"PATH": os.environ.get("PATH", "")})
        self.assertEqual((code, ops), (2, None), "records needs PF_WI_ID")

    def test_a_stale_ops_file_is_removed(self) -> None:
        self.write("ops.json", [{"verb": "attrs", "target": "wi_stale"}])
        code, ops, _ = self.run_records([{"item": "wi_n", "fix": ["p#10"]}])
        self.assertEqual((code, ops), (1, None))


if __name__ == "__main__":
    unittest.main()
