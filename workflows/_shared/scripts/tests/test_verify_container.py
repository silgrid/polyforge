"""verify.py: a repo whose HEAD carries .polyforge/Dockerfile.verify runs
its engine and bar in a per-run container; any other repo runs on the host.
The container engine is tests.fakebox.FakeRuntime, which runs every
"container" command on the host (same cwd, ``env -i`` with the image
environment plus the added one) and records every call. The Go side's twin
is verifyrun/w393_container_test.go (its Run-level tests; the docker CLI's
argv, the tag, the start arguments and the sweeps are tests/test_container*.py's)."""

import hashlib
import io
import json
import os
import re
import signal
import tempfile
import threading
import time
import unittest
from contextlib import redirect_stderr, redirect_stdout
from typing import Any, Dict, List, Mapping, Optional, Tuple

import verify
from pfstep import container, runner

from .capsuleutil import TEST_DOCKERFILE
from .fakebox import FakeRuntime
from .verifyfix import HAVE_BASH, HAVE_GIT, Fixture, Run, cmd_of, fake_engine, report, step_env

DOCKERFILE = container.DOCKERFILE_VERIFY


class ProbeFailingRuntime(FakeRuntime):
    """A FakeRuntime whose command probe fails like a docker engine that
    lost the container, counting its calls."""

    def __init__(self, tmp_root: str, err: str) -> None:
        super().__init__(tmp_root)
        self.has_cmd_err = err
        self.has_cmd_calls = 0

    def has_command(self, cid: str, name: str) -> bool:
        self.has_cmd_calls += 1
        raise container.ContainerError(self.has_cmd_err)


def box_run(capsule: str, engine: str, rt: FakeRuntime, env: Optional[Mapping[str, str]] = None) -> Run:
    """verify.py --tier light over capsule on rt, in-process, its logs in
    the capsule's next runtime-state/verify/<n> (no --out, no --log-dir:
    the run numbering the containers carry); the result file read from
    --json's stdout."""
    argv = ["--tier", "light", "--capsule", capsule, "--engine", engine, "--json"]
    so, se = io.StringIO(), io.StringIO()
    with redirect_stdout(so), redirect_stderr(se):
        code = verify.main(argv, step_env(env), runtime=rt)
    lines = [ln for ln in so.getvalue().split("\n") if ln.strip()]
    if not lines:
        raise AssertionError("verify.py printed no result (exit %d): %s" % (code, se.getvalue()))
    res = json.loads(lines[-1])
    doc: Optional[Dict[str, Any]] = None
    rep = ""
    d = res.get("details") or {}
    if d.get("result"):
        p = os.path.join(capsule, d["result"])
        with open(p, encoding="utf-8") as f:
            doc = json.load(f)
        with open(os.path.join(os.path.dirname(p), "report-mechanical.md"), encoding="utf-8") as f:
            rep = f.read()
    return Run(exit=code, stdout=so.getvalue(), stderr=se.getvalue(), result=res, doc=doc, report=rep)


def doc_of(r: Run) -> Dict[str, Any]:
    if r.doc is None:
        raise AssertionError("no result.json (exit %d): %s %s" % (r.exit, r.result, r.stderr))
    return r.doc


def repo0(r: Run) -> Dict[str, Any]:
    reps = doc_of(r)["repos"]
    if not reps:
        raise AssertionError("no repo record: %r" % doc_of(r))
    out: Dict[str, Any] = reps[0]
    return out


def read(path: str) -> str:
    try:
        with open(path, encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return ""


@unittest.skipUnless(HAVE_GIT and HAVE_BASH, "git and bash are needed")
class ContainerRunTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def box_fixture(self, with_dockerfile: bool, *cmds: Dict[str, Any], sub: str = "fx") -> Tuple[Fixture, str]:
        """A capsule whose repo app carries the Dockerfile on its task branch
        (a touched repo) and a full report of the given commands."""
        root = os.path.join(self.root, sub)
        os.makedirs(root)
        f = Fixture(root, {"app": {"a.txt": "a\n"}})
        files = {"a.txt": "a2\n"}
        if with_dockerfile:
            files[DOCKERFILE] = TEST_DOCKERFILE
        f.commit_in_repo("app", files, "change")
        return f, fake_engine(root, {"app": report("full", *cmds)})

    def fake_box(self, sub: str = "box") -> FakeRuntime:
        d = os.path.join(self.root, sub)
        os.makedirs(d, exist_ok=True)
        return FakeRuntime(d)

    def test_boxed_run_runs_engine_and_bar_in_its_container(self) -> None:
        """TestW393BoxedRunRunsEngineAndBarInItsContainer."""
        f, eng = self.box_fixture(
            True,
            cmd_of("unit", "fast", 'test -n "$TMPDIR" && test -d "$TMPDIR" && echo "image=$IMAGE_ONLY lang=$LANG'
                   ' secret=${PF_SECRET:-none} tmux=${TMUX:-none}"'),
            cmd_of("e2e", "full", "true"),
        )
        fb = self.fake_box()
        env = {"LANG": "C.UTF-8", "PF_SECRET": "s3", "TMUX": "/tmp/tmux-0/default,1,0", "SSH_AUTH_SOCK": "/x",
               "PF_WI_ID": "wi_x"}
        r = box_run(f.capsule, eng, fb, env)
        doc = doc_of(r)
        self.assertEqual((doc.get("incomplete", ""), doc["mechanical_verdict"]), ("", "pass"), doc.get("fail_reason", r.stderr))
        # the image: the Dockerfile at HEAD, tagged by its hash
        tag = container.image_tag("app", TEST_DOCKERFILE.encode())
        self.assertEqual([(b[0], b[1]) for b in fb.builds], [(tag, TEST_DOCKERFILE.encode())],
                         "want one build of %s from the committed Dockerfile" % tag)
        self.assertTrue(tag.startswith("pf-verify-app:") and len(tag[len("pf-verify-app:"):]) == 32, tag)
        build_log = os.path.join(f.capsule, "runtime-state/verify/1/app/image-build.log")
        self.assertIn("Step 1/1 : FROM scratch-test-image", read(build_log))
        # the container: labels, mounts at their own paths, cache volume, limits
        self.assertEqual(len(fb.starts), 1, fb.starts)
        s = fb.starts[0]
        labels = dict(s.labels)
        # its deadline (pf.verify.expires, unix seconds: what a later run's
        # sweep reads) and a fresh boot nonce beside the fixed labels
        expires = labels.pop(container.LABEL_EXPIRES, "")
        self.assertRegex(expires, re.compile(r"\A[0-9]+\Z", re.ASCII))
        self.assertRegex(labels.pop(container.LABEL_BOOT, ""), re.compile(r"\A[0-9a-f]{16}\Z", re.ASCII))
        self.assertEqual(labels, {container.LABEL_DAEMON: container.STEP_OWNER,
                                  container.LABEL_WI: "wi_x", container.LABEL_RUN: "1", container.LABEL_REPO: "app"})
        resolved = os.path.realpath(eng)
        self.assertEqual(
            [(b.path, b.read_only) for b in s.binds],
            [(f.repo("app"), False), (os.path.join(f.capsule, "runtime-state/verify/1/base/app"), False),
             (os.path.dirname(resolved), True)],
        )
        self.assertEqual(s.volumes, {"pf-verify-cache-app-" + hashlib.sha256(TEST_DOCKERFILE.encode()).hexdigest()[:32]: "/cache"})
        self.assertEqual((s.image, s.cpus, s.memory, s.pids),
                         (tag, container.default_cpus(), container.DEFAULT_CONTAINER_MEMORY, container.DEFAULT_CONTAINER_PIDS))
        # every engine and bar command ran in it, in the repo's own path; git did not
        saw_engine = saw_unit = False
        for cid, workdir, xenv, argv in fb.execs:
            self.assertEqual((cid, workdir), ("cid-1", f.repo("app")))
            self.assertNotIn("git", argv, "git ran in the container")
            if argv and argv[0] == resolved:
                saw_engine = True
            if "bash" in argv and "image=" in " ".join(argv[argv.index("bash"):]):
                saw_unit = True
                for kv in xenv:
                    name = kv.partition("=")[0]
                    self.assertTrue(name in container.CONTAINER_ENV_ALLOW or name == "TMPDIR",
                                    "a container command was given %s (only the allowlist and the knobs pass)" % kv)
        self.assertTrue(saw_engine and saw_unit, "engine in box %s, unit in box %s: %r" % (saw_engine, saw_unit, fb.execs))
        # what the command saw: the image's env, the allowlisted LANG, no secret, no TMUX
        unit_log = ""
        for c in repo0(r)["commands"]:
            if "image=" in c["command"]:
                self.assertEqual(c["exit_code"], 0, c)
                unit_log = os.path.join(f.capsule, c["log_path"])
        self.assertIn("image=from-image lang=C.UTF-8 secret=none tmux=none", read(unit_log))
        # after every container command: a reap; at the end: removed once
        self.assertEqual(len(fb.reaps), len(fb.execs))
        self.assertEqual(fb.removes, ["cid-1"])
        # the record: the container
        rec = repo0(r).get("container")
        self.assertIsNotNone(rec)
        assert rec is not None
        self.assertEqual((rec["image"], rec["built"], rec.get("build_log"), rec["memory"]),
                         (tag, True, "runtime-state/verify/1/app/image-build.log", "16g"))
        self.assertIn("ran in container `pf-verify-wi_x-1-app-", r.report)

        # a second run finds the image present: no build
        r2 = box_run(f.capsule, eng, fb, dict(env))
        rec2 = repo0(r2).get("container")
        self.assertEqual(len(fb.builds), 1, "the second run rebuilt")
        self.assertIsNotNone(rec2)
        assert rec2 is not None
        self.assertFalse(rec2["built"])
        self.assertEqual(len(fb.starts), 2)
        # (Go's second run also sets CPUs 2.5 / memory 3g / pids 100 through
        # Options.Container; verify.py has no limit knob, so only the run
        # numbering is ported)
        self.assertEqual(fb.starts[1].labels[container.LABEL_RUN], "2")

    def test_repo_without_dockerfile_runs_on_the_host(self) -> None:
        """TestW393RepoWithoutDockerfileRunsOnTheHost: a container engine is
        at hand, but the repo carries no Dockerfile.verify — no image, no
        container, no exec; the bar runs on the host and passes. (Go's
        TestW393HostRunIgnoresTheDockerfile has no twin: verify.py has no
        option that keeps a Dockerfile-carrying repo on the host.)"""
        f, eng = self.box_fixture(False, cmd_of("unit", "fast", "echo on-the-host"))
        fb = self.fake_box()
        r = box_run(f.capsule, eng, fb)
        doc = doc_of(r)
        self.assertEqual((doc.get("incomplete", ""), doc["mechanical_verdict"]), ("", "pass"), r.stderr)
        self.assertEqual(r.exit, 0)
        self.assertEqual(len(fb.starts) + len(fb.execs) + len(fb.builds), 0,
                         "a repo without the Dockerfile touched the container engine")
        self.assertNotIn("container", repo0(r))
        self.assertEqual(r.check("app:unit")["status"], "pass")
        cmd = [c for c in repo0(r)["commands"] if c["check"] == "app:unit"][0]
        self.assertIn("on-the-host", read(os.path.join(f.capsule, cmd["log_path"])))

    def test_container_trouble_is_incomplete_never_red(self) -> None:
        """TestW393ContainerTroubleIsIncompleteNeverRed."""
        cases = [
            ("docker unavailable", "unavailable", "cannot connect to the docker daemon", "container engine is unavailable: cannot connect"),
            ("build fails", "build_err", "docker build: exit status 1", "image-build.log"),
            ("start refused", "start_err", "no space left", "no space left"),
            ("lost while the bar runs", "reap_err", "container is not running", "became unusable while the bar ran"),
        ]
        for i, (name, attr, err, want) in enumerate(cases):
            with self.subTest(name):
                f, eng = self.box_fixture(True, cmd_of("unit", "fast", "exit 3"), sub="fx%d" % i)
                fb = self.fake_box("box%d" % i)
                setattr(fb, attr, err)
                r = box_run(f.capsule, eng, fb)
                doc = doc_of(r)
                self.assertIn(want, doc.get("incomplete", ""))
                self.assertNotEqual(doc["mechanical_verdict"], "pass", "an unusable container passed the bar")
                self.assertEqual(r.exit, 2)
                if name == "build fails":
                    raw = read(os.path.join(f.capsule, "runtime-state/verify/1/app/image-build.log"))
                    self.assertIn("E: the build broke", raw)
                    self.assertIn("the build failed", raw)
                if fb.starts:
                    self.assertTrue(fb.removes, "a started container was never removed")

    def test_requirements_are_judged_in_the_container(self) -> None:
        """TestW393RequirementsAreJudgedInTheContainer."""

        def need(name: str, env: List[str], cmds: List[str]) -> Dict[str, Any]:
            return cmd_of(name, "fast", "true", requires_env=env, requires_cmd=cmds)

        f, eng = self.box_fixture(
            True,
            need("image-env", ["IMAGE_ONLY"], []),
            need("host-env", ["HOST_ONLY"], []),
            need("box-cmd", [], ["only-in-box"]),
            need("host-cmd", [], ["git"]),
        )
        fb = self.fake_box()
        fb.commands["only-in-box"] = True
        r = box_run(f.capsule, eng, fb, {"HOST_ONLY": "1"})
        skipped = {c["name"]: c.get("skipped", "") for c in repo0(r)["commands"]}
        self.assertEqual((skipped["image-env"], skipped["box-cmd"]), ("", ""), "met in the container but skipped: %r" % skipped)
        self.assertIn("env HOST_ONLY", skipped["host-env"], "met only on the host but run: %r" % skipped)
        self.assertIn("command git", skipped["host-cmd"], "met only on the host but run: %r" % skipped)

    def test_a_timed_out_command_is_reaped(self) -> None:
        """TestW393TimeoutAndCancelEndTheContainersProcesses, "a timed-out
        command is reaped"."""
        f, eng = self.box_fixture(True, cmd_of("slow", "fast", "sleep 30", timeout="1s"))
        fb = self.fake_box()
        start = time.monotonic()
        r = box_run(f.capsule, eng, fb)
        self.assertLess(time.monotonic() - start, 20, "the timeout did not cut the command")
        c = repo0(r)["commands"][0]
        self.assertEqual((c["exit_code"], c.get("timed_out", False)), (runner.TIMEOUT_EXIT, True), c)
        self.assertEqual(len(fb.reaps), len(fb.execs))

    def test_a_cancelled_run_removes_its_container_at_once(self) -> None:
        """TestW393TimeoutAndCancelEndTheContainersProcesses, "a cancelled run
        removes its container at once": SIGINT (KeyboardInterrupt, Go's
        context cancel) arrives while the slow command runs."""
        f, eng = self.box_fixture(True, cmd_of("slow", "fast", "sleep 30"))
        fb = self.fake_box()
        main = threading.get_ident()
        stop = threading.Event()

        def cancel() -> None:
            while not stop.is_set():
                if len(fb.execs) >= 2:  # the engine, then the slow command
                    time.sleep(0.2)
                    signal.pthread_kill(main, signal.SIGINT)
                    return
                time.sleep(0.02)

        old = signal.signal(signal.SIGINT, signal.default_int_handler)
        t = threading.Thread(target=cancel, daemon=True)
        start = time.monotonic()
        try:
            t.start()
            try:
                r = box_run(f.capsule, eng, fb)
            except KeyboardInterrupt:
                self.fail("the interrupt escaped verify.py")
        finally:
            stop.set()
            t.join(5)
            signal.signal(signal.SIGINT, old)
        self.assertLess(time.monotonic() - start, 20, "the interrupt did not end the slow command")
        self.assertIn("interrupted", doc_of(r).get("incomplete", ""))
        self.assertEqual(r.exit, 2)
        self.assertTrue(fb.removes, "the cancelled run's container was not removed")

    def test_container_left_behind_is_recorded_and_logged_once(self) -> None:
        """TestW393ContainerLeftBehindIsRecordedAndLoggedOnce: Go's
        ContainerOptions.Log is verify.py's stderr."""
        f, eng = self.box_fixture(True, cmd_of("unit", "fast", "true"))
        fb = self.fake_box()
        fb.remove_err = "docker rm: cut at its deadline"
        r = box_run(f.capsule, eng, fb, {"PF_WI_ID": "wi_x"})
        doc = doc_of(r)
        self.assertEqual((doc.get("incomplete", ""), doc["mechanical_verdict"]), ("", "pass"),
                         "a removal failure changed the verdict")
        c = repo0(r).get("container")
        self.assertIsNotNone(c)
        assert c is not None
        left = c.get("left", "")
        self.assertIn("cut at its deadline", left)
        lines = r.stderr.splitlines()
        self.assertEqual(len(lines), 1, "want one line naming the container: %r" % r.stderr)
        self.assertIn("cid-1", lines[0])
        self.assertIn(c["name"], lines[0])
        self.assertEqual(len(fb.removes), 1, "the removal must be tried once (the owner's next sweep retries)")
        self.assertIn("left behind", r.report)
        # the record names who removes it: a script's container is the next
        # run's sweep's (pfstep.container), where Go's was the daemon's next
        # start
        self.assertIn("the owner's next sweep removes it", left)

    def test_command_probe_failure_is_incomplete_never_skipped(self) -> None:
        """TestW393CommandProbeFailureIsIncompleteNeverSkipped."""
        f, eng = self.box_fixture(
            True,
            cmd_of("needs-go", "fast", "exit 0", requires_cmd=["go"]),
            cmd_of("needs-node", "fast", "exit 0", requires_cmd=["node"]),
        )
        d = os.path.join(self.root, "box")
        os.makedirs(d)
        fb = ProbeFailingRuntime(d, "docker exec: exit status 1: Error response from daemon: No such container: cid-1")
        r = box_run(f.capsule, eng, fb, {"PF_WI_ID": "wi_x"})
        doc = doc_of(r)
        inc = doc.get("incomplete", "")
        self.assertIn("probing its PATH for requires_cmd go failed", inc)
        self.assertIn("No such container", inc)
        self.assertNotEqual(doc["mechanical_verdict"], "pass", "a probe the docker engine failed passed the bar")
        for e in fb.execs:
            self.assertNotIn("bash", e[3], "the command ran in a container whose probe failed")
        # a container already lost is not probed again (each probe of a hung
        # engine would wait out its bound)
        self.assertEqual(fb.has_cmd_calls, 1, "the second command's probe must end at the lost container")

    def test_container_gets_the_machines_network_settings_and_nothing_else(self) -> None:
        """TestW393ContainerGetsTheMachinesNetworkSettingsAndNothingElse: Go
        reads runner.boxEnv; here the environment the run hands a container
        command (the engine's, and the bar command's beside its TMPDIR)."""
        passed = {
            # the locale and the machine's network settings
            "LANG": "C.UTF-8", "HTTP_PROXY": "http://proxy.example.invalid:3128",
            "https_proxy": "http://proxy.example.invalid:3129", "NO_PROXY": "localhost,.example.invalid",
            "no_proxy": "127.0.0.1", "SSL_CERT_FILE": "/etc/ssl/site.pem", "SSL_CERT_DIR": "/etc/ssl/site",
            "GOPROXY": "https://goproxy.example.invalid", "GOPRIVATE": "git.example.invalid/*",
            "GONOSUMDB": "git.example.invalid/*", "GOFLAGS": "-mod=mod", "npm_config_registry": "https://npm.example.invalid/",
        }
        kept_out = {
            # the host's paths, caches, credentials, pf and tmux state
            "HOME": "/home/op", "GOPATH": "/home/op/go", "GOCACHE": "/home/op/.cache/go-build",
            "npm_config_cache": "/home/op/.npm", "ALL_PROXY": "socks5://x", "GITHUB_TOKEN": "t0k", "NPM_TOKEN": "t1k",
            "PF_DAEMON_SOCKET": "/s", "POLYFORGE_DATA_DIR": "/d", "TMUX": "/tmp/tmux-0/default,1,0", "SSH_AUTH_SOCK": "/a",
            "DOCKER_HOST": "unix:///var/run/docker.sock", "NODE_OPTIONS": "--inspect", "PATH": "/host/bin",
        }
        f, eng = self.box_fixture(True, cmd_of("unit", "fast", "true"))
        fb = self.fake_box()
        env: Dict[str, str] = {}
        env.update(passed)
        env.update(kept_out)
        r = box_run(f.capsule, eng, fb, env)
        self.assertEqual(doc_of(r)["mechanical_verdict"], "pass", r.stderr)
        resolved = os.path.realpath(eng)
        engine_envs = [e[2] for e in fb.execs if e[3] and e[3][0] == resolved]
        bar_envs = [e[2] for e in fb.execs if "bash" in e[3]]
        self.assertEqual(len(engine_envs), 1, fb.execs)
        self.assertEqual(len(bar_envs), 1, fb.execs)

        def as_map(kvs: List[str]) -> Dict[str, str]:
            out: Dict[str, str] = {}
            for kv in kvs:
                k, _, v = kv.partition("=")
                out[k] = v
            return out

        self.assertEqual(as_map(engine_envs[0]), passed)
        bar = as_map(bar_envs[0])
        self.assertTrue(bar.pop("TMPDIR", "").startswith("/tmp/"), bar_envs[0])
        self.assertEqual(bar, passed)
        # unset names are not passed empty
        rn = runner.Runner(f.capsule, time.time() + 60, env={})
        self.assertEqual(rn.box_env(), [], "an empty run environment gave the container something")


if __name__ == "__main__":
    unittest.main()
