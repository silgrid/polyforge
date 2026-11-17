"""gate.py's run: the tree's own full plan — or, a tree that carries no
engine, the lane's (this script's own neighbour, mounted read-only) —
every command, the engine included, through the container engine
(tests.fakebox's fake runs each "container" command on the host and
records it); the strict verdict; a gate
that cannot be judged; the plan digest; the container's lifetime, sweep and
removal; a deadline or an interrupt never red. The Go side's twin was
verifyrun's w405_gate_test.go, w405_gate_green_test.go (the run half) and
w405_gate_lifecycle_test.go."""

import hashlib
import json
import os
import signal
import tempfile
import threading
import time
import unittest
from typing import Any, Dict, List, Optional, Sequence, Tuple

import gate
from pfstep import container, duration

from .fakebox import FakeRuntime
from .gatefix import (
    GATE_DOCKERFILE,
    HAVE_BASH,
    HAVE_GIT,
    HAVE_PY,
    SCRIPTS,
    boxed_bash,
    commit_all,
    engine_plan,
    gate_clone,
    git_t,
    new_gate,
    run_gate,
    run_main,
    write_files,
)


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class GateRunTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.fb = FakeRuntime(os.path.join(self.root, "box-tmp"))
        self.logs = os.path.join(self.root, "logs")

    def tearDown(self) -> None:
        self._td.cleanup()

    def clone(self, conf: str, dockerfile: bool = True) -> str:
        return gate_clone(self.root, conf, dockerfile)

    def test_the_trees_full_plan_runs_inside_the_container(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo fast-one\ncheck=named|fast|echo named-fast\nfull_command=echo full-one\n"
                       "full_command=test -n \"$TMPDIR\" && test -d \"$TMPDIR\" && test -z \"${PF_SECRET:-}${TMUX:-}${GH_TOKEN:-}\""
                       " && test \"$IMAGE_ONLY\" = from-image && test \"$LANG\" = C.UTF-8\n")
        r = run_gate(d, self.fb, self.logs)
        res = r.res
        self.assertEqual(res.incomplete, "", r.progress)
        self.assertTrue(res.passed, r.progress)
        want = engine_plan(d)
        self.assertEqual(len(want), 4, "the conf's 2 fast + 2 full commands")
        self.assertEqual(boxed_bash(self.fb), want, "the commands run in the container are the engine's full plan")
        self.assertEqual([c.name for c in res.commands], ["fast-1", "named", "full-1", "full-2"])
        engine = os.path.realpath(os.path.join(d, gate.GATE_ENGINE_REL))
        saw = False
        for cid, workdir, env, argv in self.fb.execs:
            self.assertEqual((cid, workdir), ("cid-1", d), argv)
            for kv in env:
                k = kv.partition("=")[0]
                self.assertTrue(k == "TMPDIR" or k in container.CONTAINER_ENV_ALLOW, "%s reached the container" % k)
            if argv == [engine, "--json", "--full", "--base", "HEAD"]:
                saw = True
        self.assertTrue(saw, "the engine ran in the container with --json --full --base HEAD")
        self.assertEqual(res.engine, engine + " --json --full --base HEAD")
        # a tree that carries its own engine and gate id is served by them:
        # both sources read "tree" in the result
        self.assertEqual((res.engine_source, res.gate_id_source), ("tree", "tree"))
        self.assertEqual((res.as_dict()["engine_source"], res.as_dict()["gate_id_source"]), ("tree", "tree"))
        tag = container.image_tag("app", GATE_DOCKERFILE.encode())
        self.assertEqual([(b[0], b[1]) for b in self.fb.builds], [(tag, GATE_DOCKERFILE.encode())])
        self.assertEqual((res.image, res.built), (tag, True))
        self.assertEqual(len(self.fb.starts), 1)
        s = self.fb.starts[0]
        self.assertEqual([(b.path, b.read_only) for b in s.binds], [(d, False)], "the gate mounts the clone alone")
        self.assertEqual(s.labels[container.LABEL_DAEMON], container.GATE_OWNER)
        self.assertNotEqual(s.labels[container.LABEL_BOOT], "")
        self.assertEqual(s.labels[container.LABEL_REPO], "app")
        self.assertEqual(s.labels[container.LABEL_WI], gate.GATE_WI)
        self.assertEqual(s.volumes, {"pf-verify-cache-app-" + hashlib.sha256(GATE_DOCKERFILE.encode()).hexdigest()[:32]: "/cache"})
        self.assertEqual((s.cpus, s.memory, s.pids), (container.default_cpus(), "16g", 4096))
        self.assertEqual(self.fb.removes, ["cid-1"], "the gate removes its container")
        self.assertEqual([n for n in os.listdir(os.path.join(d, ".git")) if n.startswith("pf-gate-probe-")], [])
        self.assertEqual(git_t(d, "status", "--porcelain"), "", "the gate dirtied the clone")
        self.assertIn("gate: PASS - 4 commands passed", r.progress)

    def test_it_stops_at_the_first_failure_unless_keep_going(self) -> None:
        conf = "stack=none\nfast_gate=echo one\nfull_command=echo the-failure-says-why; exit 3\nfull_command=echo after\n"
        d = self.clone(conf)
        r = run_gate(d, self.fb, self.logs)
        self.assertEqual(r.res.incomplete, "")
        self.assertFalse(r.res.passed)
        self.assertEqual((r.res.commands[1].exit, r.res.commands[1].not_run), (3, False))
        self.assertTrue(r.res.commands[2].not_run, "the command after the failure ran")
        self.assertEqual(len(boxed_bash(self.fb)), 2)
        for want in ("full-1 FAIL - exit 3", "    the-failure-says-why", "gate: FAIL - 1 of 3 commands failed"):
            self.assertIn(want, r.progress)
        self.assertEqual(gate.fail_summary(r.res), "1 of 3 commands failed: full-1 (exit 3)")
        fb2 = FakeRuntime(os.path.join(self.root, "box-tmp2"))
        r2 = run_gate(d, fb2, os.path.join(self.root, "logs2"), keep_going=True)
        self.assertFalse(r2.res.passed)
        self.assertEqual((r2.res.commands[2].not_run, r2.res.commands[2].exit), (False, 0), "keep-going runs the rest")

    def test_an_unmet_prerequisite_fails_never_skips(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\ncheck=needs|full|echo ran-needs\ncheck_requires_cmd=needs|no-such-tool-w405\n")
        r = run_gate(d, self.fb, self.logs)
        self.assertFalse(r.res.passed)
        self.assertEqual(r.res.incomplete, "")
        c = r.res.commands[-1]
        self.assertEqual(c.name, "needs")
        self.assertIn("no-such-tool-w405", c.unmet)
        self.assertNotIn("echo ran-needs", boxed_bash(self.fb))
        self.assertIn("needs FAIL - declared precondition unmet", r.progress)

    def test_an_incomplete_gate_is_never_a_pass(self) -> None:
        conf = "stack=none\nfast_gate=echo one\n"

        class WrongMount(FakeRuntime):
            def exec_argv(self, cid: str, workdir: str, env: Sequence[str], argv: Sequence[str]) -> List[str]:
                if len(argv) == 2 and argv[0] == "cat" and "pf-gate-probe-" in argv[1]:
                    argv = ["cat", "/dev/null"]
                return super().exec_argv(cid, workdir, env, argv)

        for name, dockerfile, mk, want, started in (
            ("no Dockerfile at HEAD", False, FakeRuntime, "carries no " + container.DOCKERFILE_VERIFY, False),
            ("engine unavailable", True, FakeRuntime, "container engine is unavailable", False),
            ("another directory at the clone's path", True, WrongMount, "does not see this clone", True),
        ):
            with self.subTest(name):
                root = tempfile.mkdtemp(dir=self.root)
                d = gate_clone(root, conf, dockerfile)
                fb = mk(os.path.join(root, "box-tmp"))
                if name == "engine unavailable":
                    fb.unavailable = "Cannot connect to the Docker daemon"
                r = run_gate(d, fb, os.path.join(root, "logs"))
                self.assertFalse(r.res.passed)
                self.assertIn(want, r.res.incomplete)
                self.assertEqual(bool(fb.starts), started)
                self.assertEqual(boxed_bash(fb), [])
                if started:
                    self.assertEqual(fb.removes, ["cid-1"])
                self.assertIn("gate: INCOMPLETE", r.progress)

    def test_no_engine_and_an_empty_plan_are_not_judged(self) -> None:
        # an empty plan is never judged, whichever engine planned it
        d = self.clone("stack=none\n")
        r = run_gate(d, self.fb, self.logs)
        self.assertFalse(r.res.passed)
        self.assertIn("planned no command", r.res.incomplete)
        # a tree that lost its engine is still judged: the lane's engine
        # plans it — here the same empty plan
        os.remove(os.path.join(d, gate.GATE_ENGINE_REL))
        commit_all(d, "no engine")
        r = run_gate(d, FakeRuntime(os.path.join(self.root, "t2")), os.path.join(self.root, "l2"))
        self.assertFalse(r.res.passed)
        self.assertIn("planned no command", r.res.incomplete, "the lane's engine planned the empty plan")
        self.assertEqual(r.res.engine_source, "lane")

    def test_a_tree_without_an_engine_falls_back_to_the_lanes(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo lane-fast\nfull_command=echo lane-full\n",
                       name="fallback", engine=False, gate_id=False)
        r = run_gate(d, self.fb, self.logs)
        res = r.res
        self.assertEqual(res.incomplete, "", r.progress)
        self.assertTrue(res.passed, r.progress)
        self.assertEqual([c.name for c in res.commands], ["fast-1", "full-1"])
        # the engine is the lane's — this script's own neighbour — asked
        # in the container exactly as the tree's is
        lane = os.path.join(SCRIPTS, "verify-scope")
        want = [lane] if os.access(lane, os.X_OK) else ["python3", lane]
        saw = False
        for _, _, _, argv in self.fb.execs:
            if argv == want + ["--json", "--full", "--base", "HEAD"]:
                saw = True
        self.assertTrue(saw, "the lane's engine ran in the container with --json --full --base HEAD")
        self.assertEqual(res.engine, " ".join(want + ["--json", "--full", "--base", "HEAD"]))
        s = self.fb.starts[0]
        self.assertEqual([(b.path, b.read_only) for b in s.binds], [(d, False), (SCRIPTS, True)],
                         "the fallback mounts the lane's engine directory read-only beside the clone")
        # both sources are recorded, and neither the lane's path nor the
        # sources entered the plan (AC5: the plan digest carries neither)
        self.assertEqual((res.engine_source, res.gate_id_source), ("lane", "lane"))
        self.assertEqual((res.as_dict()["engine_source"], res.as_dict()["gate_id_source"]), ("lane", "lane"))
        assert res.plan is not None
        self.assertNotIn(SCRIPTS, json.dumps(res.plan))
        self.assertNotIn("engine_source", json.dumps(res.plan))

    def test_a_failing_fallback_is_a_failure_not_an_incomplete(self) -> None:
        d = gate_clone(self.root, "stack=none\nfast_gate=echo one\nfull_command=echo the-failure-says-why; exit 3\n",
                       name="red-fallback", engine=False, gate_id=False)
        r = run_gate(d, self.fb, self.logs)
        self.assertEqual(r.res.incomplete, "", r.progress)
        self.assertFalse(r.res.passed)
        self.assertEqual(r.res.commands[-1].exit, 3)
        self.assertIn("gate: FAIL - 1 of 2 commands failed", r.progress)

    def test_the_engine_and_the_gate_id_fall_back_independently(self) -> None:
        conf = "stack=none\nfast_gate=echo one\n"
        with self.subTest("the tree's engine, no tools/gate-id"):
            d = gate_clone(self.root, conf, name="no-gate-id", gate_id=False)
            r = run_gate(d, FakeRuntime(os.path.join(self.root, "t-no-gate-id")), os.path.join(self.root, "l-no-gate-id"))
            self.assertTrue(r.res.passed, r.progress)
            self.assertEqual((r.res.engine_source, r.res.gate_id_source), ("tree", "lane"))
        with self.subTest("no engine, the tree's tools/gate-id"):
            d = gate_clone(self.root, conf, name="no-engine", engine=False)
            r = run_gate(d, FakeRuntime(os.path.join(self.root, "t-no-engine")), os.path.join(self.root, "l-no-engine"))
            self.assertTrue(r.res.passed, r.progress)
            self.assertEqual((r.res.engine_source, r.res.gate_id_source), ("lane", "tree"))

    def test_it_records_its_runner_and_plan_digest(self) -> None:
        conf = "stack=none\nfast_gate=echo one\ncheck=needs|full|echo two\ncheck_requires_cmd=needs|sh\ncheck_timeout=needs|90s\n"
        d = self.clone(conf)
        self.fb.commands["sh"] = True
        r = run_gate(d, self.fb, self.logs)
        res = r.res
        self.assertTrue(res.passed, r.progress)
        self.assertEqual((res.runner, res.runner_version), (gate.GATE_RUNNER, gate.GATE_RUNNER_VERSION))
        self.assertEqual(res.commit, git_t(d, "rev-parse", "HEAD"))
        self.assertTrue(res.clean)
        self.assertRegex(res.plan_digest, r"\A[0-9a-f]{64}\Z")
        assert res.plan is not None
        self.assertEqual(gate.plan_digest(res.plan), res.plan_digest)
        tag = container.image_tag("app", GATE_DOCKERFILE.encode())
        self.assertEqual((res.plan["image"], res.plan["cpus"], res.plan["memory"], res.plan["pids"]),
                         (tag[tag.rfind(":") + 1:], container.format_cpus(container.default_cpus()), "16g", 4096))
        cmds = res.plan["commands"]
        self.assertEqual([(c["name"], c["phase"], c["timeout"]) for c in cmds], [("fast-1", "fast", "20m0s"), ("needs", "full", "1m30s")])
        self.assertEqual(cmds[1]["requires_cmd"], ["sh"])
        # plan-only: the same digest through the same path, no plan command run
        fb2 = FakeRuntime(os.path.join(self.root, "t2"))
        fb2.commands["sh"] = True
        po = run_gate(d, fb2, os.path.join(self.root, "l2"), plan_only=True).res
        self.assertEqual((po.incomplete, po.passed, po.plan_digest, len(po.commands)), ("", False, res.plan_digest, 0))
        self.assertEqual(boxed_bash(fb2), [])
        self.assertEqual((len(fb2.starts), fb2.removes), (1, ["cid-1"]), "the plan is the engine's, asked in the container")
        # what the plan is made of moves the digest; the cpu limit is one
        # past the default, which follows this machine's cores and so can
        # equal any fixed value
        for name, kw in (("cpus", {"cpus": container.default_cpus() + 1}), ("memory", {"memory": "4g"}), ("pids", {"pids": 100}),
                         ("timeout", {"command_timeout": duration.MINUTE})):
            with self.subTest(name):
                fbx = FakeRuntime(os.path.join(self.root, "t-" + name))
                fbx.commands["sh"] = True
                rx = run_gate(d, fbx, os.path.join(self.root, "l-" + name), plan_only=True, **kw).res
                self.assertNotIn(rx.plan_digest, ("", res.plan_digest))

    def test_the_plan_digest_ignores_where_the_clone_lives(self) -> None:
        from pfstep.engine import EngineCommand

        def plan(d: str, image: str) -> str:
            rep = container.ContainerReport(image=image, built=False, build_log="", name="n", cpus="8", memory="8g", pids=4096)
            cmds = [EngineCommand(name="audit", phase="full", cmd="sh '" + d + "/tools/audit-deps' --go")]
            p = gate.plan_of(d, rep, cmds, lambda c: duration.MINUTE)
            self.assertEqual(p["commands"][0]["cmd"], "sh '<clone>/tools/audit-deps' --go")
            self.assertEqual(p["image"], "0123456789abcdef0123456789abcdef")
            return gate.plan_digest(p)

        self.assertEqual(plan("/one/app", "pf-verify-app:0123456789abcdef0123456789abcdef"),
                         plan("/elsewhere/other-name", "pf-verify-other-name:0123456789abcdef0123456789abcdef"))

    def test_a_fallbacks_plan_digest_ignores_where_the_lane_snapshot_lives(self) -> None:
        from pfstep.engine import EngineCommand

        def plan(lane: str) -> str:
            rep = container.ContainerReport(image="pf-verify-app:0123456789abcdef0123456789abcdef", built=False,
                                            build_log="", name="n", cpus="8", memory="8g", pids=4096)
            cmds = [EngineCommand(name="audit", phase="full",
                                  cmd="sh '" + lane + "/audit-deps' --go --ledger '/one/app/.polyforge/audit-exemptions.conf'")]
            p = gate.plan_of("/one/app", rep, cmds, lambda c: duration.MINUTE, lane_dir=lane)
            self.assertEqual(p["commands"][0]["cmd"],
                             "sh '<engine>/audit-deps' --go --ledger '<clone>/.polyforge/audit-exemptions.conf'",
                             "the fallback's audit names <engine>, not the lane snapshot's path")
            return gate.plan_digest(p)

        # the same commit in two batch capsules: the lane snapshot lives at
        # another path in each, the digest is one
        self.assertEqual(plan("/capsule-one/workflows/_shared/scripts"),
                         plan("/capsule-two/elsewhere/_shared/scripts"))

    def test_a_fallbacks_audit_names_the_engine_not_the_snapshot(self) -> None:
        # stack=go (a real analyzer — stack=none pins none and no audit is
        # appended): the go module makes the engine append its audit, whose
        # command names the engine's own directory
        d = gate_clone(self.root, "stack=go\nfast_gate=echo one\n", name="audit-fallback", engine=False, gate_id=False)
        write_files(d, {"go.mod": "module example.invalid/app\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() {}\n"})
        commit_all(d, "go module")
        r = run_gate(d, FakeRuntime(os.path.join(self.root, "t-audit")), os.path.join(self.root, "l-audit"), plan_only=True)
        res = r.res
        self.assertEqual(res.incomplete, "", r.progress)
        assert res.plan is not None
        audit = [c for c in res.plan["commands"] if c["name"] == "audit"]
        self.assertEqual(len(audit), 1, "the lane's engine appended its dependency audit for the go module")
        self.assertEqual(audit[0]["cmd"], "sh '<engine>/audit-deps' --go")
        self.assertNotIn(SCRIPTS, json.dumps(res.plan), "the lane snapshot's path never enters the plan")

    def test_a_dirty_tree_is_recorded(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        write_files(d, {"stray.txt": "x\n"})
        r = run_gate(d, self.fb, self.logs)
        self.assertTrue(r.res.passed)
        self.assertFalse(r.res.clean)

    def test_the_container_lifetime_is_bounded_by_the_deadline(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        before = time.time()
        r = run_gate(d, self.fb, self.logs, deadline=40 * duration.MINUTE)
        self.assertTrue(r.res.passed, r.progress)
        s = self.fb.starts[0]
        self.assertGreater(s.lifetime, 39 * 60)
        self.assertLessEqual(s.lifetime, 40 * 60 + container.KEEP_ALIVE_GRACE)
        exp = int(s.labels[container.LABEL_EXPIRES])
        self.assertGreaterEqual(exp, int(before + 40 * 60) - 1)
        self.assertLessEqual(exp, int(time.time() + 40 * 60) + 1)

    def test_it_sweeps_its_owners_containers_past_their_deadline(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        now = int(time.time())
        self.fb.containers.update({
            "leaked": {container.LABEL_DAEMON: container.GATE_OWNER, container.LABEL_EXPIRES: str(now - 7200)},
            "running": {container.LABEL_DAEMON: container.GATE_OWNER, container.LABEL_EXPIRES: str(now + 7200)},
            "unlabeled": {container.LABEL_DAEMON: container.GATE_OWNER},
            "anothers": {container.LABEL_DAEMON: "/data/cell", container.LABEL_EXPIRES: str(now - 7200)},
        })
        r = run_gate(d, self.fb, self.logs)
        self.assertTrue(r.res.passed, r.progress)
        self.assertEqual(self.fb.removes, ["leaked", "cid-1"], "the leaked container, then the gate's own")
        self.assertEqual(self.fb.listed[0], ({container.LABEL_DAEMON: container.GATE_OWNER}, container.LABEL_EXPIRES))
        self.assertIn("removed container leaked", r.progress)
        self.assertIn("unlabeled", r.progress, "a gate's container without a deadline is reported")

    def test_the_deadline_is_not_judged_never_red(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\nfull_command=sleep 30\n")
        r = run_gate(d, self.fb, self.logs, deadline=4 * duration.SECOND)
        self.assertFalse(r.res.passed)
        self.assertNotEqual(r.res.incomplete, "", r.progress)
        self.assertNotIn("FAIL", r.progress)
        self.assertEqual(self.fb.removes, ["cid-1"])

    def test_an_interrupt_is_not_judged_never_red(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\nfull_command=echo started > \"$TMPDIR/../w405-started\"; sleep 30\n"
                       "full_command=echo after\n")
        marker = os.path.join(self.fb.tmp_root, "tmp", "w405-started")

        def interrupt() -> None:
            end = time.time() + 20
            while time.time() < end and not os.path.exists(marker):
                time.sleep(0.02)
            os.kill(os.getpid(), signal.SIGINT)

        threading.Thread(target=interrupt, daemon=True).start()
        r = run_gate(d, self.fb, self.logs)
        self.assertFalse(r.res.passed)
        self.assertIn("interrupted", r.res.incomplete)
        self.assertNotIn("FAIL", r.progress)
        self.assertTrue(r.res.commands[-1].not_run, "the command after the interrupt ran")
        self.assertIn("cid-1", self.fb.removes, "the interrupt removes the container")

    def test_the_steps_labels_inside_a_step(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        g = new_gate(self.fb, owner="/data/cell")
        self.assertTrue(g.run(d, "", self.logs).passed)
        self.assertEqual(self.fb.starts[0].labels[container.LABEL_DAEMON], "/data/cell")
        self.assertEqual(self.fb.listed[0][0], {container.LABEL_DAEMON: "/data/cell"})


class GateRulesTest(unittest.TestCase):
    def test_memory_rule(self) -> None:
        for v, ok in (("8g", True), ("12G", True), ("6m", True), ("6291456", True), ("5m", False), ("8 gigs", False),
                      ("", False), ("8gb", False), ("-1g", False), ("9" * 30 + "g", False)):
            with self.subTest(v):
                self.assertEqual(gate.check_memory(v) == "", ok, gate.check_memory(v))

    def test_round_seconds(self) -> None:
        self.assertEqual(gate.round_seconds(1_499_999_999), duration.SECOND)
        self.assertEqual(gate.round_seconds(1_500_000_000), 2 * duration.SECOND)
        self.assertEqual(gate.dur(gate.round_seconds(63_400_000_000)), "1m3s")

    def test_green_numbering_and_newest(self) -> None:
        refs = {"green/20261108-1": ("a", ""), "green/20261108-3": ("b", "c"), "green/20261107-9": ("d", "e"), "other": ("f", "")}
        self.assertEqual(gate.next_green_number(refs, "20261108"), 4)
        self.assertEqual(gate.next_green_number(refs, "20261109"), 1)
        self.assertEqual(gate.newest_green_of(["green/20261107-9", "green/20261108-2", "green/20261108-10", "green/x"]),
                         "green/20261108-10")
        self.assertEqual(gate.newest_green_of([]), "")


# a prepared image and its local ID, as a step's `image` parameter names
# them (polyforge#64)
IMAGE_REF = "registry.example/team/verify:2026-11"
IMAGE_ID = "sha256:" + "ab" * 32
OTHER_REF = "registry.example/team/verify:2026-12"
OTHER_ID = "sha256:" + "cd" * 32


@unittest.skipUnless(HAVE_GIT and HAVE_BASH and HAVE_PY, "git, bash and python3 needed")
class PreparedImageGateTest(unittest.TestCase):
    """The gate with a prepared image (the step's `image` parameter,
    polyforge#64), run through the command line as the skill runs it — the
    expected values are the ticket's rulings, not the implementation's own
    answers: nothing is built, the container starts by the image's local
    ID with --pull never, the plan's image half and the result record the
    digest and where the value came from (the flag wins over the step
    environment), a tree without a Dockerfile is gated too, a missing
    image is `not judged` carrying the fixed mark, and a command the
    image fails is a real failure — the container contract (the mount, the
    allowlist) is the default mode's."""

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.fb = FakeRuntime(os.path.join(self.root, "box-tmp"))
        self.fb.identities[IMAGE_REF] = (IMAGE_ID, [])
        self.logs = os.path.join(self.root, "logs")

    def tearDown(self) -> None:
        self._td.cleanup()

    def clone(self, conf: str, dockerfile: bool = True) -> str:
        return gate_clone(self.root, conf, dockerfile)

    def result_of(self, r: Any) -> Dict[str, Any]:
        return json.loads(r.stdout)["details"]["repos"]["app"]["result"]

    def test_the_prepared_image_runs_without_a_build(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        r = run_main(["--isolation", "container", "--json", "--image", IMAGE_REF], cwd=d, runtime=self.fb)
        self.assertEqual(r.exit, 0, r.stderr)
        res = self.result_of(r)
        self.assertTrue(res["passed"])
        self.assertEqual(self.fb.builds, [], "nothing is built")
        self.assertEqual(len(self.fb.starts), 1)
        s = self.fb.starts[0]
        self.assertEqual((s.image, s.pull_never), (IMAGE_ID, True), "started by the local ID, never pulled")
        self.assertEqual((res.get("image"), res.get("built"), res.get("image_digest"), res.get("image_source")),
                         (IMAGE_REF, False, IMAGE_ID, "flag"))
        self.assertEqual(res.get("plan", {}).get("image"), IMAGE_ID, "the plan's image half is the digest")
        self.assertEqual([(b.path, b.read_only) for b in s.binds], [(d, False)], "the clone alone is mounted")
        for _, _, env, _ in self.fb.execs:
            for kv in env:
                k = kv.partition("=")[0]
                self.assertTrue(k == "TMPDIR" or k in container.CONTAINER_ENV_ALLOW, "%s reached the container" % k)

    def test_the_step_environment_names_it_and_the_flag_wins(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        r = run_main(["--isolation", "container", "--json"], cwd=d, env={"PF_PARAM_IMAGE": IMAGE_REF}, runtime=self.fb)
        self.assertEqual(r.exit, 0, r.stderr)
        res = self.result_of(r)
        self.assertEqual(self.fb.builds, [], "the parameter names the image: nothing is built")
        self.assertEqual((res.get("image_digest"), res.get("image_source")), (IMAGE_ID, "param"))
        fb2 = FakeRuntime(os.path.join(self.root, "box-tmp2"))
        fb2.identities[OTHER_REF] = (OTHER_ID, [])
        r2 = run_main(["--isolation", "container", "--json", "--image", OTHER_REF], cwd=d,
                      env={"PF_PARAM_IMAGE": IMAGE_REF}, runtime=fb2)
        self.assertEqual(r2.exit, 0, r2.stderr)
        res2 = self.result_of(r2)
        self.assertEqual((res2.get("image_digest"), res2.get("image_source")), (OTHER_ID, "flag"),
                         "the command line wins over the step environment")

    def test_a_tree_without_a_dockerfile_is_gated_with_an_image(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n", dockerfile=False)
        r = run_main(["--isolation", "container", "--json", "--image", IMAGE_REF], cwd=d, runtime=self.fb)
        self.assertEqual(r.exit, 0, r.stderr)
        res = self.result_of(r)
        self.assertTrue(res["passed"])
        self.assertEqual((self.fb.builds, res.get("image_digest")), ([], IMAGE_ID))
        # without an image the gate still refuses such a tree: unchanged
        fb2 = FakeRuntime(os.path.join(self.root, "box-tmp2"))
        r2 = run_main(["--isolation", "container", "--json"], cwd=d, runtime=fb2)
        self.assertEqual(r2.exit, 2, "no Dockerfile and no image is still not judged")

    def test_a_missing_image_is_not_judged_with_the_fixed_mark(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        r = run_main(["--isolation", "container", "--json", "--image", "registry.example/nope:1"],
                     cwd=d, runtime=self.fb)
        self.assertEqual(r.exit, 2)
        summary = json.loads(r.stdout)["summary"] if r.stdout.strip() else ""
        self.assertIn(container.IMAGE_MISSING_MARK, summary)
        self.assertIn("registry.example/nope:1", summary)
        self.assertIn("prepared by a person", summary)
        self.assertEqual((self.fb.builds, self.fb.starts), ([], []))

    def test_a_failing_command_in_the_image_is_a_real_failure(self) -> None:
        d = self.clone("stack=none\nfull_command=echo the-failure-says-why; exit 3\n")
        r = run_main(["--isolation", "container", "--json", "--image", IMAGE_REF], cwd=d, runtime=self.fb)
        self.assertEqual(r.exit, 1, r.stderr)
        res = self.result_of(r)
        self.assertFalse(res["passed"])
        self.assertEqual(res["commands"][1]["exit"], 3)
        self.assertEqual(res.get("image_digest"), IMAGE_ID)

    def test_the_triage_opens_the_prepared_image_too(self) -> None:
        d = self.clone("stack=none\nfast_gate=echo one\n")
        lines: List[str] = []
        g = new_gate(self.fb, lines=lines)
        # the command line's route is pinned above; setting the field keeps
        # this test's red an assertion, not a missing constructor name
        g.image = IMAGE_REF
        sess = gate.Session.open(g, d, "app", self.logs)
        try:
            self.assertEqual(self.fb.builds, [], "the triage builds nothing either")
            self.assertEqual((self.fb.starts[0].image, self.fb.starts[0].pull_never), (IMAGE_ID, True))
        finally:
            sess.close()


if __name__ == "__main__":
    unittest.main()

