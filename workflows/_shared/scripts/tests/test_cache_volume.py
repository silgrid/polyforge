# carries: workflows/_shared/scripts/pfstep/container.py
"""A repo's cache volume follows its image's generation: the volume
mounted at /cache is pf-verify-cache-<repo>-<the image tag's hash>, so a
change to any byte of the Dockerfile gives a new, empty volume, and a
start removes the repo's older generations no container references — never
another repo's volume, one of another shape, or one in use. Over the fake
engine (tests/fakebox.py), and the engine's own "unused" and "in use"
answers over a real docker (skipped without one)."""

import hashlib
import os
import secrets
import tempfile
import time
import unittest
from typing import List

from pfstep import container, proc, runner
from pfstep.container import Bind, ContainerOptions, ContainerSpec
from pfstep.runner import CommandOptions

from .capsuleutil import HAVE_GIT, TEST_DOCKERFILE, commit, make_capsule, write_files
from .fakebox import FakeRuntime
from .test_container_docker import ENGINE, PROBE_IMAGE

OTHER_DOCKERFILE = "FROM scratch-test-image\nRUN echo another pin\n"


def generation(dockerfile: str) -> str:
    return hashlib.sha256(dockerfile.encode()).hexdigest()[:32]


def old(n: int) -> str:
    """An older generation's hash (32 hex)."""
    return ("%x" % n) * 32


@unittest.skipUnless(HAVE_GIT, "git is not on PATH")
class CacheVolumeTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)
        self.capsule = make_capsule(self.root, "app", {"a.txt": "a\n", container.DOCKERFILE_VERIFY: TEST_DOCKERFILE})
        self.clone = os.path.join(self.capsule, "repos", "app")
        tmp_root = os.path.join(self.root, "box-tmp")
        os.mkdir(tmp_root)
        self.fb = FakeRuntime(tmp_root)
        self.said: List[str] = []
        self.runs = 0

    def tearDown(self) -> None:
        self._td.cleanup()

    def run_once(self) -> runner.CommandResult:
        self.runs += 1
        res = runner.run_command(CommandOptions(
            capsule_dir=self.capsule, repo="app", command="true", deadline=time.time() + 60, timeout=60.0,
            log_dir=os.path.join(self.capsule, "runtime-state", "run", str(self.runs), "app"), run=self.runs,
            container=ContainerOptions(runtime=self.fb, owner="/srv/pf-data", boot="boot-1", wi="wi_x", say=self.said.append),
        ))
        self.assertEqual(res.exit, 0, "the run's verdict does not change")
        return res

    def test_the_volume_is_the_image_generation(self) -> None:
        a = "pf-verify-cache-app-" + generation(TEST_DOCKERFILE)
        self.run_once()
        self.run_once()
        for st in self.fb.starts:
            self.assertEqual(st.volumes, {a: "/cache"}, "one volume, the image's generation, and nothing else cached")
            self.assertEqual(a.rsplit("-", 1)[1], st.image.split(":", 1)[1], "the volume's hash is the image tag's")
            self.assertEqual(st.binds, [Bind(self.clone)], "no node_modules or other directory is mounted beside the clone")
        write_files(self.clone, {container.DOCKERFILE_VERIFY: OTHER_DOCKERFILE})
        commit(self.clone, self.root, "move a pin")
        self.run_once()
        b = "pf-verify-cache-app-" + generation(OTHER_DOCKERFILE)
        self.assertNotEqual(a, b)
        self.assertEqual(self.fb.starts[2].volumes, {b: "/cache"}, "a changed Dockerfile gets a new volume")
        self.assertEqual(self.fb.starts[2].image.split(":", 1)[1], generation(OTHER_DOCKERFILE))

    def test_a_start_removes_the_repos_unused_older_generations(self) -> None:
        current = "pf-verify-cache-app-" + generation(TEST_DOCKERFILE)
        unused_old = "pf-verify-cache-app-" + old(1)
        in_use_old = "pf-verify-cache-app-" + old(2)
        kept = [
            "pf-verify-cache-app-web-" + old(3),  # another repo whose name starts with this one's
            "pf-verify-cache-other-" + old(4),
            "pf-verify-cache-app",  # the name before generations: left to the upgrade's manual step
            "pf-verify-cache-app-" + old(5)[:16],  # not a generation's shape
            "unrelated",
        ]
        self.fb.volumes.update([unused_old, in_use_old, *kept])
        # another verification, still running on the older image
        self.fb.containers["other-run"] = {container.LABEL_DAEMON: "/srv/other-daemon"}
        self.fb.mounts["other-run"] = [in_use_old]
        self.run_once()
        self.assertEqual(self.fb.volume_removes, [unused_old])
        self.assertEqual(sorted(self.fb.volumes), sorted([current, in_use_old, *kept]))
        lines = [s for s in self.said if "volume" in s]
        self.assertEqual(len(lines), 1, self.said)
        self.assertIn("removed", lines[0])
        self.assertIn(unused_old, lines[0], "the line names the removed volume")
        # once that verification's container is gone, its volume goes too
        del self.fb.containers["other-run"]
        self.run_once()
        self.assertEqual(self.fb.volume_removes, [unused_old, in_use_old])
        self.assertEqual(sorted(self.fb.volumes), sorted([current, *kept]))

    def test_a_failing_listing_or_removal_costs_only_a_message(self) -> None:
        olds = ["pf-verify-cache-app-" + old(1), "pf-verify-cache-app-" + old(2)]
        self.fb.volumes.update(olds)
        self.fb.volume_list_err = "engine hung"
        self.run_once()
        self.assertEqual(len(self.fb.starts), 1, "the container starts")
        self.assertEqual(self.fb.volume_removes, [])
        self.assertEqual(len([s for s in self.said if "could not list" in s and "engine hung" in s and "volume" in s]), 1, self.said)
        self.said.clear()
        self.fb.volume_list_err, self.fb.volume_remove_err = "", "volume busy"
        self.run_once()
        self.assertEqual(len(self.fb.starts), 2, "the container starts")
        self.assertEqual(self.fb.volume_removes, olds, "every older generation is still tried")
        for v in olds:
            self.assertEqual(len([s for s in self.said if "could not remove" in s and v in s and "volume busy" in s]), 1, self.said)
        self.assertTrue(set(olds) <= self.fb.volumes)


@unittest.skipUnless(ENGINE is not None, "no docker engine (or %s cannot be had)" % PROBE_IMAGE)
class RealDockerVolumeTest(unittest.TestCase):
    def setUp(self) -> None:
        assert ENGINE is not None
        self.d = ENGINE
        self.owner = "pfstep-vol-" + secrets.token_hex(6)
        self.prefix = "pfstep-vol-" + secrets.token_hex(6) + "-"
        self.volume = self.prefix + "a"

    def tearDown(self) -> None:
        try:
            for cid in self.d.list_labelled({container.LABEL_DAEMON: self.owner}, container.LABEL_BOOT):
                try:
                    self.d.remove(cid)
                except container.ContainerError:
                    pass
        except container.ContainerError:
            pass
        proc.run([self.d.bin, "volume", "rm", "--force", self.volume], timeout=60)

    def test_docker_lists_only_unused_volumes_and_refuses_one_in_use(self) -> None:
        c = proc.run([self.d.bin, "volume", "create", self.volume], timeout=60)
        self.assertEqual(c.exit_code, 0, c.stderr)
        self.assertEqual(self.d.unused_volumes(self.prefix), [self.volume])
        cid = self.d.start(ContainerSpec(
            name=self.owner, image=PROBE_IMAGE, labels={container.LABEL_DAEMON: self.owner, container.LABEL_BOOT: "b"},
            volumes={self.volume: container.CONTAINER_CACHE_DIR}, cpus=1, memory="256m", pids=64,
        ))
        self.assertEqual(self.d.unused_volumes(self.prefix), [], "a volume a container references is not unused")
        with self.assertRaises(container.ContainerError):
            self.d.remove_volume(self.volume)
        self.d.remove(cid)
        self.assertEqual(self.d.unused_volumes(self.prefix), [self.volume])
        self.d.remove_volume(self.volume)
        self.assertEqual(self.d.unused_volumes(self.prefix), [])


if __name__ == "__main__":
    unittest.main()
