"""pfstep.container and pfstep.runner against a real docker engine (skipped
when none answers, the small public image cannot be had, or the engine
does not see this machine's directories at their own paths): a command
that runs and exits 0 or 1 in its repo's container, one the deadline kills,
and the container gone after each; a timed-out command leaves no process
in the container while the keep-alive survives; a bounded container ends
by itself; the expiry sweep removes only its own label's expired
containers. Every container is labelled with a fresh owner and removed —
with the image and volume a test built — when the test ends."""

import os
import secrets
import shutil
import tempfile
import time
import unittest
from typing import Dict, List, Optional

from pfstep import container, proc, runner
from pfstep.container import ContainerOptions, ContainerSpec, DockerCLI
from pfstep.runner import CommandOptions, ExecSpec, Runner

from .capsuleutil import HAVE_GIT, make_capsule

PROBE_IMAGE = "busybox:1.37"
# busybox has no bash: the image gives it one that is sh
REAL_DOCKERFILE = "FROM " + PROBE_IMAGE + "\nRUN printf '#!/bin/sh\\nexec /bin/sh \"$@\"\\n' > /bin/bash && chmod 0755 /bin/bash\n"


def _engine() -> Optional[DockerCLI]:
    d = DockerCLI.for_env()
    try:
        d.available()
    except container.ContainerError:
        return None
    try:
        if not d.image_exists(PROBE_IMAGE):
            c = proc.run([d.bin, "pull", "-q", PROBE_IMAGE], timeout=180)
            if c.exit_code != 0:
                return None
    except container.ContainerError:
        return None
    return d


def _sees(d: DockerCLI, directory: str) -> bool:
    """Whether the engine mounts directory of this machine at its own path."""
    sentinel = os.path.join(directory, ".pfstep-docker-sentinel")
    with open(sentinel, "w", encoding="utf-8") as f:
        f.write("seen\n")
    try:
        c = proc.run([d.bin, "run", "--rm", "--label", "pf.pfstep.probe=1", "--mount",
                      "type=bind,source=%s,target=%s,readonly" % (directory, directory),
                      PROBE_IMAGE, "cat", sentinel], timeout=120)
        return c.exit_code == 0 and c.stdout.strip() == b"seen"
    finally:
        os.remove(sentinel)


ENGINE = _engine()


@unittest.skipUnless(ENGINE is not None, "no docker engine (or %s cannot be had)" % PROBE_IMAGE)
class RealDockerTest(unittest.TestCase):
    def setUp(self) -> None:
        assert ENGINE is not None
        self.d = ENGINE
        self.owner = "pfstep-real-" + secrets.token_hex(6)
        self.tags: List[str] = []
        self.volumes: List[str] = []
        self.dirs: List[str] = []

    def tearDown(self) -> None:
        for owner in (self.owner, self.owner + "-other"):
            try:
                for cid in self.d.list_labelled({container.LABEL_DAEMON: owner}, container.LABEL_BOOT):
                    try:
                        self.d.remove(cid)
                    except container.ContainerError:
                        pass
            except container.ContainerError:
                pass
        for tag in self.tags:
            proc.run([self.d.bin, "rmi", "--force", tag], timeout=60)
        for v in self.volumes:
            proc.run([self.d.bin, "volume", "rm", "--force", v], timeout=60)
        for p in self.dirs:
            shutil.rmtree(p, ignore_errors=True)

    def visible_dir(self) -> str:
        for base in (tempfile.gettempdir(), os.getcwd()):
            try:
                p = os.path.realpath(tempfile.mkdtemp(prefix=".pfstep-docker-", dir=base))
            except OSError:
                continue
            self.dirs.append(p)
            if _sees(self.d, p):
                return p
        self.skipTest("the docker engine does not see this machine's directories at their own paths")

    def left(self, owner: str) -> List[str]:
        return sorted(self.d.list_labelled({container.LABEL_DAEMON: owner}, container.LABEL_BOOT))

    def start(self, name: str, labels: Dict[str, str], lifetime: float = 0.0) -> str:
        spec = ContainerSpec(name=name, image=PROBE_IMAGE, labels=labels, cpus=1, memory="256m", pids=64, lifetime=lifetime)
        return self.d.start(spec)

    @unittest.skipUnless(HAVE_GIT, "git is not on PATH")
    def test_run_command_in_a_real_container(self) -> None:
        root = self.visible_dir()
        repo = "pfstep-real-" + secrets.token_hex(4)
        capsule = make_capsule(root, repo, {"a.txt": "a\n", container.DOCKERFILE_VERIFY: REAL_DOCKERFILE})
        self.tags.append(container.image_tag(repo, REAL_DOCKERFILE.encode()))
        self.volumes.append(container.cache_volume(repo, REAL_DOCKERFILE.encode()))
        results = {}
        for i, (cmd, seconds) in enumerate((("test -f .polyforge/Dockerfile.verify", 240.0), ("false", 240.0),
                                            ("sleep 300 & sleep 301", 8.0)), start=1):
            res = runner.run_command(CommandOptions(
                capsule_dir=capsule, repo=repo, command=cmd, deadline=time.time() + seconds, timeout=seconds,
                log_dir=os.path.join(capsule, "runtime-state", "run", str(i), repo), run=i,
                container=ContainerOptions(owner=self.owner, boot="boot-1", wi="wi_real", cpus=1, memory="256m", pids=128),
            ))
            results[cmd] = res
            self.assertEqual(self.left(self.owner), [], "the run's container is gone once the command ends")
            self.assertEqual((res.image, res.host), (container.image_tag(repo, REAL_DOCKERFILE.encode()), False))
        self.assertEqual(results["test -f .polyforge/Dockerfile.verify"].exit, 0, "the command runs from the clone's root")
        self.assertEqual((results["false"].exit, results["false"].timed_out), (1, False))
        cut = results["sleep 300 & sleep 301"]
        self.assertEqual((cut.exit, cut.timed_out, cut.not_started), (runner.TIMEOUT_EXIT, True, False))

    def test_a_timeout_leaves_no_process_but_the_keep_alive(self) -> None:
        cid = self.start(self.owner, {container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "boot-old"})
        report = container.ContainerReport(image=PROBE_IMAGE, built=False, build_log="", name=self.owner, cpus="1", memory="256m", pids=64)
        box = container.Box(self.d, cid, [], report)
        logs = tempfile.mkdtemp()
        self.dirs.append(logs)
        r = Runner(logs, time.time() + 120, env={"PATH": os.environ.get("PATH", "")})
        start = time.monotonic()
        er = r.exec_to(ExecSpec(argv=["sh", "-c", "sleep 300 & sleep 301; echo never"], dir="/", cwd="/", timeout=2.0, private_tmp=True),
                       os.path.join(logs, "1.log"), box)
        self.assertEqual((er.exit, er.timed_out), (runner.TIMEOUT_EXIT, True), er.error)
        self.assertLess(time.monotonic() - start, 60)
        self.assertEqual(box.lost_reason(), "")
        ps = proc.run([self.d.bin, "exec", cid, "ps", "-o", "pid,args"], timeout=60).stdout.decode()
        self.assertNotIn("sleep 30", ps, "the timed-out command left processes in the container")
        self.assertIn("sleep 3600", ps, "the keep-alive did not survive the reap")
        self.assertNotEqual(proc.run([self.d.bin, "exec", cid, "test", "-e", er.tmp_dir], timeout=60).exit_code, 0,
                            "the private TMPDIR survived its command")
        nxt = r.exec_to(ExecSpec(argv=["sh", "-c", 'test -d "$TMPDIR" && echo ok'], dir="/", cwd="/", timeout=30.0, private_tmp=True),
                        os.path.join(logs, "2.log"), box)
        self.assertEqual(nxt.exit, 0, "the container still runs the next command")
        self.assertTrue(self.d.has_command(cid, "sh"))
        self.assertFalse(self.d.has_command(cid, "pf-no-such-command"))
        self.d.remove(cid)
        self.assertEqual(self.left(self.owner), [])
        with self.assertRaises(container.ContainerError):
            self.d.has_command(cid, "sh")

    def test_a_bounded_container_ends_by_itself(self) -> None:
        cid = self.start(self.owner, {container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "b"}, lifetime=4.0)
        proc.run([self.d.bin, "exec", "--detach", cid, "sleep", "600"], timeout=60)
        self.d.reap(cid, [])
        proc.run([self.d.bin, "exec", "--detach", cid, "sleep", "600"], timeout=60)
        end = time.monotonic() + 60
        while self.left(self.owner):
            if time.monotonic() > end:
                self.fail("the container with a 4s lifetime still exists after 60s")
            time.sleep(1)

    def test_the_expiry_sweep_removes_only_its_own_expired(self) -> None:
        now = int(time.time())
        other = self.owner + "-other"
        old = self.start(self.owner + "-old", {container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "b",
                                               container.LABEL_EXPIRES: str(now - 60)})
        live = self.start(self.owner + "-live", {container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "b",
                                                 container.LABEL_EXPIRES: str(now + 3600)})
        theirs = self.start(other + "-old", {container.LABEL_DAEMON: other, container.LABEL_BOOT: "b",
                                             container.LABEL_EXPIRES: str(now - 60)})
        said: List[str] = []
        self.assertEqual(container.sweep_expired(self.d, self.owner, time.time(), False, said.append), [old])
        self.assertEqual(self.left(self.owner), [live])
        self.assertEqual(self.left(other), [theirs])


if __name__ == "__main__":
    unittest.main()
