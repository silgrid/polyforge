# carries: workflows/_shared/scripts/release.py, workflows/_shared/scripts/tests/test_release.py
"""release.py as the release step runs it: the green-batch resolver's
decisions on real git clones and a fake hub (polyforge#154, AC1/AC2/AC3
and AC6's lane-side scenarios). Expected values are derived from the
ticket's settled answers and the owner's ruling of 2026-11-19, never from
the implementation: the latest green batch is the one with the newest
``closed_at`` (Q4 B), a pinned commit behind the default branch's head is
still the release object, a single ``v*`` tag on the owner pin means the
same release resumed — whoever claimed it — and only two or more tags (or
a version a second cut would make) refuse ``PF-RELEASE-ALREADY_RELEASED``
; every batch pin must be on the default branch's history, the owner repo
must be covered (Q5 B), a capsule repo the batch does not cover is not
part of the release, and no green batch refuses
``PF-RELEASE-NO_GREEN_BATCH`` (Q7 A: never an invented release object)."""

import json
import os
import unittest
from typing import Any, Dict, List, Optional, Tuple

import release
from pfstep import LIBRARY

from . import schemacheck
from .gitworld import GitCase, git, write


def batch(wid: str, result: str, main: Dict[str, str], closed_at: str, seq: int = 1) -> Dict[str, Any]:
    """A wrapped integrate batch work item as the hub list answers it."""
    return {"id": wid, "slug": "", "seq": seq, "status": "wrapped", "closed_at": closed_at,
            "updated_at": closed_at, "attrs": {"integration_batch": {"result": result, "main": main, "closed_at": closed_at}}}


class FakeHub:
    """``pf --json step hub list`` answered from a fixed page; every call kept."""

    def __init__(self, items: List[Dict[str, Any]], fail: Optional[Exception] = None) -> None:
        self.items, self.fail, self.calls = items, fail, [] 

    def __call__(self, args: List[str]) -> bytes:
        self.calls.append(" ".join(args))
        if self.fail is not None:
            raise self.fail
        return json.dumps({"items": self.items}).encode("utf-8")


def read_json(path: str) -> Dict[str, Any]:
    with open(path, encoding="utf-8") as f:
        doc = json.load(f)
    if not isinstance(doc, dict):
        raise AssertionError("not a JSON object: %s" % path)
    return doc


class ReleaseBatchTest(GitCase):
    """One capsule with real clones of real (bare, local) origins."""

    def setUp(self) -> None:
        super().setUp()
        self.cap = self.new_dir("capsule")
        self.env = {"PATH": os.environ.get("PATH", ""), "PF_ROUND": "1"}
        self.calls: List[str] = []

    # -- the world ---------------------------------------------------------

    def repo(self, name: str, owner: bool = False) -> str:
        """One capsule clone of a fresh bare origin on main; the owner
        carries .polyforge/release.sh in its base commit."""
        origin = self.new_dir(name + ".git")
        git(self.tmp, "init", "-q", "--bare", "-b", "main", origin)
        seed = self.new_dir(name + "-seed")
        git(self.tmp, "clone", "-q", origin, seed)
        write(seed, {".polyforge/release.sh": "# procedure\n"} if owner else {"lib.txt": "x\n"})
        git(seed, "add", "-A")
        git(seed, "commit", "-q", "-m", "base")
        git(seed, "push", "-q", "origin", "main")
        clone = os.path.join(self.cap, "repos", name)
        git(self.tmp, "clone", "-q", origin, clone)
        return clone

    def commit(self, clone: str, msg: str, files: Dict[str, str]) -> str:
        """One pushed commit on the clone's main; answers its sha."""
        write(clone, files)
        git(clone, "add", "-A")
        git(clone, "commit", "-q", "-m", msg)
        git(clone, "push", "-q", "origin", "main")
        return git(clone, "rev-parse", "HEAD")

    def side(self, clone: str, msg: str) -> str:
        """One commit on a branch no default branch carries; answers its sha."""
        git(clone, "checkout", "-q", "-b", "side-" + msg.replace(" ", "-"))
        write(clone, {msg.replace(" ", "_") + ".txt": "side\n"})
        git(clone, "add", "-A")
        git(clone, "commit", "-q", "-m", msg)
        git(clone, "checkout", "-q", "main")
        return git(clone, "rev-parse", "side-" + msg.replace(" ", "-"))

    def tag(self, clone: str, name: str, commit: str, push: bool = True) -> None:
        """One annotated, pushed release tag at commit."""
        git(clone, "tag", "-a", "-m", name, name, commit)
        if push:
            git(clone, "push", "-q", "origin", "refs/tags/" + name)

    def resolve(self, hub: Optional[FakeHub], batches_file: str = "", pre_release: str = "",
            args: Optional[List[str]] = None) -> Tuple[int, Dict[str, Any], Optional[Dict[str, Any]]]:
        """release.py over this capsule; answers (exit code, the result file,
        the pins file when written)."""
        out = os.path.join(self.cap, "release-batch.json")
        pins = os.path.join(self.cap, "release-pins.json")
        argv = ["--capsule", self.cap, "--out", out, "--pins-out", pins]
        if batches_file:
            argv += ["--batches", batches_file]
        if pre_release:
            argv += ["--pre-release", pre_release]
        argv += args or []
        code = release.main(argv, self.env, batches_read=hub)
        res = read_json(out)
        pinsdoc: Optional[Dict[str, Any]] = None
        if os.path.exists(pins):
            pinsdoc = read_json(pins)
        self.calls = hub.calls if hub is not None else []
        return code, res, pinsdoc

    def owner_world(self, extra: List[str] = []) -> Tuple[str, Dict[str, str]]:
        """The owner repo app with two commits on main (the pin is the
        older one, so the batch pins a commit the default branch's head has
        moved past - the shape the origin-head release refused)."""
        app = self.repo("app", owner=True)
        for n in extra:
            self.repo(n)
        first = self.commit(app, "first", {"first.txt": "1\n"})
        second = self.commit(app, "second", {"second.txt": "2\n"})
        return app, {"first": first, "second": second}

    # -- 1. a green batch: the latest one wins, the pins are written -------

    def test_latest_green_batch_wins_and_pins_are_written(self) -> None:
        app, heads = self.owner_world()
        # main moves past the pin: the release object is still the pin
        self.commit(app, "third", {"third.txt": "3\n"})
        hub = FakeHub([
            batch("wi_b1", "green", {"app": heads["first"]}, "2026-11-17T08:00:00Z", seq=1),
            batch("wi_r0", "red", {"app": heads["second"]}, "2026-11-17T12:00:00Z", seq=2),
            batch("wi_b2", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z", seq=3),
        ])
        code, res, pins = self.resolve(hub)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["verdict"], "pass")
        self.assertEqual(self.calls, ["list --status wrapped --bundle integrate --limit 1000"])
        self.assertEqual(pins, {"batch": "wi_b2", "repos": {"app": heads["second"]}})
        d = res["details"]
        self.assertEqual((d["batch"], d["decision"], d["owner"], d["owner_pin"]),
                         ("wi_b2", "fresh", "app", heads["second"]))
        self.assertEqual(res["heads"], {"app": heads["second"]})
        self.assertNotIn("red_batches_after", pins, "the red batch predates the green one")
        self.assertNotIn("version", d)

    def test_red_batches_after_the_green_one_are_reported(self) -> None:
        _, heads = self.owner_world()
        hub = FakeHub([
            batch("wi_r2", "red", {"app": heads["second"]}, "2026-11-19T08:00:00Z", seq=3),
            batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z", seq=1),
            batch("wi_r1", "red", {"app": heads["first"]}, "2026-11-17T08:00:00Z", seq=2),
        ])
        code, res, pins = self.resolve(hub)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(pins, {"batch": "wi_g", "repos": {"app": heads["second"]},
                                "red_batches_after": ["wi_r2"]})

    # -- 2. no green batch: refuse ------------------------------------------

    def test_no_green_batch_refuses(self) -> None:
        self.owner_world()
        for name, items in (
            ("only red and empty", [batch("wi_r", "red", {"app": "0" * 40}, "2026-11-18T08:00:00Z"),
                                    batch("wi_e", "empty", {}, "2026-11-18T09:00:00Z")]),
            ("nothing at all", []),
        ):
            with self.subTest(name):
                code, res, pins = self.resolve(FakeHub(items))
                self.assertEqual(code, 1)
                self.assertEqual(res["verdict"], "fail")
                self.assertTrue(res["summary"].startswith("PF-RELEASE-NO_GREEN_BATCH"), res["summary"])
                self.assertIsNone(pins)

    # -- 3. the owner pin already carries its release tag -------------------

    def test_one_tag_on_the_owner_pin_resumes_whatever_claimed_it(self) -> None:
        app, heads = self.owner_world()
        self.commit(app, "third", {"third.txt": "3\n"})
        self.tag(app, "v1.2.3", heads["second"])
        # a list an earlier attempt of this work item kept: it lacks the tag
        pre = os.path.join(self.cap, "pre.txt")
        write(os.path.dirname(pre), {"pre.txt": ""})
        hub = FakeHub([batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z")])
        code, res, pins = self.resolve(hub, pre_release=pre)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(res["verdict"], "pass")
        d = res["details"]
        self.assertEqual((d["decision"], d["version"]), ("resume", "v1.2.3"))
        self.assertEqual(pins, {"batch": "wi_g", "repos": {"app": heads["second"]}})
        self.assertIn("never a second version", res["summary"])

    def test_two_tags_on_the_owner_pin_refuse(self) -> None:
        app, heads = self.owner_world()
        self.tag(app, "v1.2.3", heads["second"])
        self.tag(app, "v1.2.4", heads["second"])
        hub = FakeHub([batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z")])
        code, res, pins = self.resolve(hub)
        self.assertEqual(code, 1)
        self.assertEqual(res["verdict"], "fail")
        self.assertTrue(res["summary"].startswith("PF-RELEASE-ALREADY_RELEASED"), res["summary"])
        for t in ("v1.2.3", "v1.2.4"):
            self.assertIn(t, res["summary"])
        self.assertIsNone(pins)

    def test_a_new_tag_elsewhere_with_a_kept_list_refuses(self) -> None:
        app, heads = self.owner_world()
        self.tag(app, "v0.9.0", heads["first"])
        pre = os.path.join(self.cap, "pre.txt")
        write(os.path.dirname(pre), {"pre.txt": ""})  # recorded before v0.9.0 existed
        hub = FakeHub([batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z")])
        code, res, pins = self.resolve(hub, pre_release=pre)
        self.assertEqual(code, 1)
        self.assertEqual(res["verdict"], "fail")
        self.assertTrue(res["summary"].startswith("PF-RELEASE-ALREADY_RELEASED"), res["summary"])
        self.assertIn("v0.9.0", res["summary"])
        self.assertIn("not on the owner pin", res["summary"])
        self.assertIsNone(pins)

    # -- 4. a pin no longer on the default branch ---------------------------

    def test_a_pin_off_the_default_branch_refuses(self) -> None:
        app, heads = self.owner_world()
        off = self.side(app, "off the branch")
        for name, pin in (("a side-branch commit", off), ("a commit the clone does not know", "0" * 39 + "1")):
            with self.subTest(name):
                code, res, pins = self.resolve(FakeHub([batch("wi_g", "green", {"app": pin}, "2026-11-18T08:00:00Z")]))
                self.assertEqual(code, 1)
                self.assertEqual(res["verdict"], "fail")
                self.assertTrue(res["summary"].startswith("PF-RELEASE-PIN_OFF_DEFAULT"), res["summary"])
                self.assertIn("app", res["summary"])
                self.assertIsNone(pins)

    # -- 5. multi-repo: the whole batch is the release object ----------------

    def test_multi_repo_pins_cover_the_batch_and_exclude_the_uncovered(self) -> None:
        app, heads = self.owner_world(extra=["lib", "extra"])
        lib = os.path.join(self.cap, "repos", "lib")
        libhead = self.commit(lib, "lib change", {"lib2.txt": "2\n"})
        hub = FakeHub([batch("wi_g", "green", {"app": heads["second"], "lib": libhead}, "2026-11-18T08:00:00Z")])
        code, res, pins = self.resolve(hub)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(pins, {"batch": "wi_g", "repos": {"app": heads["second"], "lib": libhead}})
        self.assertEqual(res["details"]["excluded_repos"], ["extra"])
        self.assertEqual(res["heads"], {"app": heads["second"], "lib": libhead})

    def test_an_uncovered_owner_refuses(self) -> None:
        _, heads = self.owner_world(extra=["lib"])
        lib = os.path.join(self.cap, "repos", "lib")
        libhead = self.commit(lib, "lib change", {"lib2.txt": "2\n"})
        hub = FakeHub([batch("wi_g", "green", {"lib": libhead}, "2026-11-18T08:00:00Z")])
        code, res, pins = self.resolve(hub)
        self.assertEqual(code, 1)
        self.assertEqual(res["verdict"], "fail")
        self.assertTrue(res["summary"].startswith("PF-RELEASE-UNPINNED_OWNER"), res["summary"])
        self.assertIn("app", res["summary"])
        self.assertIsNone(pins)

    def test_owner_resolution(self) -> None:
        # two owners: a loud failure naming every candidate
        self.repo("app", owner=True)
        self.repo("other", owner=True)
        code, res, pins = self.resolve(FakeHub([]))
        self.assertEqual(code, 1)
        self.assertTrue(res["summary"].startswith("PF-RELEASE-AMBIGUOUS_OWNER"), res["summary"])
        for n in ("app", "other"):
            self.assertIn(n, res["summary"])
        self.assertIsNone(pins)
        # zero owners: a release work item without a procedure is an error
        self.cap = self.new_dir("capsule2")
        os.makedirs(os.path.join(self.cap, "repos", "lib"), exist_ok=True)
        code, res, _ = self.resolve(FakeHub([]))
        self.assertEqual(code, 1)
        self.assertIn("no repo in the capsule carries .polyforge/release.sh", res["summary"])

    # -- 6. not judged: fail closed ------------------------------------------

    def test_unreadable_inputs_exit_2(self) -> None:
        _, heads = self.owner_world()
        cases: List[Tuple[str, Any, str]] = [
            ("the hub", FakeHub([], fail=RuntimeError("pf step hub list: exit status 1: DAEMON-403")), "hub"),
            ("an empty main", FakeHub([batch("wi_g", "green", {}, "2026-11-18T08:00:00Z")]), "main"),
            ("a missing closed_at", FakeHub([{"id": "wi_g", "status": "wrapped",
                                              "attrs": {"integration_batch": {"result": "green", "main": {"app": heads["second"]}}}}]), "closed_at"),
            ("a short sha", FakeHub([batch("wi_g", "green", {"app": "deadbeef"}, "2026-11-18T08:00:00Z")]), "full commit id"),
            ("a repo no clone is of", FakeHub([batch("wi_g", "green", {"ghost": "0" * 40}, "2026-11-18T08:00:00Z")]), "ghost"),
            ("an unreadable batch record", FakeHub([{"id": "wi_g", "status": "wrapped", "attrs": {}}]), "integration_batch"),
        ]
        for name, hub, want in cases:
            with self.subTest(name):
                code, res, pins = self.resolve(hub)
                self.assertEqual(code, 2, res["summary"])
                self.assertEqual(res["verdict"], "error")
                self.assertIn(want, res["summary"])
                self.assertIsNone(pins)

    def test_an_unresolvable_default_branch_exits_2(self) -> None:
        _, heads = self.owner_world()
        app = os.path.join(self.cap, "repos", "app")
        git(app, "update-ref", "-d", "refs/remotes/origin/HEAD")
        code, res, _ = self.resolve(FakeHub([batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z")]))
        self.assertEqual(code, 2)
        self.assertEqual(res["verdict"], "error")
        self.assertIn("default branch", res["summary"])

    # -- 7. the CLI's own shapes ----------------------------------------------

    def test_snapshot_input_and_result_schema(self) -> None:
        _, heads = self.owner_world()
        snap = os.path.join(self.cap, "batches.json")
        write(os.path.dirname(snap), {"batches.json": json.dumps(
            {"items": [batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z")]})})
        code, res, pins = self.resolve(None, batches_file=snap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(pins, {"batch": "wi_g", "repos": {"app": heads["second"]}})
        self.assertEqual(res["command"], "release.py")
        self.assertEqual(res["version"], LIBRARY)
        with open(os.path.join(os.path.dirname(os.path.abspath(release.__file__)), "schema", "pf-step-result-1.json"),
                  encoding="utf-8") as f:
            schema = json.load(f)
        errs = schemacheck.errors(schema, res)
        self.assertEqual(errs, [], str(errs))
        # the snapshot file may also be a bare array
        write(os.path.dirname(snap), {"batches.json": json.dumps(
            [batch("wi_g", "green", {"app": heads["second"]}, "2026-11-18T08:00:00Z")])})
        code, res, pins = self.resolve(None, batches_file=snap)
        self.assertEqual(code, 0, res["summary"])
        self.assertEqual(pins, {"batch": "wi_g", "repos": {"app": heads["second"]}})


if __name__ == "__main__":
    unittest.main()
