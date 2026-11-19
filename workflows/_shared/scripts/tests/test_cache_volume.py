# carries: workflows/_shared/scripts/pfstep/container.py, workflows/_shared/scripts/pfstep/runner.py
"""A repo's cache volume follows its image's generation and its clone
path (polyforge#153): the volume mounted at /cache is
pf-verify-cache-<repo>-<the image tag's hash>-<the clone path's identity>,
so a change to any byte of the Dockerfile gives a new, empty volume, and
two clones at different paths never share one volume's path-sensitive
caches while the same path always reuses its own — however the path is
spelled (a trailing slash, a dot, a relative path, a symlink alias). A
start removes the repo's older generations no container references —
never another generation's volume, another repo's, one of another shape
(including the pre-path shared shape, left to the upgrade's manual
step), or one in use. Over the fake engine (tests/fakebox.py), and the
engine's own "unused" and "in use" answers over a real docker (skipped
without one)."""

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

from .capsuleutil import HAVE_GIT, TEST_DOCKERFILE, commit, make_capsule, path_id, write_files
from .fakebox import FakeRuntime
from .test_container_docker import ENGINE, PROBE_IMAGE

OTHER_DOCKERFILE = "FROM scratch-test-image\nRUN echo another pin\n"


def generation(dockerfile: str) -> str:
    return hashlib.sha256(dockerfile.encode()).hexdigest()[:32]


def volume_of(image_generation: str, clone_path: str) -> str:
    """A cache volume name, derived from the ruling's formula (never from
    the module under test): the image generation and the clone path's
    identity as two fields."""
    return "pf-verify-cache-app-" + image_generation + "-" + path_id(clone_path)


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

    def run_in(self, capsule: str) -> runner.CommandResult:
        self.runs += 1
        res = runner.run_command(CommandOptions(
            capsule_dir=capsule, repo="app", command="true", deadline=time.time() + 60, timeout=60.0,
            log_dir=os.path.join(capsule, "runtime-state", "run", str(self.runs), "app"), run=self.runs,
            container=ContainerOptions(runtime=self.fb, owner="/srv/pf-data", boot="boot-1", wi="wi_x", say=self.said.append),
        ))
        self.assertEqual(res.exit, 0, "the run's verdict does not change")
        return res

    def run_once(self) -> runner.CommandResult:
        return self.run_in(self.capsule)

    def test_the_volume_is_the_image_generation_and_the_clone_path(self) -> None:
        a = volume_of(generation(TEST_DOCKERFILE), self.clone)
        self.assertEqual(container.cache_volume("app", TEST_DOCKERFILE.encode(), self.clone), a)
        self.run_once()
        self.run_once()
        for st in self.fb.starts:
            self.assertEqual(st.volumes, {a: "/cache"}, "one volume, the image's generation and this clone path's")
            self.assertEqual(st.image.split(":", 1)[1], generation(TEST_DOCKERFILE),
                             "the volume's generation field is the image tag's")
            self.assertEqual(st.binds, [Bind(self.clone)], "no node_modules or other directory is mounted beside the clone")
        write_files(self.clone, {container.DOCKERFILE_VERIFY: OTHER_DOCKERFILE})
        commit(self.clone, self.root, "move a pin")
        self.run_once()
        b = volume_of(generation(OTHER_DOCKERFILE), self.clone)
        self.assertNotEqual(a, b)
        self.assertEqual(self.fb.starts[2].volumes, {b: "/cache"}, "a changed Dockerfile gets a new volume")
        self.assertEqual(self.fb.starts[2].image.split(":", 1)[1], generation(OTHER_DOCKERFILE))

    def test_two_clone_paths_never_share_one_cache_volume(self) -> None:
        second = os.path.join(self.root, "second")
        capsule = make_capsule(second, "app", {"a.txt": "a\n", container.DOCKERFILE_VERIFY: TEST_DOCKERFILE})
        other_clone = os.path.join(capsule, "repos", "app")
        self.run_once()
        self.run_in(capsule)
        gen = generation(TEST_DOCKERFILE)
        self.assertEqual([st.volumes for st in self.fb.starts],
                         [{volume_of(gen, self.clone): "/cache"}, {volume_of(gen, other_clone): "/cache"}],
                         "the same repo and image at two paths mount two volumes")
        self.assertNotEqual(self.fb.starts[0].volumes, self.fb.starts[1].volumes,
                            "another path's clone never shares this one's cache volume")

    def test_the_volume_key_is_the_canonical_clone_path(self) -> None:
        real = os.path.join(self.root, "real-clone")
        os.mkdir(real)
        alias = os.path.join(self.root, "alias")
        os.symlink(real, alias)
        a = container.cache_volume("app", TEST_DOCKERFILE.encode(), real)
        for spelling in (real + "/", os.path.join(self.root, ".", "real-clone"), alias,
                         os.path.relpath(real, os.getcwd())):
            self.assertEqual(container.cache_volume("app", TEST_DOCKERFILE.encode(), spelling), a,
                             "the spelling %r is the same clone path" % spelling)
        other = os.path.join(self.root, "other-clone")
        os.mkdir(other)
        self.assertNotEqual(container.cache_volume("app", TEST_DOCKERFILE.encode(), other), a,
                            "a different clone path is a different volume")

    def test_a_start_removes_the_repos_unused_older_generations(self) -> None:
        other_clone = os.path.join(self.root, "other-clone")
        os.mkdir(other_clone)
        current = volume_of(generation(TEST_DOCKERFILE), self.clone)
        unused_old = volume_of(old(1), self.clone)
        unused_old_other_path = volume_of(old(1), other_clone)  # the same older generation, another path's
        in_use_old = volume_of(old(2), self.clone)
        kept = [
            "pf-verify-cache-web-" + old(3) + "-" + path_id(self.clone),  # another repo whose name starts with this one's
            "pf-verify-cache-other-" + old(4) + "-" + path_id(self.clone),
            "pf-verify-cache-app-" + old(5),  # the pre-path shared shape: never touched, left to the upgrade's manual step
            "pf-verify-cache-app",  # the name before generations
            "pf-verify-cache-app-" + old(6)[:16],  # not a generation's shape
            "pf-verify-cache-app-" + old(7) + "-" + old(7)[:16],  # a path field of the wrong length
            "unrelated",
        ]
        self.fb.volumes.update([unused_old, unused_old_other_path, in_use_old, *kept])
        # another verification, still running on the older image
        self.fb.containers["other-run"] = {container.LABEL_DAEMON: "/srv/other-daemon"}
        self.fb.mounts["other-run"] = [in_use_old]
        self.run_once()
        self.assertEqual(self.fb.volume_removes, sorted([unused_old, unused_old_other_path]))
        self.assertEqual(sorted(self.fb.volumes), sorted([current, in_use_old, *kept]))
        for v in (unused_old, unused_old_other_path):
            self.assertEqual(len([s for s in self.said if "removed" in s and v in s]), 1, self.said)
        # once that verification's container is gone, its volume goes too
        del self.fb.containers["other-run"]
        self.run_once()
        self.assertEqual(self.fb.volume_removes, sorted([unused_old, unused_old_other_path, in_use_old]))
        self.assertEqual(sorted(self.fb.volumes), sorted([current, *kept]))

    def test_a_start_keeps_the_current_generations_other_clone_paths(self) -> None:
        """A start keeps every clone path's volume of the running image
        generation (another clone's cache is not this start's to reap) and
        removes only the older generations': the pruning rule reads the
        name's generation field, not the whole name."""
        other_clone = os.path.join(self.root, "other-clone")
        os.mkdir(other_clone)
        same_gen_other = volume_of(generation(TEST_DOCKERFILE), other_clone)  # this generation, another path, unused
        older_other = volume_of(old(9), other_clone)
        older_here = volume_of(old(9), self.clone)
        self.fb.volumes.update([same_gen_other, older_other, older_here])
        self.run_once()
        self.assertEqual(self.fb.volume_removes, sorted([older_other, older_here]),
                         "only the older generation's volumes go")
        self.assertTrue(same_gen_other in self.fb.volumes,
                        "another clone path's volume of the running image generation stays")
        self.assertEqual([s.volumes for s in self.fb.starts],
                         [{volume_of(generation(TEST_DOCKERFILE), self.clone): "/cache"}] * len(self.fb.starts))

    def test_a_failing_listing_or_removal_costs_only_a_message(self) -> None:
        other_clone = os.path.join(self.root, "other-clone")
        os.mkdir(other_clone)
        olds = [volume_of(old(1), self.clone), volume_of(old(2), other_clone)]
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
        self.assertEqual(self.fb.volume_removes, sorted(olds), "every older generation is still tried")
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
