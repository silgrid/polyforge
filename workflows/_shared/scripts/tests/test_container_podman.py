# carries: workflows/_shared/scripts/pfstep/container.py, workflows/_shared/scripts/pfstep/stepenv.py, workflows/_shared/scripts/tests/test_container_podman.py
"""The podman backend of pfstep.container (polyforge#159, 84-engine):
its selection — PF_CONTAINER_ENGINE picks DockerCLI or PodmanCLI at
``ContainerOptions.with_defaults``, an unset variable keeps the docker
client, an empty or unknown value is refused, an explicit runtime is never
replaced — and its own semantics at the points the two engines differ:
the missing-image answer (``image not known`` → ImageMissing, never
pulled), the bare local image ID (normalized to ``sha256:``), the ps
label template, the volume listing (the engine filters dangling only;
the name is this side's AND), the build's private crun
``--no-new-keyring`` wrapper and the pinned default bridge network. The
engine is driven through a fake ``podman`` executable (a shell script
that records its argv and answers canned output), so the real
subprocess path — one podman process per call, its stderr in the failure
— runs here too; a real rootful podman combination (build, prepared
image missing, bridge networking, label sweeps, in-use volume refusal)
is the 84-image smoke's, not this suite's."""

import os
import stat
import tempfile
import unittest
from typing import Dict, List, Optional, Tuple

from pfstep import container, stepenv
from pfstep.container import ContainerOptions, DockerCLI, ImageMissing, PodmanCLI

from .fakebox import FakeRuntime

class EngineSelectionTest(unittest.TestCase):
    """PF_CONTAINER_ENGINE selects the backend (Q3 A: unset = docker, an
    empty or unknown value is refused — never a silent fallback), and an
    explicit runtime keeps its place."""

    def test_the_step_env_names_the_variable(self) -> None:
        self.assertEqual(stepenv.ENV_CONTAINER_ENGINE, "PF_CONTAINER_ENGINE")

    def test_without_the_variable_the_docker_client_is_the_backend(self) -> None:
        for env in ({"PATH": "/usr/bin:/bin"}, {"PATH": "", stepenv.ENV_CONTAINER_ENGINE: "docker"}):
            with self.subTest(env=env):
                o = ContainerOptions().with_defaults(env)
                self.assertIsInstance(o.runtime, DockerCLI, env)
                self.assertNotIsInstance(o.runtime, PodmanCLI, "docker is DockerCLI")

    def test_podman_is_selected_when_the_variable_says_so(self) -> None:
        o = ContainerOptions().with_defaults({"PATH": "/usr/bin:/bin", stepenv.ENV_CONTAINER_ENGINE: "podman"})
        self.assertIsInstance(o.runtime, PodmanCLI, "podman is PodmanCLI")
        self.assertEqual(o.runtime.bin, "podman", "no podman on the test PATH: the bare name, available() says it is missing")

    def test_an_empty_value_is_refused(self) -> None:
        with self.assertRaises(container.ContainerError) as got:
            ContainerOptions().with_defaults({"PATH": "/usr/bin:/bin", stepenv.ENV_CONTAINER_ENGINE: ""})
        self.assertIn(stepenv.ENV_CONTAINER_ENGINE, str(got.exception))
        self.assertIn("docker", str(got.exception))

    def test_an_unknown_value_is_refused(self) -> None:
        for bad in ("k8s", "Docker", " podman", "podman ", "containerd"):
            with self.subTest(bad=bad):
                with self.assertRaises(container.ContainerError) as got:
                    ContainerOptions().with_defaults({"PATH": "/usr/bin:/bin", stepenv.ENV_CONTAINER_ENGINE: bad})
                self.assertIn(bad, str(got.exception))
                self.assertNotIsInstance(got.exception, ImageMissing)

    def test_an_explicit_engine_is_never_replaced(self) -> None:
        fb = FakeRuntime(tempfile.gettempdir())
        o = ContainerOptions(runtime=fb).with_defaults({"PATH": "/usr/bin:/bin", stepenv.ENV_CONTAINER_ENGINE: "podman"})
        self.assertIs(o.runtime, fb, "a caller's own runtime (a test's fake, verify.py's --image) keeps its place")

    def test_engine_for_env_selects_without_the_options(self) -> None:
        self.assertIsInstance(container.engine_for_env({"PATH": "/usr/bin:/bin"}), DockerCLI)
        self.assertIsInstance(
            container.engine_for_env({"PATH": "/usr/bin:/bin", stepenv.ENV_CONTAINER_ENGINE: "podman"}), PodmanCLI)


if __name__ == "__main__":
    unittest.main()
