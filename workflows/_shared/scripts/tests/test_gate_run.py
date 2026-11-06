"""gate.py's run: the tree's own full plan, every command — the engine
included — through the container engine (tests.fakebox's fake runs each
"container" command on the host and records it); the strict verdict; a gate
that cannot be judged; the plan digest; the container's lifetime, sweep and
removal; a deadline or an interrupt never red. The Go side's twin was
verifyrun's w405_gate_test.go, w405_gate_green_test.go (the run half) and
w405_gate_lifecycle_test.go."""

import hashlib
import os
import signal
import tempfile
import threading
import time
import unittest
from typing import List, Sequence

import gate
from pfstep import container, duration

from .fakebox import FakeRuntime
from .gatefix import (
    GATE_DOCKERFILE,
    HAVE_BASH,
    HAVE_GIT,
    HAVE_PY,
    boxed_bash,
    commit_all,
    engine_plan,
    gate_clone,
    git_t,
    new_gate,
    run_gate,
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
        d = self.clone("stack=none\n")
        r = run_gate(d, self.fb, self.logs)
        self.assertFalse(r.res.passed)
        self.assertIn("planned no command", r.res.incomplete)
        os.remove(os.path.join(d, gate.GATE_ENGINE_REL))
        commit_all(d, "no engine")
        r = run_gate(d, FakeRuntime(os.path.join(self.root, "t2")), os.path.join(self.root, "l2"))
        self.assertIn("carries no scope engine " + gate.GATE_ENGINE_REL, r.res.incomplete)

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
        # what the plan is made of moves the digest
        for name, kw in (("cpus", {"cpus": 4.0}), ("memory", {"memory": "4g"}), ("pids", {"pids": 100}),
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


if __name__ == "__main__":
    unittest.main()

