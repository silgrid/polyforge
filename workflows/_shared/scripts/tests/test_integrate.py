"""integrate.py's rules on real git history and on a fake hub — ported
from the Go integrate package's tests (merges_test.go, hold_test.go: the
every-merge rules of the batch, a fix ticket's release of a hold and the
green cover; the stuck hold) and the Go step commands' pool-reader and
record tests (internal/polyforge/stepscripts runs the script end to end),
with the R13 waiver fallback and the call ledger's filed tickets."""

import json
import os
import unittest
from typing import Any, Callable, Dict, List, Optional

import integrate
from pfstep.hub import HubError

from .gitworld import GitCase, Repo, write


class MRepo(Repo):
    """A task repo's clone: main the default branch, every develop merge a
    --no-ff merge into it, a side commit one on no default branch."""

    def merge(self, name: str) -> str:
        self.git("checkout", "-q", "-b", "pr-" + name, "main")
        self.commit("add " + name, {name: name + "\n"})
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "--no-ff", "-m", "merge " + name, "pr-" + name)
        return self.head()

    def side(self, name: str) -> str:
        self.git("checkout", "-q", "-b", "side-" + name, "main")
        c = self.commit("add " + name, {name: name + "\n"})
        self.git("checkout", "-q", "main")
        return c

    def tag_green(self, name: str, commit: str) -> None:
        self.git("tag", "-a", "-m", "{}", name, commit)


def landed(wid: str, merges: Dict[str, List[str]]) -> Dict[str, Any]:
    return {"id": wid, "slug": "p#" + wid, "priority": "", "landed": "", "waivers": None,
            "integration": {"state": "landed", "merge_commits": {r: list(cs) for r, cs in merges.items()}}}


def snap(items: List[Dict[str, Any]], red_heads: Optional[Dict[str, str]] = None) -> Dict[str, Any]:
    return {"items": items, "open_batches": None, "red_heads": red_heads or {}, "tickets": None}


def pin(head: str, green: str = "") -> Dict[str, str]:
    return {"branch": "main", "head": head, "green": green, "skip": ""}


class MergesTest(GitCase):
    def repo(self, name: str = "app") -> MRepo:
        return MRepo(os.path.join(self.tmp, "r%d" % len(os.listdir(self.tmp)), name), {"base.txt": "base\n"})

    @staticmethod
    def dirs(repos: Dict[str, MRepo]) -> Callable[[str], str]:
        return lambda r: repos[r].dir if r in repos else "/nonexistent/no-such-clone-" + r

    def test_take_batch_needs_every_merge_in_the_pinned_head(self) -> None:
        def both(r: MRepo) -> "tuple[List[str], str]":
            m1, m2 = r.merge("m1"), r.merge("m2")
            return [m1, m2], r.head()

        def first_off(r: MRepo) -> "tuple[List[str], str]":
            s, m2 = r.side("s"), r.merge("m2")
            return [s, m2], r.head()

        def second_off(r: MRepo) -> "tuple[List[str], str]":
            m1, s = r.merge("m1"), r.side("s")
            return [m1, s], r.head()

        def after_pin(r: MRepo) -> "tuple[List[str], str]":
            m1 = r.merge("m1")
            p = r.head()
            return [m1, r.merge("m2")], p

        for name, build, why in (
            ("both merges on main", both, ""),
            ("the first merge on no default branch", first_off, "is not in the pinned head"),
            ("the second merge on no default branch", second_off, "is not in the pinned head"),
            ("the second merge after the pin", after_pin, "merged after the pin"),
        ):
            with self.subTest(name):
                r = self.repo()
                c = r.merge("c")
                merges, p = build(r)
                heads = {"app": pin(p)}
                items, skipped, _ = integrate.take_batch(self.dirs({"app": r}), heads, snap([landed("wi_c", {"app": [c]}), landed("wi_m", {"app": merges})]))
                ids = [i["id"] for i in items]
                if not why:
                    self.assertEqual((ids, skipped), (["wi_c", "wi_m"], []))
                    self.assertEqual(items[1]["merge_commits"]["app"], merges)
                    continue
                self.assertEqual(ids, ["wi_c"], "SYMPTOM: a merge of wi_m outside the pinned head must keep it out")
                self.assertIn(why, [s["why"] for s in skipped if s["wi"] == "wi_m"][0])
                self.assertEqual(heads["app"]["skip"], "", "wi_c's merge still gates app")

    def test_an_item_with_a_merge_outside_the_pins_gates_nothing(self) -> None:
        app, lib = self.repo("app"), self.repo("lib")
        am, ls = app.merge("m"), lib.side("s")
        heads = {"app": pin(app.head()), "lib": pin(lib.head())}
        items, skipped, repo_skips = integrate.take_batch(self.dirs({"app": app, "lib": lib}), heads, snap([landed("wi_m", {"app": [am], "lib": [ls]})]))
        self.assertEqual(items, [])
        self.assertIn("is not in the pinned head", skipped[0]["why"])
        self.assertEqual((integrate.gated(heads), len(repo_skips)), ([], 2), "SYMPTOM: it gates no repo")

    def test_a_green_tag_covers_every_merge(self) -> None:
        for name, over_both in (("over the first merge only", False), ("over both merges", True)):
            with self.subTest(name):
                r = self.repo()
                m1 = r.merge("m1")
                r.tag_green("green/20261102-1", m1)
                m2 = r.merge("m2")
                green = "green/20261102-1"
                if over_both:
                    r.tag_green("green/20261102-2", m2)
                    green = "green/20261102-2"
                r.merge("later")
                heads = {"app": pin(r.head(), green)}
                items, skipped, _ = integrate.take_batch(self.dirs({"app": r}), heads, snap([landed("wi_m", {"app": [m1, m2]})]))
                if over_both:
                    self.assertEqual(items, [])
                    self.assertIn("every merge of it is under a green tag already", skipped[0]["why"])
                else:
                    self.assertEqual([i["id"] for i in items], ["wi_m"], "SYMPTOM: a green tag over the first merge only must leave the item the batch's")

    def test_merged_after_any_merge_outside_the_blaming_gates_head(self) -> None:
        r = self.repo()
        f1 = r.merge("f1")
        blamed = r.head()
        f2 = r.merge("f2")
        s = r.side("s")
        src = {"id": "wi_s", "integration": {"red_heads": {"app": blamed}}}
        repo_dir = self.dirs({"app": r})
        for name, merges, after in (
            ("its one merge before the gate", {"app": [f1]}, False),
            ("the second merge after the gate", {"app": [f1, f2]}, True),
            ("the record listing the newer merge first", {"app": [f2, f1]}, True),
            ("a merge on no default branch, the newest in place", {"app": [s, f1]}, True),
            ("a merge on no default branch, listed last", {"app": [f1, s]}, True),
            ("a repo the blaming gate did not judge", {"app": [f1], "lib": [f1]}, True),
        ):
            self.assertEqual(integrate.merged_after(repo_dir, merges, src), after, "SYMPTOM: " + name)

    def test_green_cover_covers_every_merge(self) -> None:
        def earlier_first_only(r: MRepo) -> "tuple[List[str], Dict[str, str], Dict[str, str]]":
            m1 = r.merge("m1")
            r.tag_green("green/20261102-1", m1)
            return [m1, r.merge("m2")], {}, {}

        def second_first_off(r: MRepo) -> "tuple[List[str], Dict[str, str], Dict[str, str]]":
            s, m2 = r.side("s"), r.merge("m2")
            r.tag_green("green/20261102-1", m2)
            return [s, m2], {}, {}

        def earlier_both(r: MRepo) -> "tuple[List[str], Dict[str, str], Dict[str, str]]":
            m1, m2 = r.merge("m1"), r.merge("m2")
            r.tag_green("green/20261102-1", m2)
            return [m1, m2], {}, {}

        def this_both(r: MRepo) -> "tuple[List[str], Dict[str, str], Dict[str, str]]":
            m1, m2 = r.merge("m1"), r.merge("m2")
            return [m1, m2], {"app": r.head()}, {"app": "green/20261111-1"}

        def this_first_off(r: MRepo) -> "tuple[List[str], Dict[str, str], Dict[str, str]]":
            s, m2 = r.side("s"), r.merge("m2")
            return [s, m2], {"app": r.head()}, {"app": "green/20261111-1"}

        for name, build, cover in (
            ("an earlier tag over the first merge only", earlier_first_only, ""),
            ("a tag over the second, the first on no default branch", second_first_off, ""),
            ("an earlier tag over both", earlier_both, "green/20261102-1"),
            ("this batch's tag over both", this_both, "green/20261111-1"),
            ("this batch's tag, the first merge on no default branch", this_first_off, ""),
        ):
            with self.subTest(name):
                r = self.repo()
                merges, greens, tags = build(r)
                got = integrate.green_cover(self.dirs({"app": r}), {"app": merges}, greens, tags)
                if not cover:
                    self.assertIsNone(got, "SYMPTOM: a merge under no green tag must leave the item uncovered")
                else:
                    self.assertEqual((got or {}).get("app"), cover)
        self.assertIsNone(integrate.green_cover(self.dirs({}), {"lib": ["abc"]}, {}, {}), "a merge in no task repo is covered by nothing")


class HoldTest(unittest.TestCase):
    def test_hold_stuck(self) -> None:
        def t(ref: str, status: str = "", integ: str = "", after: bool = False) -> Dict[str, Any]:
            return {"ref": ref, "status": status, "integration": integ, "merged_after": after}

        for name, fixes, stuck in (
            ("none registered", [], True),
            ("one in development", [t("p#1", "running")], False),
            ("one queued", [t("p#1", "queued")], False),
            ("one landed after the blaming gate", [t("p#1", "wrapped", "landed", True)], False),
            ("one landed before the blaming gate", [t("p#1", "wrapped", "landed")], True),
            ("failed and canceled", [t("p#1", "failed"), t("p#2", "canceled")], True),
            ("one reverted, one still in development", [t("p#1", "wrapped", "reverted"), t("p#3", "queued")], False),
            ("wrapped with no hand-off", [t("p#1", "wrapped")], True),
            ("not found", [t("p#9")], True),
        ):
            got, why = integrate.hold_stuck(fixes)
            self.assertEqual(got, stuck, (name, why))
        _, why = integrate.hold_stuck([t("p#1", "wrapped", "landed", True), t("p#9")])
        self.assertEqual(why, "p#1 wrapped, landed after the blaming gate; p#9 not found")


class FakeHub:
    """`pf step hub list/get` answered from work items, every call kept."""

    def __init__(self, items: List[Dict[str, Any]], batches: Optional[List[Dict[str, Any]]] = None,
                 tickets: Optional[List[Dict[str, Any]]] = None, fail: Optional[Exception] = None) -> None:
        self.items, self.batches, self.tickets, self.fail = items, batches or [], tickets or [], fail
        self.calls: List[str] = []

    def __call__(self, args: List[str]) -> bytes:
        self.calls.append(" ".join(args))
        if self.fail is not None:
            raise self.fail
        if args[0] == "list":
            flags = dict(zip(args[1::2], args[2::2]))
            return json.dumps({"items": self.batches if flags.get("--bundle") else self.items}).encode()
        for it in self.items + self.batches + self.tickets:
            if args[1] in (it.get("id"), it.get("slug")):
                return json.dumps(it).encode()
        raise HubError("DAEMON-404", "get: work item %s not found in project p" % args[1])


def wi(wid: str, status: str = "wrapped", attrs: Optional[Dict[str, Any]] = None, **kw: Any) -> Dict[str, Any]:
    d: Dict[str, Any] = {"id": wid, "slug": "", "seq": 0, "status": status, "attrs": attrs or {}, "updated_at": "2026-11-10T08:00:00Z", "closed_at": None}
    d.update(kw)
    return d


class PoolTest(unittest.TestCase):
    def test_reads_and_their_flags(self) -> None:
        hub = FakeHub(
            [wi("wi_h", attrs={"integration": {"state": "landed", "red_rounds": 1, "fix_tickets": ["p#9", "other#1", "p#9"]}}, slug="p#2", closed_at="2026-11-10T07:00:00Z")],
            batches=[wi("wi_b1", seq=1, attrs={"integration_batch": {"result": "red", "main": {"app": "red-old", "lib": "lib-red"}}}),
                     wi("wi_b2", seq=2, attrs={"integration_batch": {"result": "red", "main": {"app": "red-new"}}}),
                     wi("wi_b3", seq=3, status="failed", attrs={"integration_batch": {"result": "red", "main": {"app": "failed"}}}),
                     wi("wi_b4", seq=4, status="running"), wi("wi_b5", seq=5, status="queued")],
            tickets=[wi("wi_t9", slug="p#9", attrs={"integration": {"state": "landed", "merge_commits": {"app": ["m1", "", 3]}}})])
        s = integrate.read_pool(integrate.HubLister(hub), "integrate")
        self.assertEqual(hub.calls, ["list --status wrapped --attr integration.state=landed --limit 1000", "get p#9", "get other#1", "list --bundle integrate --limit 200"])
        self.assertEqual(s["items"][0]["landed"], "2026-11-10T07:00:00Z")
        self.assertEqual(s["tickets"]["p#9"], {"status": "wrapped", "integration": "landed", "merge_commits": {"app": ["m1"]}})
        self.assertEqual(s["tickets"]["other#1"]["status"], "", "a ticket the hub does not find")
        self.assertEqual(s["red_heads"], {"app": "red-new", "lib": "lib-red"})
        self.assertEqual(s["open_batches"], ["wi_b5", "wi_b4"])
        with self.assertRaisesRegex(HubError, "may not call it"):
            integrate.read_pool(integrate.HubLister(FakeHub([], fail=HubError("DAEMON-403", "pf step hub list refused: step close may not call it"))), "integrate")

    def test_waivers_r13_fallback(self) -> None:
        waived = [{"gate": "verification", "ruling": "这次不算"}]
        # contract 4: attrs.waivers, present (even empty) wins
        self.assertEqual(integrate.waivers_of({"waivers": [{"words": "w"}]}, {"waived": waived}), [{"words": "w"}])
        self.assertEqual(integrate.waivers_of({"waivers": []}, {"waived": waived}), [])
        # R13: no attrs.waivers key (landed before contract 4): the record's waived
        self.assertEqual(integrate.waivers_of({}, {"waived": waived}), waived)
        self.assertIsNone(integrate.waivers_of({}, {}))
        hub = FakeHub([wi("wi_old", attrs={"integration": {"state": "landed", "merge_commits": {"app": ["m"]}, "waived": waived}})])
        s = integrate.read_pool(integrate.HubLister(hub), "integrate")
        self.assertTrue(integrate.batch_item_of(s["items"][0]).get("waived"), "SYMPTOM: an in-flight pre-contract-4 waiver is lost")

    def test_no_lane_and_an_empty_pool(self) -> None:
        hub = FakeHub([])
        with self.assertRaisesRegex(RuntimeError, "no integration lane named"):
            integrate.read_pool(integrate.HubLister(hub), "")
        self.assertEqual(hub.calls, [], "nothing listed without a lane")
        # an empty pool: no items, no batches, no red heads (a map, never null)
        s = integrate.read_pool(integrate.HubLister(hub), "integrate")
        self.assertEqual(integrate.snapshot_json(s), {"items": None, "red_heads": {}})


class CloseShapesTest(GitCase):
    def test_batch_record_shape(self) -> None:
        f = {"result": "green", "refused": "", "empty": "", "hold_rulings": None, "main": {"app": "c1"}, "tags": {"app": "green/20261110-1"},
             "items": [{"id": "wi_a", "waived": False}, {"id": "wi_b", "waived": True}], "green_items": ["p#2", "p#1"], "fix_tickets": ["p#9"],
             "closed_at": "2026-11-10T00:00:00Z"}
        got = json.dumps(integrate.batch_record(f), sort_keys=True, separators=(",", ":"))
        self.assertEqual(got, '{"closed_at":"2026-11-10T00:00:00Z","fix_tickets":["p#9"],"green_items":["p#1","p#2"],"items":["wi_a","wi_b"],'
                              '"main":{"app":"c1"},"result":"green","tags":{"app":"green/20261110-1"},"waived":["wi_b"]}')
        f = dict(f, result="empty", refused="another batch", main={}, tags={}, items=[], green_items=[], fix_tickets=[], closed_at="t")
        got = json.dumps(integrate.batch_record(f), sort_keys=True, separators=(",", ":"))
        self.assertEqual(got, '{"closed_at":"t","items":[],"main":{},"refused":"another batch","result":"empty"}')

    def test_filed_tickets_from_the_ledger(self) -> None:
        p = os.path.join(self.tmp, "step-calls.jsonl")
        self.assertEqual(integrate.filed_tickets(p), [], "no ledger, no ticket")
        lines = [
            {"seq": 1, "kind": "call", "verb": "hub.batch"},
            {"seq": 2, "kind": "exec", "verb": "hub.file", "op": "1.1", "ref": "p#9", "outcome": "done"},
            {"seq": 3, "kind": "exec", "verb": "hub.file", "op": "1.2", "outcome": "failed", "error": "aihub unreachable"},
            {"seq": 4, "kind": "exec", "verb": "hub.move", "op": "1.3", "ref": "p#8", "outcome": "done"},
            {"seq": 5, "kind": "exec", "verb": "hub.file", "op": "4.1", "ref": "p#7", "outcome": "done"},
            {"seq": 0, "kind": "exec", "verb": "hub.file", "ref": "p#0", "outcome": "done"},
            {"seq": 6, "kind": "exec", "verb": "hub.file", "ref": "p#6", "outcome": "done", "round": "1"},
            {"seq": 7, "kind": "exec", "verb": "hub.file", "op": "1.1", "ref": "p#9", "outcome": "done"},
        ]
        with open(p, "w") as f:
            f.write("\n".join(json.dumps(x) for x in lines) + '\n{"seq": 8, "kind": "exec", "verb"\n')
        self.assertEqual(integrate.filed_tickets(p), ["p#9", "p#7"])

    def test_close_writes_its_moves_as_hub_writes(self) -> None:
        # the ops file's shape (what `pf step hub batch` registers: attrs
        # writes, workflow contract 1), on a batch a green tag of an
        # earlier batch covers
        r = MRepo(os.path.join(self.tmp, "capsule", "repos", "app"), {"base.txt": "b\n"})
        origin = os.path.join(self.tmp, "origin.git")
        r.git("init", "-q", "--bare", "-b", "main", origin)
        r.git("remote", "add", "origin", origin)
        m = r.merge("m")
        r.tag_green("green/20261102-1", m)
        r.git("push", "-q", "origin", "main", "refs/tags/green/20261102-1")
        cap = os.path.join(self.tmp, "capsule")
        pool = os.path.join(cap, "pool.json")
        write(cap, {"pool.json": json.dumps({"items": [{"id": "wi_m", "slug": "p#1", "landed": "x", "integration": {"state": "landed", "merge_commits": {"app": [m]}}}], "red_heads": {}})})
        batch = {"schema": "pf-step-result/1", "command": "integrate.py", "version": "", "argv": [], "round": "", "started_at": "", "ended_at": "",
                 "exit_code": 0, "verdict": "pass", "heads": {"app": m}, "summary": "", "logs": [],
                 "details": {"batch": "wi_batch", "heads": {"app": m}, "refused": "", "empty": "nothing new", "items": []}}
        write(cap, {"batch.json": json.dumps(batch)})
        ops = os.path.join(cap, "ops.json")
        code = integrate.main(["close", "--pool", pool, "--batch", os.path.join(cap, "batch.json"), "--capsule", cap, "--ops-out", ops, "--out", os.path.join(cap, "close.json")],
                              environ={"PATH": os.environ.get("PATH", ""), "PF_WI_ID": "wi_batch"})
        self.assertEqual(code, 0)
        with open(ops) as f:
            got = json.load(f)
        # the whole record: the develop lane's keys kept, the move's set
        # over them, fenced on the whole record as read
        self.assertEqual(got, [{"verb": "attrs", "target": "wi_m", "name": "integration",
                                "value": {"batch": "wi_batch", "green_tags": {"app": "green/20261102-1"}, "merge_commits": {"app": [m]}, "state": "merged"},
                                "if": {"integration": {"state": "landed", "merge_commits": {"app": [m]}}}}])

    def test_close_reads_the_revert_steps_result(self) -> None:
        # --revert: revert.py's result puts its landed
        # reverts on the record; a result file of another command — the
        # retired pf-revert's included — is the invocation's (exit 2)
        r = MRepo(os.path.join(self.tmp, "capsule", "repos", "app"), {"base.txt": "b\n"})
        origin = os.path.join(self.tmp, "origin.git")
        r.git("init", "-q", "--bare", "-b", "main", origin)
        r.git("remote", "add", "origin", origin)
        m = r.merge("m")
        r.git("push", "-q", "origin", "main")
        cap = os.path.join(self.tmp, "capsule")
        pool = os.path.join(cap, "pool.json")
        write(cap, {"pool.json": json.dumps({"items": [], "red_heads": {}})})
        env = {"schema": "pf-step-result/1", "version": "", "argv": [], "round": "", "started_at": "", "ended_at": "",
               "exit_code": 0, "verdict": "pass", "heads": {"app": m}, "summary": "", "logs": []}
        batch = dict(env, command="integrate.py",
                     details={"batch": "wi_batch", "heads": {"app": m}, "refused": "", "empty": "nothing new", "items": []})
        write(cap, {"batch.json": json.dumps(batch)})
        reverts = {"reverts": [{"wi": "wi_b", "name": "p#2", "group": ["wi_b"], "done": True,
                                "repos": [{"name": "app", "merge_commits": [m], "revert_pr": "https://forge.invalid/pr/77", "revert_merge": m}]}],
                   "heads": {"app": m}, "gated": ["app"]}
        for command, want in (("revert.py", 0), ("pf-revert", 2), ("pf-gate", 2)):
            with self.subTest(command):
                write(cap, {"revert.json": json.dumps(dict(env, command=command, details=reverts))})
                rec = os.path.join(cap, "record-%s.json" % command)
                code = integrate.main(["close", "--pool", pool, "--batch", os.path.join(cap, "batch.json"), "--revert", os.path.join(cap, "revert.json"),
                                       "--capsule", cap, "--record-out", rec, "--out", os.path.join(cap, "close.json")],
                                      environ={"PATH": os.environ.get("PATH", ""), "PF_WI_ID": "wi_batch"})
                self.assertEqual(code, want)
                if want == 0:
                    with open(rec) as f:
                        got = json.load(f)
                    self.assertEqual(got["reverts"], [{"name": "p#2", "prs": ["https://forge.invalid/pr/77"], "wi": "wi_b"}])
                else:
                    with open(os.path.join(cap, "close.json")) as f:
                        self.assertIn("not a revert.py result file", json.load(f)["summary"])


if __name__ == "__main__":
    unittest.main()
