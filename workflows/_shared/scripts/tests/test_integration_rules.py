"""The integration lane's record rules (pfstep.integration), ported case by
case from aihub's former integration route (workflow contract 1: aihub
keeps no lane's rules). The cases of the deleted
internal/aihub/core/w404_integration_update_test.go, each under its old
name: a move is from landed and keeps the develop lane's keys; the
transition table is landed -> landed, merged or reverted; a hold is
cleared only on the owner's word, recorded on the record; a merge needs
its green tag; and no hold is cleared while another batch of the lane
runs. Every write is fenced on the whole record as read
(CompareAndSwapTest)."""

import copy
import json
import unittest
from typing import Any, Dict, Optional

from pfstep import integration as rules
from pfstep.integration import RuleError


def landed_handoff(pr: str) -> Dict[str, Any]:
    """The develop lane's hand-off record as the wrap carries it."""
    head, merge = "a" * 40, "c" * 40
    return {"state": "landed",
            "repos": [{"repo": "app", "pr": pr, "head": head, "light_verified_head": head, "merge_commit": merge}],
            "merge_commits": {"app": [merge]}}


class RulesCase(unittest.TestCase):
    def refused(self, reason: str, f: Any, *args: Any, **kw: Any) -> RuleError:
        with self.assertRaises(RuleError) as cm:
            f(*args, **kw)
        self.assertEqual(cm.exception.reason, reason, str(cm.exception))
        return cm.exception


class W404UpdateIntegrationFencedTest(RulesCase):
    """TestW404UpdateIntegrationFencedAndAudited."""

    def test_fenced_and_keeps_the_develop_lanes_keys(self) -> None:
        rnd = {"state": "landed", "batch": "p#9", "red_rounds": 1, "fix_tickets": ["p#10"], "red_heads": {"app": "d" * 40}}
        # a work item that is not wrapped carries no hand-off
        self.refused(rules.NOT_WRAPPED, rules.move, "queued", None, rnd)
        self.refused(rules.NO_HANDOFF, rules.move, "wrapped", None, rnd)
        rec = landed_handoff("https://github.com/o/r/pull/7")
        for reason, bad in (
            (rules.NO_STATE, {}),
            (rules.NO_STATE, {"batch": "p#9"}),
            (rules.LANE_KEY, {"state": "landed", "merge_commits": {"app": ["e" * 40]}}),
            (rules.LANE_KEY, {"state": "landed", "repos": []}),
            (rules.LANE_KEY, {"state": "landed", "waived": None}),
        ):
            self.refused(reason, rules.move, "wrapped", rec, bad)
        got = rules.move("wrapped", rec, rnd)
        assert got is not None
        self.assertEqual(got["state"], "landed")
        self.assertEqual(got["batch"], "p#9")
        self.assertEqual(got["red_heads"], {"app": "d" * 40})
        self.assertEqual(got["merge_commits"], rec["merge_commits"])
        self.assertEqual(got["repos"], rec["repos"], "the move keeps the develop lane's keys")
        self.assertEqual(rec, landed_handoff("https://github.com/o/r/pull/7"), "the record read is never changed in place")
        merged = rules.move("wrapped", got, {"state": "merged", "green_tag": "green/20261108-1", "batch": None})
        assert merged is not None
        self.assertNotIn("batch", merged, "a null value deletes the key")
        self.assertEqual(merged["green_tag"], "green/20261108-1")
        # a move from the state the record left hits the fence
        self.refused(rules.FENCED, rules.move, "wrapped", merged, {"state": "merged", "green_tag": "green/20261108-2"})
        # the record already carrying the move (a resumed step repeating
        # it) needs no write
        self.assertIsNone(rules.move("wrapped", merged, {"state": "merged", "green_tag": "green/20261108-1", "batch": None}))

    def test_the_write_is_fenced_on_the_record_as_read(self) -> None:
        read = {"state": "landed", "repos": [{"repo": "app"}], "red_rounds": 1}
        op = rules.write("wi_s", {"state": "merged"}, read, per_owner="2026-11-11T10:00:00Z")
        self.assertEqual(op, {"verb": "attrs", "target": "wi_s", "name": "integration", "value": {"state": "merged"},
                              "if": {"integration": read}, "per_owner": "2026-11-11T10:00:00Z"})
        read["red_rounds"] = 2
        self.assertEqual(op["if"]["integration"]["red_rounds"], 1, "the fence is a copy of the read, not a view of it")
        self.assertNotIn("per_owner", rules.write("wi_s", {"state": "merged"}, read))


class W404MovesAreLandedOnlyTest(RulesCase):
    """TestW404IntegrationMovesAreLandedOnly: the transition table is
    landed -> landed, merged (on a green tag) or reverted; every other pair
    of the record's states — the deleted pending and bounced included — is
    refused, whatever the record's state."""

    def test_every_pair(self) -> None:
        states = ["pending", "landed", "merged", "bounced", "reverted"]
        for frm in states:
            for to in states:
                rec = dict(landed_handoff("https://github.com/o/r/pull/5"), state=frm)
                changes = {"state": to, "green_tag": "green/20261108-1"}
                if frm == "landed" and to in ("landed", "merged", "reverted"):
                    self.assertIsNotNone(rules.move("wrapped", rec, changes), (frm, to))
                    continue
                with self.assertRaises(RuleError, msg="%s -> %s" % (frm, to)) as cm:
                    rules.move("wrapped", rec, changes)
                self.assertIn(cm.exception.reason, (rules.NOT_A_MOVE, rules.FENCED), (frm, to))
                if to not in ("landed", "merged", "reverted"):
                    self.assertIn("not a move of the integration lane", str(cm.exception))

    def test_reverted_is_final(self) -> None:
        rec = landed_handoff("https://github.com/o/r/pull/5")
        for changes in ({"state": "landed", "red_rounds": 1}, {"state": "reverted"}):
            got = rules.move("wrapped", rec, changes)
            assert got is not None
            rec = got
        self.refused(rules.FENCED, rules.move, "wrapped", rec, {"state": "landed"})


class W404ClearIntegrationHoldTest(RulesCase):
    """TestW404ClearIntegrationHold: a held source — landed, blamed by a red
    batch — is released to merged with the owner's words recorded; an item
    no red batch blamed is refused, and a cleared hold is not cleared twice.
    (The blaming batch's record no longer gains holds_cleared: the clearing
    batch's own record carries the ruling, hold_rulings.)"""

    def test_clear(self) -> None:
        rec = rules.move("wrapped", landed_handoff("https://github.com/o/r/pull/1"), {"state": "landed", "batch": "p#8"})
        self.refused(rules.NOT_HELD, rules.clear_hold, "wrapped", rec, "the red is the environment's", "u_owner", "t1", "wi_b", "t2")
        rec = rules.move("wrapped", rec, {"state": "landed", "red_rounds": 1, "fix_tickets": ["p#9"]})
        self.refused(rules.NO_REASON, rules.clear_hold, "wrapped", rec, " ", "u_owner", "t1", "wi_b", "t2")
        self.refused(rules.NOT_WRAPPED, rules.clear_hold, "running", rec, "w", "u_owner", "t1", "wi_b", "t2")
        got = rules.clear_hold("wrapped", rec, "the red is the environment's", "u_owner", "t1", "wi_b", "t2")
        assert got is not None
        hc = got["hold_cleared"]
        self.assertEqual(got["state"], "merged")
        self.assertEqual(hc["reason"], "the red is the environment's")
        self.assertEqual(hc["by"], "u_owner")
        self.assertEqual(got["merge_commits"], landed_handoff("x")["merge_commits"])
        # the same batch's clear again: carried, no write; another batch's: refused
        self.assertIsNone(rules.clear_hold("wrapped", got, "again", "u_owner", "t1", "wi_b", "t3"))
        self.refused(rules.NOT_HELD, rules.clear_hold, "wrapped", got, "again", "u_owner", "t1", "wi_c", "t3")

    def test_the_hold_definition(self) -> None:
        base = landed_handoff("x")
        for name, rec, want in (
            ("no record", None, False),
            ("landed, never blamed", base, False),
            ("landed, a red round", dict(base, red_rounds=1), True),
            ("landed, a fix ticket", dict(base, fix_tickets=["p#9"]), True),
            ("landed, red_rounds 0 and no ticket", dict(base, red_rounds=0, fix_tickets=[]), False),
            ("landed, an empty ticket", dict(base, fix_tickets=[""]), False),
            ("merged after a red round", dict(base, state="merged", red_rounds=1), False),
            ("reverted with a ticket", dict(base, state="reverted", fix_tickets=["p#9"]), False),
            ("a red round as a string", dict(base, red_rounds="1"), False),
        ):
            self.assertEqual(rules.held(rec), want, name)


class W404HoldIsClearedOnlyOnTheOwnersWordTest(RulesCase):
    """TestW404HoldIsClearedOnlyByTheClearRoute: a move takes landed ->
    merged only on a green tag (close's move) and never writes
    hold_cleared: a hold is cleared only by clear_hold, which records the
    owner's words, who answered and when, and the batch."""

    def test_moves_never_clear(self) -> None:
        rec = rules.move("wrapped", landed_handoff("https://github.com/o/r/pull/3"), {"state": "landed", "red_rounds": 1, "fix_tickets": ["p#10"]})
        for changes in (
            {"state": "merged"},
            {"state": "merged", "green_tag": ""},
            {"state": "merged", "green_tags": {}},
            {"state": "merged", "green_tag": "green/20261108-1", "hold_cleared": {"by": "owner"}},
            {"state": "landed", "hold_cleared": {"by": "owner"}},
        ):
            with self.assertRaises(RuleError, msg=str(changes)):
                rules.move("wrapped", rec, changes)
        self.assertIsNotNone(rules.move("wrapped", rec, {"state": "merged", "green_tags": {"app": "green/20261108-1"}}))
        got = rules.clear_hold("wrapped", rec, "the environment's", "u_owner", "2026-11-08T10:00:00Z", "wi_b", "2026-11-08T10:05:00Z")
        assert got is not None
        self.assertEqual(got["hold_cleared"], {"reason": "the environment's", "by": "u_owner", "at": "2026-11-08T10:05:00Z",
                                               "answered_by": "u_owner", "answered_at": "2026-11-08T10:00:00Z", "cleared_in": "wi_b"})
        anon = rules.clear_hold("wrapped", rec, "w", "", "t1", "wi_b", "t2")
        assert anon is not None
        self.assertEqual(anon["hold_cleared"]["by"], "owner", "a message naming no writer is the owner's")


class W404ClearHoldRefusedWhileABatchRunsTest(RulesCase):
    """TestW404ClearHoldRefusedWhileABatchRuns: while another batch of the
    lane is unfinished — it may be acting on the hold — the clear is
    refused naming it; a batch that is not running never clears in its own
    name; the running batch alone clears, and the record names it."""

    def batches(self, *extra: Dict[str, Any]) -> list:  # type: ignore[type-arg]
        return [{"id": "wi_old", "slug": "p#50", "status": "wrapped"}, {"id": "wi_f", "slug": "p#51", "status": "failed"},
                {"id": "wi_c", "slug": "p#52", "status": "canceled"}, {"id": "wi_b", "slug": "p#53", "status": "running"}] + list(extra)

    def test_refused_while_another_runs(self) -> None:
        err = self.refused(rules.BATCH_RUNNING, rules.refuse_while_batches_run, self.batches({"id": "wi_o", "slug": "p#54", "status": "queued"}), "wi_b")
        self.assertIn("p#54 (queued)", str(err))
        self.refused(rules.BATCH_NOT_RUNNING, rules.refuse_while_batches_run, self.batches(), "p#nope")
        self.refused(rules.BATCH_NOT_RUNNING, rules.refuse_while_batches_run, self.batches(), "wi_old")
        rules.refuse_while_batches_run(self.batches(), "wi_b")
        rules.refuse_while_batches_run(self.batches(), "p#53")
        rec = rules.move("wrapped", landed_handoff("x"), {"state": "landed", "red_rounds": 1})
        got = rules.clear_hold("wrapped", rec, "the environment's", "u_owner", "t1", "p#53", "t2")
        assert got is not None
        self.assertEqual(got["hold_cleared"]["cleared_in"], "p#53")


class RecordOfTest(unittest.TestCase):
    def test_record_of(self) -> None:
        rec: Optional[Dict[str, Any]] = rules.record_of({"attrs": {"integration": {"state": "landed"}}})
        self.assertEqual(rec, {"state": "landed"})
        self.assertIsNone(rules.record_of({"attrs": {}}))
        self.assertIsNone(rules.record_of({"attrs": {"integration": "landed"}}))
        self.assertIsNone(rules.record_of({}))
        item = {"attrs": {"integration": {"state": "landed", "fix_tickets": ["p#9"]}}}
        got = rules.record_of(item)
        assert got is not None
        changed = rules.move("wrapped", got, {"state": "landed", "fix_tickets": ["p#9", "p#10"]})
        self.assertEqual(item, {"attrs": {"integration": {"state": "landed", "fix_tickets": ["p#9"]}}}, copy.deepcopy(changed))


if __name__ == "__main__":
    unittest.main()


class AttrsConflict(Exception):
    """aihub's CORE-409 ATTRS_IF_MISMATCH: nothing was written."""


def aihub_write(attrs: Dict[str, Any], op: Dict[str, Any]) -> None:
    """aihub's generic attrs write as internal/aihub/core/attrs.go makes it
    (WriteAttr), over the wire's JSON: every `if` path must hold its JSON
    value — an absent path reading as null — or the write is refused and
    nothing is written; else the top-level key is replaced whole."""
    op = json.loads(json.dumps(op))
    for path, want in sorted(op.get("if", {}).items()):
        cur: Any = attrs
        for seg in path.split("."):
            cur = cur.get(seg) if isinstance(cur, dict) else None
        if cur != want:
            raise AttrsConflict("attrs.%s is %s, not %s" % (path, json.dumps(cur), json.dumps(want)))
    attrs[op["name"]] = op["value"]


class CompareAndSwapTest(RulesCase):
    """Two writes registered in one round from one read of a record — a
    fix ticket registered on a hold, and the green tag's move to merged —
    never drop one another's keys: each is fenced on the whole record as
    read, so the first one made changes the record and the second is
    refused (CORE-409), where a fence on the state alone would have let it
    replace the record and lose the first one's keys."""

    def test_the_second_write_from_one_read_is_refused(self) -> None:
        read = landed_handoff("https://forge.invalid/pr/1")
        read.update({"red_rounds": 1, "fix_tickets": ["p#9"]})
        attrs: Dict[str, Any] = {"integration": copy.deepcopy(read)}
        fix = rules.move("wrapped", read, {"state": "landed", "fix_tickets": ["p#9", "p#10"]})
        green = rules.move("wrapped", read, {"state": "merged", "green_tag": "green/20261112-1", "batch": "wi_b"})
        assert fix is not None and green is not None
        first, second = rules.write("wi_s", fix, read), rules.write("wi_s", green, read)
        aihub_write(attrs, first)
        with self.assertRaises(AttrsConflict):
            aihub_write(attrs, second)
        self.assertEqual(attrs["integration"], fix, "the first write's keys stand; the second wrote nothing")
        self.assertEqual(attrs["integration"]["fix_tickets"], ["p#9", "p#10"])
        # a write built from the record as it is now is made
        now = rules.move("wrapped", attrs["integration"], {"state": "merged", "green_tag": "green/20261112-1", "batch": "wi_b"})
        assert now is not None
        aihub_write(attrs, rules.write("wi_s", now, attrs["integration"]))
        self.assertEqual((attrs["integration"]["state"], attrs["integration"]["fix_tickets"]), ("merged", ["p#9", "p#10"]))
