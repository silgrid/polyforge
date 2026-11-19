# carries: workflows/_shared/scripts/pfstep/container.py, workflows/_shared/scripts/pfstep/runner.py
"""The cache volume is the clone path's too (polyforge#153): a tool whose
/cache results embed absolute paths — golangci-lint's result cache, the
reported defect — must never read, from a second clone at another path,
what a first clone wrote; the two clones mount two different volumes,
while the same path, run again, reads its own (a HIT, not a cold start).
The stand-in for such a tool is deterministic: its first run in a volume
writes the absolute working directory into /cache/result and answers
"MISS <path>", every later one answers "HIT <the saved path>" without
writing again. It runs inside real containers over both image modes (the
Dockerfile's own tag, and a prepared image started by its local ID),
each container removed at its run's end and the volumes left behind — the
runs' answers are the evidence, never a volume's name or a run's
duration. Skipped without a docker engine, the small public image, or an
engine that does not see this machine's directories at their own paths;
a skip proves no acceptance (AC1 and AC2 are the real runs)."""

import os
import secrets
import shutil
import tempfile
import time
import unittest
from typing import List, Tuple

from pfstep import container, proc, runner
from pfstep.container import ContainerOptions
from pfstep.runner import CommandOptions, ExecSpec, Runner

from .capsuleutil import HAVE_GIT, make_capsule
from .test_container_docker import ENGINE, PROBE_IMAGE, REAL_DOCKERFILE, _sees

# The stand-in for a path-sensitive cache tool: MISS writes its working
# directory (the clone's path as the container sees it) into /cache/result
# and answers "MISS <path>"; a later run in the same volume answers
# "HIT <the saved path>" and writes nothing.
STAND_IN = (
    'r=/cache/result; '
    'if [ -f "$r" ]; then echo "HIT $(cat "$r")"; '
    'else p=$(pwd); printf \'%s\\n\' "$p" > "$r"; echo "MISS $p"; fi'
)


@unittest.skipUnless(ENGINE is not None, "no docker engine (or %s cannot be had)" % PROBE_IMAGE)
class RealDockerCachePathTest(unittest.TestCase):
    def setUp(self) -> None:
        assert ENGINE is not None
        self.d = ENGINE
        self.owner = "pfstep-path-" + secrets.token_hex(6)
        self.repo = ""
        self.tags: List[str] = []
        self.volumes: List[str] = []
        self.dirs: List[str] = []

    def tearDown(self) -> None:
        try:
            for cid in self.d.list_labelled({container.LABEL_DAEMON: self.owner}, container.LABEL_BOOT):
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
                p = os.path.realpath(tempfile.mkdtemp(prefix=".pfstep-path-", dir=base))
            except OSError:
                continue
            self.dirs.append(p)
            if _sees(self.d, p):
                return p
        self.skipTest("the docker engine does not see this machine's directories at their own paths")

    def two_clones(self, dockerfile: bool) -> Tuple[str, str]:
        """Two clones of one repo at two different paths, both under a
        directory the engine sees; answers their paths. Registers the
        cleanups: the built image's tag and both clones' volumes."""
        self.repo = "pfstep-path-" + secrets.token_hex(4)
        base = self.visible_dir()
        files = {"a.txt": "a\n"}
        if dockerfile:
            files[container.DOCKERFILE_VERIFY] = REAL_DOCKERFILE
            self.tags.append(container.image_tag(self.repo, REAL_DOCKERFILE.encode()))
        second = os.path.join(base, "second")
        os.mkdir(second)
        clones = []
        for root in (base, second):
            capsule = make_capsule(root, self.repo, files)
            clone = os.path.join(capsule, "repos", self.repo)
            clones.append(clone)
            if dockerfile:
                self.volumes.append(container.cache_volume(self.repo, REAL_DOCKERFILE.encode(), clone))
            else:
                self.volumes.append(container.image_cache_volume(self.repo, self.image_id(), clone))
        return clones[0], clones[1]

    def image_id(self) -> str:
        """The prepared image's local ID, read independently of the module."""
        c = proc.run([self.d.bin, "image", "inspect", PROBE_IMAGE, "--format", "{{.Id}}"], timeout=60)
        self.assertEqual(c.exit_code, 0, "busybox:1.37 must be inspectable")
        return c.stdout.decode("utf-8", "replace").strip()

    def run_in(self, clone: str, i: int) -> str:
        """The stand-in run in the repo's container of the clone at clone
        (the Dockerfile's own image), over runner.run_command; answers its
        combined output. The container is removed when the run ends."""
        capsule = os.path.dirname(os.path.dirname(clone))
        res = runner.run_command(CommandOptions(
            capsule_dir=capsule, repo=self.repo, command=STAND_IN, deadline=time.time() + 240.0, timeout=240.0,
            log_dir=os.path.join(capsule, "runtime-state", "run", str(i), self.repo), run=i,
            container=ContainerOptions(runtime=self.d, owner=self.owner, boot="boot-1", wi="wi_path",
                                       cpus=1, memory="256m", pids=128, say=lambda _: None),
        ))
        self.assertEqual(res.exit, 0, "the stand-in ran in the container")
        with open(os.path.join(capsule, res.log), encoding="utf-8") as f:
            return f.read()

    def exec_in(self, clone: str, i: int) -> str:
        """The stand-in run in a container of the prepared image
        (started by its local ID, never built), over start_box and one
        exec; answers the exec's captured stdout. The box is removed at
        the run's end, its volume left behind."""
        log_root = os.path.join(self.visible_dir(), "exec-%d" % i)
        co = ContainerOptions(runtime=self.d, owner=self.owner, boot="boot-1", wi="wi_path",
                              cpus=1, memory="256m", pids=128, say=lambda _: None)
        box = container.start_box(co, self.repo, clone, os.path.join(log_root, "box"),
                                  time.time() + 240.0, i, lambda p: p, image=PROBE_IMAGE)
        try:
            r = Runner(log_root, time.time() + 240.0, env={"PATH": os.environ.get("PATH", "")})
            er = r.exec_to(
                ExecSpec(argv=["sh", "-c", STAND_IN], dir=clone, cwd=".", timeout=120.0,
                         private_tmp=True, capture=True),
                os.path.join(log_root, "%d.log" % i), box,
            )
            self.assertEqual((er.exit, er.error), (0, ""), "the stand-in ran in the container")
            return er.stdout.decode("utf-8", "replace")
        finally:
            box.remove()

    @unittest.skipUnless(HAVE_GIT, "git is not on PATH")
    def test_two_clone_paths_are_isolated(self) -> None:
        clone_a, clone_b = self.two_clones(dockerfile=True)
        first = self.run_in(clone_a, 1)
        self.assertIn("MISS " + clone_a, first, first)
        second = self.run_in(clone_b, 2)
        self.assertIn("MISS " + clone_b, second,
                      "the second clone at another path must not read the first one's cache: " + second)
        self.assertNotIn(clone_a, second, "no result may point at the other clone's path: " + second)

    @unittest.skipUnless(HAVE_GIT, "git is not on PATH")
    def test_two_clone_paths_are_isolated_prepared_image(self) -> None:
        clone_a, clone_b = self.two_clones(dockerfile=False)
        first = self.exec_in(clone_a, 1)
        self.assertIn("MISS " + clone_a, first, first)
        second = self.exec_in(clone_b, 2)
        self.assertIn("MISS " + clone_b, second,
                      "the second clone at another path must not read the first one's cache: " + second)
        self.assertNotIn(clone_a, second, "no result may point at the other clone's path: " + second)

if __name__ == "__main__":
    unittest.main()
