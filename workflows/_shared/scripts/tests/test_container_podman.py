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
import shutil
import tempfile
import unittest
from typing import Any, Dict, List, Optional, Tuple

from pfstep import container, stepenv
from pfstep.container import ContainerOptions, ContainerSpec, DockerCLI, ImageMissing, PodmanCLI

from .fakebox import FakeRuntime

# A local image ID as podman answers it (docker adds the sha256: prefix;
# the backend normalizes — callers and the cache volume's generation speak
# sha256:).
HEX = "ab" * 32
DIGEST = "repo.example/img@sha256:%s" % ("cd" * 32)

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
        rt = o.runtime
        assert rt is not None
        assert isinstance(rt, PodmanCLI), "podman is PodmanCLI"
        self.assertEqual(rt.bin, "podman", "no podman on the test PATH: the bare name, available() says it is missing")

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
        assert o.runtime is fb, "a caller's own runtime (a test's fake, verify.py's --image) keeps its place"
        self.assertIs(o.runtime, fb)

    def test_engine_for_env_selects_without_the_options(self) -> None:
        self.assertIsInstance(container.engine_for_env({"PATH": "/usr/bin:/bin"}), DockerCLI)
        self.assertIsInstance(
            container.engine_for_env({"PATH": "/usr/bin:/bin", stepenv.ENV_CONTAINER_ENGINE: "podman"}), PodmanCLI)


class PodmanCLITest(unittest.TestCase):
    """The backend's own semantics at the points the engines differ: the
    bare local image ID (normalized to sha256:), podman's missing-image
    answer (image not known → ImageMissing, the same never-pulled
    contract), the ps label template, the volume listing (the engine
    answers dangling only; the name's containment is this side's AND),
    the build's private crun --no-new-keyring wrapper (generated, passed
    as --runtime, removed when the build ends however it ended) and the
    pinned default bridge network. The engine is a fake podman — a shell
    script recording its argv — so the real one-process-per-call path
    runs; a real rootful podman is the 84-image smoke's, not this
    suite's."""

    def podman(self, body: str) -> Tuple[PodmanCLI, str]:
        """A fake podman executable answering body's cases; every call's
        argv appends to <script>.argv. Answers (cli, argv-record path)."""
        d = tempfile.mkdtemp(prefix="pf-podman-fake-")
        self.addCleanup(shutil.rmtree, d, ignore_errors=True)
        script = os.path.join(d, "podman")
        with open(script, "w", encoding="utf-8") as f:
            f.write('#!/bin/sh\nprintf \'%s\\n\' "$*" >> "$0.argv"\n' + body)
        os.chmod(script, 0o755)
        return PodmanCLI(bin=script), script + ".argv"

    @staticmethod
    def recorded(path: str) -> List[str]:
        with open(path, encoding="utf-8") as f:
            return [line.rstrip("\n") for line in f if line.strip()]

    @staticmethod
    def run_lines(argv: List[str], marker: str) -> List[str]:
        """The words of the one recorded call whose argv holds marker."""
        for line in argv:
            words = line.split()
            if marker in words:
                return words
        return []

    def test_a_bare_local_id_is_normalized_to_sha256(self) -> None:
        cli, _ = self.podman('case "$1" in image) case "$2" in inspect) echo %s; echo "[]" ;; esac ;; esac\n' % HEX)
        ident = cli.image_identity("registry.example/img:1")
        self.assertEqual(ident.id, "sha256:" + HEX, "podman answers the bare digest; the callers speak sha256:")
        self.assertEqual(ident.repo_digests, [])

    def test_an_id_already_prefixed_keeps_its_one_prefix(self) -> None:
        cli, _ = self.podman('case "$1" in image) case "$2" in inspect) echo sha256:%s; echo [\'%s\'] ;; esac ;; esac\n'
                             % (HEX, DIGEST))
        ident = cli.image_identity("registry.example/img:1")
        self.assertEqual(ident.id, "sha256:" + HEX)
        self.assertEqual(ident.repo_digests, [DIGEST])

    def test_an_unreadable_id_is_an_engine_error(self) -> None:
        cli, _ = self.podman('case "$1" in image) case "$2" in inspect) echo not-an-id; echo "[]" ;; esac ;; esac\n')
        with self.assertRaises(container.ContainerError) as got:
            cli.image_identity("registry.example/img:1")
        self.assertIn("no readable local image ID", str(got.exception))
        self.assertIn("podman", str(got.exception))

    def test_image_not_known_is_a_missing_image_nothing_else_is(self) -> None:
        cli, _ = self.podman('case "$1" in image) case "$2" in inspect) echo "Error: image not known" >&2; exit 125 ;; esac ;; esac\n')
        with self.assertRaises(ImageMissing):
            cli.image_identity("registry.example/nope:1")
        cli2, _ = self.podman('case "$1" in image) case "$2" in inspect) echo "Error: cannot connect to the podman socket" >&2; exit 125 ;; esac ;; esac\n')
        with self.assertRaises(container.ContainerError) as got:
            cli2.image_identity("registry.example/nope:1")
        self.assertNotIsInstance(got.exception, ImageMissing, "an engine failure is never a missing image")

    def test_the_label_listing_uses_podmans_own_template(self) -> None:
        cli, rec = self.podman('case "$1" in ps) printf \'cid-1\\t1717000000\\ncid-2\\t\\n\' ;; esac\n')
        found = cli.list_labelled({container.LABEL_DAEMON: "/srv/pf"}, container.LABEL_EXPIRES)
        self.assertEqual(found, {"cid-1": "1717000000", "cid-2": ""})
        words = self.run_lines(self.recorded(rec), "--all")
        self.assertIn("--filter", words)
        self.assertIn("label=" + container.LABEL_DAEMON + "=/srv/pf", words, "the label filter selects the owner's")
        fmt_i = words.index("--format") + 1
        self.assertEqual(words[fmt_i], '{{.ID}}\t{{.Labels "%s"}}' % container.LABEL_EXPIRES,
                         "docker's {{.Label \"…\"}} is not a template podman answers; its own is {{.Labels \"…\"}}")

    def test_the_volume_listing_ands_the_name_on_this_side(self) -> None:
        prefix = "pf-verify-cache-app-"
        mine = prefix + HEX + "-" + HEX
        other_repo = "pf-verify-cache-web-" + HEX + "-" + HEX
        old_shape = prefix + "old"
        cli, rec = self.podman('case "$1" in volume) case "$2" in ls) printf \'%s\\n%s\\n%s\\n\' %s %s %s ;; esac ;; esac\n'
                               % (mine, other_repo, old_shape, mine, other_repo, old_shape))
        self.assertEqual(cli.unused_volumes(prefix), [mine, old_shape],
                         "the name's containment is this side's: another repo's volume never reaches the answer")
        words = self.run_lines(self.recorded(rec), "volume")
        self.assertIn("dangling=true", words, "the engine answers the dangling ones")
        for bad in ("name=", "dangling=false"):
            self.assertNotIn(bad, " ".join(words), "no second engine filter: the AND is this side's")

    def test_a_build_runs_under_a_private_crun_wrapper_that_ends_with_it(self) -> None:
        cli, rec = self.podman(
            'case "$1" in\n'
            '  build)\n'
            '    prev=""; rt=""\n'
            '    for a in "$@"; do\n'
            '      if [ "$prev" = "--runtime" ]; then rt="$a"; fi\n'
            '      prev="$a"\n'
            '    done\n'
            '    if [ -n "$rt" ]; then\n'
            '      if [ -x "$rt" ]; then echo executable > "$0.runtime"; else echo missing > "$0.runtime"; fi\n'
            '      sed -n "1,2p" "$rt" >> "$0.runtime"\n'
            '    fi\n'
            '    cat > "$0.stdin" ;;\n'
            'esac\n')
        dockerfile = b"FROM registry.example/img:1\n"
        log_path = os.path.join(tempfile.gettempdir(), "pf-podman-build.log")
        with open(log_path, "wb") as log:
            cli.build_image("pf-verify-app:tag", dockerfile, {container.LABEL_REPO: "app"}, log, 30.0)
        script = cli.bin
        words = self.run_lines(self.recorded(rec), "build")
        self.assertIn("--runtime", words, "the build's containers run under the wrapper")
        wrapper = words[words.index("--runtime") + 1]
        with open(script + ".runtime", encoding="utf-8") as f:
            wrapper_record = [line.rstrip("\n") for line in f]
        self.assertEqual(wrapper_record[0], "executable", "the wrapper is an executable the engine can run")
        self.assertEqual(wrapper_record[1:], ["#!/bin/sh", 'exec crun --no-new-keyring "$@"'],
                         "the wrapper is crun with --no-new-keyring, nothing else")
        with open(script + ".stdin", "rb") as f:
            self.assertEqual(f.read(), dockerfile, "the Dockerfile rides stdin, the empty build context as ever")
        self.assertIn("--tag", words)
        self.assertIn(container.LABEL_REPO + "=app", words)
        self.assertFalse(os.path.exists(os.path.dirname(wrapper)),
                         "the wrapper and its directory are gone when the build ends")

    def test_a_failed_build_still_cleans_the_wrapper_and_names_podman(self) -> None:
        cli, rec = self.podman('case "$1" in build) cat > "$0.stdin"; echo "the build broke" >&2; exit 17 ;; esac\n')
        log_path = os.path.join(tempfile.gettempdir(), "pf-podman-build-fail.log")
        with self.assertRaises(container.ContainerError) as got:
            with open(log_path, "wb") as log:
                cli.build_image("pf-verify-app:tag", b"FROM x\n", {}, log, 30.0)
        self.assertIn("podman build", str(got.exception))
        words = self.run_lines(self.recorded(rec), "build")
        wrapper = words[words.index("--runtime") + 1] if "--runtime" in words else ""
        self.assertTrue(wrapper, "the wrapper was passed")
        self.assertFalse(os.path.exists(os.path.dirname(wrapper)), "a failed build leaves no wrapper behind")

    def test_a_container_starts_on_the_default_bridge(self) -> None:
        spec = ContainerSpec(name="n", image="sha256:" + HEX, pull_never=True)
        args = PodmanCLI().start_args(spec)
        self.assertIn("--network", args, "the bridge is pinned, not inherited from containers.conf")
        self.assertEqual(args[args.index("--network") + 1], "bridge")
        self.assertEqual(DockerCLI().start_args(spec).count("--network"), 0,
                         "the docker path gains no network flag of its own")
        # otherwise the same argv: the docker args with the network pair
        # spliced in after --log-driver none
        d = DockerCLI().start_args(spec)
        at = d.index("--log-driver") + 2
        self.assertEqual(args, d[:at] + ["--network", "bridge"] + d[at:])

    def test_failures_name_podman_not_docker(self) -> None:
        cli, _ = self.podman('case "$1" in version) echo "boom" >&2; exit 1 ;; esac\n')
        with self.assertRaises(container.ContainerError) as got:
            cli.available()
        self.assertIn("podman version", str(got.exception))
        self.assertNotIn("docker", str(got.exception).replace("podman", ""), "no docker name in a podman failure")
        missing = PodmanCLI(bin=os.path.join(tempfile.gettempdir(), "no-podman-here"))
        with self.assertRaises(container.ContainerError) as got2:
            missing.available()
        self.assertIn("the podman client", str(got2.exception))
        self.assertIn("is not found", str(got2.exception))


if __name__ == "__main__":
    unittest.main()
