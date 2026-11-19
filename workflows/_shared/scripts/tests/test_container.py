"""pfstep.runner in the repo's verification container, over a fake engine
(tests/fakebox.py): the image's name, the labels, the limits' defaults
(D7: CPU min(8, cores), memory 16g, pids 4096 — parameters), the bounded
keep-alive (the container ends by itself a grace after the deadline), the
sweep of expired containers that touches only its own label's, and the
"environment not satisfied" answer when docker is missing — the Go
executor's behaviour (verifyrun.RunCommand, container.go, docker.go).
The real-docker proofs are in test_container_docker.py."""

import hashlib
import os
import re
import tempfile
import time
import unittest
from typing import Any, List, Sequence
from unittest import mock

from pfstep import container, proc, runner, tmpdir
from pfstep.container import Bind, ContainerOptions, ContainerSpec, DockerCLI
from pfstep.runner import CommandOptions, EnvironmentNotSatisfied, ExecutorError

from .capsuleutil import HAVE_GIT, TEST_DOCKERFILE, commit, head, make_capsule, path_id
from .fakebox import FakeRuntime

# The daemon's credentials (one sentinel each) beside the network setting a
# container command is given.
DAEMON_ENV = {
    "LANG": "C.UTF-8", "HTTPS_PROXY": "http://proxy.example:3128",
    "PF_API_KEY": "aihub-SENTINEL-py", "POLYFORGE_API_KEY": "aihub-SENTINEL-py",
    "GH_TOKEN": "gh-SENTINEL-py", "GITHUB_TOKEN": "gh-SENTINEL-py",
    "POLYFORGE_DATA_DIR": "/srv/pf-data", "PF_DAEMON_SOCKET": "/srv/pf-data/daemon.sock",
    "TMUX": "/tmp/tmux-0/default,1,0", "TMUX_PANE": "%1",
    "HOME": "/home/daemon", "PATH": "/host/bin",
}


def _read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


@unittest.skipUnless(HAVE_GIT, "git is not on PATH")
class ContainerRunTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.capsule = make_capsule(self.root, "app", {"a.txt": "a\n", container.DOCKERFILE_VERIFY: TEST_DOCKERFILE})
        self.clone = os.path.join(self.capsule, "repos", "app")
        self.log_dir = os.path.join(self.capsule, "runtime-state", "run", "2", "app")
        tmp_root = os.path.join(self.root, "box-tmp")
        os.mkdir(tmp_root)
        self.fb = FakeRuntime(tmp_root)
        self.said: List[str] = []

    def tearDown(self) -> None:
        self._td.cleanup()

    def opts(self, command: str, seconds: float = 60.0, **kw: object) -> CommandOptions:
        o = CommandOptions(
            capsule_dir=self.capsule, repo="app", command=command,
            deadline=time.time() + seconds, timeout=seconds, log_dir=self.log_dir, run=2,
            env=dict(DAEMON_ENV),
            container=ContainerOptions(runtime=self.fb, owner="/srv/pf-data", boot="boot-1", wi="wi_x", say=self.said.append),
        )
        for k, v in kw.items():
            setattr(o, k, v)
        return o

    def test_runs_in_the_repo_container(self) -> None:
        o = self.opts("echo smoke-out; exit 3")
        res = runner.run_command(o)
        self.assertEqual((res.exit, res.timed_out, res.not_started, res.host), (3, False, False, False))
        self.assertEqual((res.log, res.head, res.dirty), ("runtime-state/run/2/app/command.log", head(self.clone), False))
        self.assertIn("smoke-out", _read(os.path.join(self.capsule, res.log)))
        self.assertEqual(len(self.fb.starts), 1)
        st = self.fb.starts[0]
        tag = "pf-verify-app:" + hashlib.sha256(TEST_DOCKERFILE.encode()).hexdigest()[:32]
        self.assertEqual((st.image, res.image, res.container), (tag, tag, st.name))
        self.assertRegex(st.name, r"^pf-verify-wi_x-2-app-[0-9a-f]{8}$")
        self.assertEqual(st.labels, {
            container.LABEL_DAEMON: "/srv/pf-data", container.LABEL_BOOT: "boot-1", container.LABEL_WI: "wi_x",
            container.LABEL_RUN: "2", container.LABEL_REPO: "app", container.LABEL_EXPIRES: str(int(o.deadline)),
        })
        self.assertEqual(st.binds, [Bind(self.clone)], "the container mounts the clone alone")
        self.assertEqual(st.volumes, {"pf-verify-cache-app-" + hashlib.sha256(TEST_DOCKERFILE.encode()).hexdigest()[:32]
                                      + "-" + path_id(self.clone): "/cache"})
        self.assertEqual(self.fb.builds, [(tag, TEST_DOCKERFILE.encode(), {container.LABEL_REPO: "app"})])
        cmd = self.fb.command_execs("echo smoke-out")
        self.assertEqual(len(cmd), 1)
        self.assertEqual(cmd[0][1], self.clone, "the command runs from the clone's root")
        self.assertEqual(cmd[0][3], runner.BOX_TMP_WRAP + ["bash", "-c", "echo smoke-out; exit 3"])
        self.assertEqual(self.fb.removes, ["cid-1"], "the container is removed when the command ends")
        # the Go executor's log names: the mount probe is the first numbered log
        self.assertEqual(sorted(os.listdir(self.log_dir)), ["1.log", "command.log", "image-build.log"])
        self.assertTrue(_read(os.path.join(self.log_dir, "1.log")).startswith("$ cat " + os.path.join(self.clone, ".git", "pf-gate-probe-")))
        # a second run finds the image
        res2 = runner.run_command(self.opts("true", log_dir=os.path.join(self.capsule, "runtime-state", "run", "3", "app")))
        self.assertEqual((res2.exit, res2.image, len(self.fb.builds)), (0, tag, 1))
        self.assertIn("is present (built by an earlier run)", _read(os.path.join(self.capsule, "runtime-state", "run", "3", "app", "image-build.log")))

    def test_the_command_sees_only_the_allowlist(self) -> None:
        res = runner.run_command(self.opts("env"))
        seen = _read(os.path.join(self.capsule, res.log))
        self.assertIn("HTTPS_PROXY=http://proxy.example:3128", seen, "the allowlist works at all")
        self.assertIn("IMAGE_ONLY=from-image", seen)
        self.assertIn("HOME=/root", seen, "HOME is the image's")
        for mark in ("SENTINEL", "PF_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "POLYFORGE_DATA_DIR",
                     "PF_DAEMON_SOCKET", "TMUX=", "TMUX_PANE", "/home/daemon", "/host/bin"):
            self.assertNotIn(mark, seen)
            for e in self.fb.execs:
                self.assertNotIn(mark, "\n".join(e[2]))
        cmd = self.fb.command_execs("env")[0]
        self.assertEqual([kv.split("=")[0] for kv in cmd[2]], ["LANG", "HTTPS_PROXY", "TMPDIR"])

    def test_a_socket_in_a_go_temp_dir_under_the_container_tmpdir_fits(self) -> None:
        # The same budget as the host's (test_runner): Go's t.TempDir() under
        # the TMPDIR the container command is given, a unix socket in it,
        # within sun_path (107 bytes plus NUL); the name matches the host's.
        runner.run_command(self.opts("echo socket-budget"))
        env = self.fb.command_execs("echo socket-budget")[0][2]
        tmp = [kv[len("TMPDIR="):] for kv in env if kv.startswith("TMPDIR=")][0]
        sock = os.path.join(tmp, "TestAccounts_AddRefusesAMalformedRequestOrAnUnusablePassword4294967295", "001", "bff.sock")
        self.assertLessEqual(len(sock.encode("utf-8")), 107, sock)
        self.assertEqual(os.path.dirname(tmp), "/tmp", tmp)
        self.assertTrue(os.path.basename(tmp).startswith(tmpdir.DIR_PREFIX), tmp)

    def test_limits_default_and_are_parameters(self) -> None:
        runner.run_command(self.opts("true"))
        st = self.fb.starts[0]
        self.assertEqual((st.cpus, st.memory, st.pids), (container.default_cpus(), "16g", 4096))
        self.assertEqual(container.default_cpus(), min(8.0, float(container._host_cpus() or 8)))
        args = DockerCLI().start_args(st)
        for flag, want in (("--cpus", container.format_cpus(st.cpus)), ("--memory", "16g"), ("--memory-swap", "16g"), ("--pids-limit", "4096")):
            self.assertEqual(args[args.index(flag) + 1], want)
        o = self.opts("true")
        o.container.cpus, o.container.memory, o.container.pids = 2.5, "1g", 64
        runner.run_command(o)
        st2 = self.fb.starts[1]
        self.assertEqual((st2.cpus, st2.memory, st2.pids), (2.5, "1g", 64))

    def test_the_container_ends_by_itself_a_grace_after_the_deadline(self) -> None:
        o = self.opts("true", seconds=600.0)
        runner.run_command(o)
        st = self.fb.starts[0]
        want = 600.0 + container.KEEP_ALIVE_GRACE
        self.assertTrue(want - 60 < st.lifetime <= want, st.lifetime)
        args = DockerCLI().start_args(st)
        self.assertIn("--rm", args, "a bounded container is removed by the engine when it ends")
        self.assertEqual(args[-1], container.bounded_keep_alive(st.lifetime))
        self.assertEqual(st.labels[container.LABEL_EXPIRES], str(int(o.deadline)))

    def test_start_sweeps_only_its_own_expired_containers(self) -> None:
        now = int(time.time())
        own, other = "/srv/pf-data", "/srv/other-daemon"
        self.fb.containers.update({
            "own-expired": {container.LABEL_DAEMON: own, container.LABEL_EXPIRES: str(now - 100)},
            "own-running": {container.LABEL_DAEMON: own, container.LABEL_EXPIRES: str(now + 1000)},
            "own-no-deadline": {container.LABEL_DAEMON: own, container.LABEL_BOOT: "boot-0"},
            "other-expired": {container.LABEL_DAEMON: other, container.LABEL_EXPIRES: str(now - 100)},
        })
        runner.run_command(self.opts("true"))
        self.assertEqual(self.fb.removes, ["own-expired", "cid-1"])
        self.assertEqual(sorted(self.fb.containers), ["other-expired", "own-no-deadline", "own-running"])
        self.assertEqual(self.fb.listed[0], ({container.LABEL_DAEMON: own}, container.LABEL_EXPIRES))
        self.assertTrue(any("removed container own-expired" in s for s in self.said), self.said)

    def test_missing_docker_is_environment_not_satisfied(self) -> None:
        self.fb.unavailable = "Cannot connect to the Docker daemon"
        with self.assertRaises(EnvironmentNotSatisfied) as cm:
            runner.run_command(self.opts("true"))
        self.assertIn("the container engine is unavailable: Cannot connect to the Docker daemon", str(cm.exception))
        self.assertEqual(cm.exception.exit_code, 2)
        self.assertEqual((self.fb.starts, self.fb.builds, self.fb.listed), ([], [], []))
        # the real client, missing
        o = self.opts("true")
        o.container = ContainerOptions(runtime=DockerCLI(bin=os.path.join(self.root, "no-docker")))
        with self.assertRaises(EnvironmentNotSatisfied) as cm2:
            runner.run_command(o)
        self.assertIn("is not found", str(cm2.exception))
        # the default client when none is on either PATH
        empty = os.path.join(self.root, "empty-bin")
        os.mkdir(empty)
        with mock.patch("pfstep.container.shutil.which", return_value=None):
            cli = DockerCLI.for_env({"PATH": empty})
            self.assertEqual(cli.bin, "docker")
            with self.assertRaises(container.ContainerError):
                cli.available()

    def test_container_trouble_is_an_error_never_a_result(self) -> None:
        cases = {
            "build fails": ("build_err", "the build broke", "could not be prepared"),
            "start refused": ("start_err", "no space left on device", "no space left on device"),
            "lost mid-command": ("reap_err", "No such container: cid-1", "could not start in the verification container"),
        }
        for name, (attr, err, want) in cases.items():
            with self.subTest(name):
                self.fb = FakeRuntime(self.fb.tmp_root)
                setattr(self.fb, attr, err)
                with self.assertRaises(ExecutorError) as cm:
                    runner.run_command(self.opts("make smoke"))
                self.assertIn(want, str(cm.exception))
                self.assertNotIsInstance(cm.exception, EnvironmentNotSatisfied)
                if name != "lost mid-command":
                    self.assertEqual(self.fb.command_execs("make smoke"), [], "the command ran without its container")
                self.assertEqual(self.fb.removes, ["cid-1"] if self.fb.starts else [], "a started container is removed")
        # no Dockerfile at HEAD: nothing runs, on the host least of all
        os.remove(os.path.join(self.clone, container.DOCKERFILE_VERIFY))
        commit(self.clone, self.root, "drop the Dockerfile")
        self.fb = FakeRuntime(self.fb.tmp_root)
        with self.assertRaises(ExecutorError) as cm:
            runner.run_command(self.opts("make smoke"))
        self.assertIn("carries no .polyforge/Dockerfile.verify at HEAD", str(cm.exception))
        self.assertEqual((self.fb.starts, self.fb.execs), ([], []))

    def test_the_deadline_kills_the_command_and_a_passed_one_starts_nothing(self) -> None:
        start = time.monotonic()
        res = runner.run_command(self.opts("echo started; sleep 30", seconds=3.0))
        self.assertLess(time.monotonic() - start, 20)
        self.assertEqual((res.exit, res.timed_out, res.not_started), (runner.TIMEOUT_EXIT, True, False))
        self.assertTrue(self.fb.reaps[-1] and self.fb.reaps[-1][0].startswith("/tmp/" + tmpdir.DIR_PREFIX),
                        "what the command left in the container is reaped, its TMPDIR removed")
        self.fb = FakeRuntime(self.fb.tmp_root)
        res2 = runner.run_command(self.opts("echo never", seconds=-1.0))
        self.assertEqual((res2.exit, res2.timed_out, res2.not_started, res2.log), (runner.TIMEOUT_EXIT, True, True, ""))
        self.assertEqual(self.fb.starts, [])

    def test_a_clone_the_container_does_not_see(self) -> None:
        class WrongMount(FakeRuntime):
            def exec_argv(self, cid: str, workdir: str, env: Sequence[str], argv: Sequence[str]) -> List[str]:
                if argv and argv[0] == "cat":
                    argv = ["cat", "/dev/null"]
                return super().exec_argv(cid, workdir, env, argv)

        self.fb = WrongMount(self.fb.tmp_root)
        with self.assertRaises(ExecutorError) as cm:
            runner.run_command(self.opts("make smoke"))
        self.assertIn("the container does not see this clone at " + self.clone, str(cm.exception))
        self.assertEqual(self.fb.command_execs("make smoke"), [])
        self.assertEqual(self.fb.removes, ["cid-1"])
        self.assertEqual([n for n in os.listdir(os.path.join(self.clone, ".git")) if n.startswith("pf-gate-probe-")], [],
                         "the probe file is removed")

    def test_an_interrupt_removes_the_container(self) -> None:
        real_run = proc.run

        def interrupted(argv: Sequence[str], **kw: Any) -> proc.Completed:
            if "make smoke" in " ".join(argv):
                raise KeyboardInterrupt()
            return real_run(argv, **kw)

        with mock.patch("pfstep.runner.proc.run", side_effect=interrupted):
            with self.assertRaises(KeyboardInterrupt):
                runner.run_command(self.opts("make smoke"))
        self.assertEqual(self.fb.removes, ["cid-1"], "the interrupted run's container is removed")
        self.assertEqual(len(self.fb.reaps), 1, "only the probe is reaped: the container is removed, not reaped")

    def test_a_container_that_cannot_be_removed_is_said(self) -> None:
        self.fb.remove_err = "engine hung"
        res = runner.run_command(self.opts("true"))
        self.assertEqual(res.exit, 0, "the verdict does not change")
        self.assertTrue(any("was left behind" in s and "engine hung" in s for s in self.said), self.said)


class SweepTest(unittest.TestCase):
    def setUp(self) -> None:
        self.fb = FakeRuntime(tempfile.gettempdir())
        self.said: List[str] = []
        self.now = 2_000_000_000
        x, y = "owner-x", "owner-y"
        self.fb.containers = {
            "x-old": {container.LABEL_DAEMON: x, container.LABEL_BOOT: "boot-0", container.LABEL_EXPIRES: str(self.now - 1)},
            "x-now": {container.LABEL_DAEMON: x, container.LABEL_BOOT: "boot-1", container.LABEL_EXPIRES: str(self.now)},
            "x-later": {container.LABEL_DAEMON: x, container.LABEL_BOOT: "boot-1", container.LABEL_EXPIRES: str(self.now + 60)},
            "x-bad": {container.LABEL_DAEMON: x, container.LABEL_EXPIRES: "soon"},
            "x-none": {container.LABEL_DAEMON: x, container.LABEL_BOOT: "boot-0"},
            "y-old": {container.LABEL_DAEMON: y, container.LABEL_BOOT: "boot-0", container.LABEL_EXPIRES: str(self.now - 1)},
        }

    def test_expired_only_and_only_its_own(self) -> None:
        removed = container.sweep_expired(self.fb, "owner-x", self.now, False, self.said.append)
        self.assertEqual(removed, ["x-old"], "a deadline not yet passed (x-now, x-later) is a run going on now")
        self.assertIn("y-old", self.fb.containers)
        self.assertEqual([s for s in self.said if "carries no readable" in s], [], "not strict: passed over silently")
        self.said.clear()
        container.sweep_expired(self.fb, "owner-x", self.now, True, self.said.append)
        self.assertEqual(sorted(s.split()[1] for s in self.said if "carries no readable" in s), ["x-bad", "x-none"])
        self.assertEqual(sorted(self.fb.containers), ["x-bad", "x-later", "x-none", "x-now", "y-old"])

    def test_a_failing_listing_or_removal_costs_only_the_sweep(self) -> None:
        self.fb.list_err = "engine down"
        self.assertEqual(container.sweep_expired(self.fb, "owner-x", self.now, False, self.said.append), [])
        self.assertIn("none removed", self.said[0])
        self.fb.list_err, self.fb.remove_err = "", "busy"
        self.assertEqual(container.sweep_expired(self.fb, "owner-x", self.now, False, self.said.append), [])
        self.assertIn("could not remove container x-old", self.said[-1])


class NamesAndLimitsTest(unittest.TestCase):
    def test_image_volume_and_container_names(self) -> None:
        df = b"FROM x\n"
        self.assertEqual(container.image_tag("App", df), "pf-verify-app:" + hashlib.sha256(df).hexdigest()[:32])
        for raw, want in (("My Repo!", "my-repo"), ("..x..", "x"), ("", "repo"), ("___", "repo"), ("a.b_c-d", "a.b_c-d")):
            self.assertEqual(container.tag_safe(raw), want, raw)
        self.assertEqual(container.cache_volume("My Repo", df, "/srv/work/my repo"),
                         "pf-verify-cache-my-repo-" + hashlib.sha256(df).hexdigest()[:32] + "-" + path_id("/srv/work/my repo"))
        self.assertRegex(container.container_name("wi_x", 2, "App"), r"^pf-verify-wi_x-2-app-[0-9a-f]{8}$")
        self.assertRegex(container.container_name("", 1, "a"), r"^pf-verify-manual-1-a-[0-9a-f]{8}$")
        self.assertRegex(container.container_name("-a/b c.", 1, "a"), r"^pf-verify-a-b-c-1-a-[0-9a-f]{8}$")
        self.assertNotEqual(container.container_name("w", 1, "a"), container.container_name("w", 1, "a"))

    def test_default_cpus_capped_at_the_cores(self) -> None:
        for host, want in ((2, 2.0), (32, 8.0), (8, 8.0), (0, 8.0)):
            self.assertEqual(container.default_cpus(lambda: host), want, host)
        o = ContainerOptions(runtime=FakeRuntime("/tmp"), cpus=64.0).with_defaults({})
        self.assertEqual((o.cpus, o.memory, o.pids), (64.0, "16g", 4096), "a given limit is never capped")
        for x, s in ((8.0, "8"), (2.5, "2.5"), (0.01, "0.01"), (1e-05, "0.00001"), (12.0, "12"), (1e16, "10000000000000000")):
            self.assertEqual(container.format_cpus(x), s)

    def test_labels_default_from_the_step_environment(self) -> None:
        o = ContainerOptions(runtime=FakeRuntime("/tmp")).with_defaults({"PF_WI_ID": "wi_7"})
        self.assertEqual((o.owner, o.wi), (container.STEP_OWNER, "wi_7"))
        self.assertRegex(o.boot, r"^[0-9a-f]{16}$")
        bare = ContainerOptions(runtime=FakeRuntime("/tmp")).with_defaults({})
        self.assertEqual((bare.owner, bare.wi), (container.STEP_OWNER, ""))
        self.assertRegex(bare.boot, r"^[0-9a-f]{16}$")
        self.assertIsInstance(ContainerOptions().with_defaults({"PATH": "/nonexistent"}).runtime, DockerCLI)

    def test_keep_alive_scripts(self) -> None:
        self.assertIn("+ 905 )) && while :; do sleep 30;", container.bounded_keep_alive(905.9))
        self.assertIn("+ 1 )) && while :; do sleep 1;", container.bounded_keep_alive(0.2))
        self.assertIn("+ 50 )) && while :; do sleep 5;", container.bounded_keep_alive(50))
        spec = ContainerSpec(name="n", image="i:t", labels={"b": "2", "a": "1"},
                             binds=[Bind("/c"), Bind("/t", read_only=True)], volumes={"v": "/cache"}, cpus=2.5, memory="1g", pids=64)
        self.assertEqual(DockerCLI().start_args(spec), [
            "run", "--detach", "--init", "--name", "n", "--log-driver", "none",
            "--label", "a=1", "--label", "b=2",
            "--cpus", "2.5", "--memory", "1g", "--memory-swap", "1g", "--pids-limit", "64",
            "--mount", "type=bind,source=/c,target=/c", "--mount", "type=bind,source=/t,target=/t,readonly",
            "--mount", "type=volume,source=v,target=/cache",
            "--entrypoint", "sh", "i:t", "-c", container.KEEP_ALIVE_SCRIPT,
        ])
        self.assertEqual(DockerCLI(bin="/d").exec_argv("cid", "/w", ["A=1"], ["true"]),
                         ["/d", "exec", "--workdir", "/w", "--env", "A=1", "cid", "true"])

    def test_probe_status(self) -> None:
        self.assertTrue(container.probe_status("pf-probe:0\n"))
        self.assertFalse(container.probe_status("noise\npf-probe:1"))
        for bad in ("", "pf-probe:x"):
            with self.assertRaises(container.ContainerError):
                container.probe_status(bad)

    def test_box_removal_happens_once(self) -> None:
        fb = FakeRuntime("/tmp")
        report = container.ContainerReport(image="i", built=False, build_log="", name="n", cpus="1", memory="1g", pids=1)
        box = container.Box(fb, "cid-9", [], report)
        box.remove()
        box.remove()
        self.assertEqual(fb.removes, ["cid-9"])
        self.assertTrue(box.is_removed())
        self.assertEqual(report.as_dict(), {"image": "i", "built": False, "name": "n", "cpus": "1", "memory": "1g", "pids": 1})

    def test_within(self) -> None:
        self.assertTrue(container.within("/a/b", "/a/b"))
        self.assertTrue(container.within("/a/b", "/a/b/c"))
        self.assertFalse(container.within("/a/b", "/a/bc"))
        self.assertFalse(container.within("/a/b", "/a"))
        self.assertFalse(container.within("/a/b", "rel"))
        self.assertTrue(re.fullmatch(r"[0-9a-f]{8}", container.container_name("w", 1, "a")[-8:]))


@unittest.skipUnless(HAVE_GIT, "git is not on PATH")
class PreparedImageTest(unittest.TestCase):
    """start_box's prepared-image mode (the step's `image` parameter):
    the image a person prepared is resolved to its local ID and the
    container starts by that ID — never built, never pulled — so a repo
    without .polyforge/Dockerfile.verify runs in its container too, with
    the default mode's container contract unchanged: the clone and the
    cache volume alone mounted, the labels, the image's environment. The
    expected values are the ticket's rulings (polyforge#64), not the
    implementation's own answers: the digest is the resolved local ID, the
    cache volume's generation is its first 32 hex, and a missing image is
    its own error (ImageMissing, a BoxError), never an engine failure."""

    IMAGE_REF = "registry.example/team/verify:2026-11"
    IMAGE_ID = "sha256:" + "ab" * 32
    REPO_DIGESTS = ["registry.example/team/verify@sha256:" + "cd" * 32]

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.capsule = make_capsule(self.root, "app", {"a.txt": "a\n", container.DOCKERFILE_VERIFY: TEST_DOCKERFILE})
        self.clone = os.path.join(self.capsule, "repos", "app")
        self.log_dir = os.path.join(self.capsule, "runtime-state", "run", "2", "app")
        tmp_root = os.path.join(self.root, "box-tmp")
        os.mkdir(tmp_root)
        self.fb = FakeRuntime(tmp_root)
        self.fb.identities[self.IMAGE_REF] = (self.IMAGE_ID, list(self.REPO_DIGESTS))
        self.said: List[str] = []

    def tearDown(self) -> None:
        self._td.cleanup()

    def co(self) -> ContainerOptions:
        return ContainerOptions(runtime=self.fb, owner="/srv/pf-data", boot="boot-1", wi="wi_x",
                                 cpus=2.0, memory="1g", pids=33, say=self.said.append)

    def start(self, image: str = "", repo: str = "app", clone: str = "", log_dir: str = "") -> container.Box:
        return container.start_box(self.co(), repo, clone or self.clone, log_dir or self.log_dir,
                                   time.time() + 60.0, 2, lambda p: p, image=image)

    def test_starts_by_the_resolved_local_id_and_builds_nothing(self) -> None:
        box = self.start(image=self.IMAGE_REF)
        spec = self.fb.starts[0]
        self.assertEqual(spec.image, self.IMAGE_ID, "the container starts by the image's local ID")
        self.assertTrue(spec.pull_never, "a prepared image is never pulled")
        self.assertEqual(self.fb.builds, [], "a prepared image is never built")
        self.assertEqual(spec.volumes,
                         {"pf-verify-cache-app-" + self.IMAGE_ID[len("sha256:"):][:32] + "-" + path_id(self.clone):
                          container.CONTAINER_CACHE_DIR})
        self.assertEqual([(b.path, b.read_only) for b in spec.binds], [(self.clone, False)],
                         "the clone alone is mounted, as in the default mode")
        self.assertEqual(spec.labels, {
            container.LABEL_DAEMON: "/srv/pf-data", container.LABEL_BOOT: "boot-1", container.LABEL_WI: "wi_x",
            container.LABEL_RUN: "2", container.LABEL_REPO: "app",
        })
        self.assertEqual((spec.cpus, spec.memory, spec.pids), (2.0, "1g", 33))
        rep = box.report
        self.assertEqual((rep.image, rep.built, rep.build_log, rep.digest, rep.repo_digests),
                         (self.IMAGE_REF, False, "", self.IMAGE_ID, self.REPO_DIGESTS))
        self.assertEqual(rep.as_dict(), {"image": self.IMAGE_REF, "built": False, "digest": self.IMAGE_ID,
                                         "repo_digests": self.REPO_DIGESTS, "name": spec.name,
                                         "cpus": "2", "memory": "1g", "pids": 33})
        self.assertEqual(box.env, self.fb.image_environ, "the environment is the image's own")
        self.assertEqual(os.listdir(self.log_dir), [], "no build log is written")
        box.remove()
        self.assertEqual(self.fb.removes, [box.id])

    def test_a_repo_without_a_dockerfile_runs_in_its_container(self) -> None:
        capsule = make_capsule(self.root, "bare", {"a.txt": "a\n"})
        clone = os.path.join(capsule, "repos", "bare")
        log_dir = os.path.join(capsule, "runtime-state", "run", "2", "bare")
        try:
            box = self.start(image=self.IMAGE_REF, repo="bare", clone=clone, log_dir=log_dir)
        except container.BoxError as err:
            self.fail("a prepared image needs no Dockerfile: %s" % err)
        spec = self.fb.starts[0]
        self.assertEqual((self.fb.builds, spec.image, spec.pull_never), ([], self.IMAGE_ID, True))
        self.assertEqual(spec.volumes,
                         {"pf-verify-cache-bare-" + self.IMAGE_ID[len("sha256:"):][:32] + "-" + path_id(clone): "/cache"})
        box.remove()
        # the default mode still needs the Dockerfile: unchanged
        with self.assertRaises(container.BoxError) as got:
            self.start(repo="bare", clone=clone, log_dir=log_dir)
        self.assertIn("unreadable at HEAD", str(got.exception))

    def test_a_missing_image_is_its_own_error_never_a_start(self) -> None:
        with self.assertRaises(container.ImageMissing) as got:
            self.start(image="registry.example/nope:1")
        self.assertEqual(got.exception.ref, "registry.example/nope:1")
        self.assertIsInstance(got.exception, container.BoxError, "a caller catching BoxError still catches it")
        self.assertEqual((self.fb.builds, self.fb.starts), ([], []))

    def test_without_the_parameter_the_default_mode_is_unchanged(self) -> None:
        box = self.start()
        spec = self.fb.starts[0]
        tag = container.image_tag("app", TEST_DOCKERFILE.encode())
        self.assertFalse(spec.pull_never)
        self.assertEqual(spec.image, tag)
        self.assertEqual(self.fb.builds, [(tag, TEST_DOCKERFILE.encode(), {container.LABEL_REPO: "app"})])
        rep = box.report
        self.assertEqual((rep.digest, rep.repo_digests), ("", []))
        self.assertEqual(rep.build_log, os.path.join(self.log_dir, "image-build.log"))
        self.assertNotIn("digest", rep.as_dict())
        self.assertNotIn("repo_digests", rep.as_dict())
        box.remove()

    def test_start_args_pull_never_only_for_a_prepared_image(self) -> None:
        args = DockerCLI().start_args(ContainerSpec(name="n", image="sha256:" + "ab" * 32, pull_never=True))
        self.assertIn("--pull", args, "a prepared image is started with --pull never")
        self.assertEqual(args[args.index("--pull") + 1], "never")
        self.assertEqual(DockerCLI().start_args(ContainerSpec(name="n", image="i:t")).count("--pull"), 0)

    def test_a_reference_that_cannot_be_one_is_refused_before_the_engine(self) -> None:
        for bad in ("--privileged", "x y", "a\nb", " sha256:x"):
            with self.assertRaises(container.BoxError) as got:
                self.start(image=bad)
            self.assertNotIsInstance(got.exception, container.ImageMissing, bad)
            self.assertIn("not a docker image reference", str(got.exception), bad)
        self.assertEqual((self.fb.builds, self.fb.starts), ([], []))


if __name__ == "__main__":
    unittest.main()
