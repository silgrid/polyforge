"""pfstep.gateid: the lane's own gate id, pinned byte for byte against
tools/gate-id — the pure core against the documented algorithm derived by
hand (tools/gate-id's header, docs/reference/release.md "The gate id"),
whole commits through both implementations (this repository's own HEAD
included, its real conf and Dockerfile), and the errors a commit or file
that is not there raises. tools/gate-id stays the release's; this library
is the gate's fallback for a tree that carries none (polyforge#29)."""

import hashlib
import os
import subprocess
import tempfile
import unittest
from typing import Tuple

from pfstep import gateid

from .gatefix import GATE_ID_SRC, HAVE_GIT, REPO_ROOT, commit_all, git_t, write_files

# A conf exercising the line rule: leading whitespace (a tab, a space and
# awk's [[:space:]] \v) comes off, comments and non-gate keys never hash,
# a key's trailing space keeps the line out, a line without "=" is not a
# gate line, and a last line without a trailing newline still prints with
# one.
CONF = (
    b"\t stack=none\n"
    b"  # a comment\n"
    b"full_command=echo full\n"
    b"forced_full=go.mod\n"
    b"checks=echo not-a-gate-key\n"
    b"fast_gate =echo spaced\n"
    b"no_equals_line\n"
    b"\n"
    b"check=n|fast|echo n\n"
    b"check_requires_env=n|FOO\n"
    b"check_requires_cmd=n|sh\n"
    b"check_timeout=n|90s\n"
    b"\x0bfull_command=echo vtab\n"
    b"full_command=echo no trailing newline"
)
# A CRLF conf: each kept line carries its \r to the hashed text (awk's
# record is the \n-delimited line, byte for byte).
CRLF_CONF = b"stack=none\r\n  fast_gate=echo one\r\nforced_full=x\r\n"
DOCKERFILE = b"FROM gate-id-test-image\nRUN true\n"
# a prepared image's local ID, as the gate hands it to the id (polyforge#64)
IMAGE_ID = "sha256:" + "ab" * 32


def gate_lines_of(conf: bytes) -> bytes:
    """CONF's and CRLF_CONF's gate lines as the algorithm keeps them
    (derived by hand, line by line, from the constants above)."""
    if conf is CONF:
        return (
            b"stack=none\n"
            b"full_command=echo full\n"
            b"check=n|fast|echo n\n"
            b"check_requires_env=n|FOO\n"
            b"check_requires_cmd=n|sh\n"
            b"check_timeout=n|90s\n"
            b"full_command=echo vtab\n"
            b"full_command=echo no trailing newline\n"
        )
    return b"stack=none\r\nfast_gate=echo one\r\n"


def documented_text(conf: bytes, dockerfile: bytes) -> bytes:
    """The text the id hashes, derived by hand from the documented
    algorithm: the header, the Dockerfile's sha256, then the gate lines."""
    d = hashlib.sha256(dockerfile).hexdigest().encode("ascii")
    return b"polyforge-gate-id 1\ndockerfile " + d + b"\n" + gate_lines_of(conf)


def documented_image_text(conf: bytes, dockerfile: bytes, image: str) -> bytes:
    """The text the id hashes with a prepared image, derived by hand the
    same way: the image's local-ID line follows the dockerfile one."""
    d = hashlib.sha256(dockerfile).hexdigest().encode("ascii")
    return (b"polyforge-gate-id 1\ndockerfile " + d + b"\nimage " + image.encode("ascii")
            + b"\n" + gate_lines_of(conf))


@unittest.skipUnless(os.path.isfile(GATE_ID_SRC), "the repository's tools/gate-id")
class ImageLineTest(unittest.TestCase):
    """The pure core with a prepared image (polyforge#64): an
    ``image <local ID>`` line follows the dockerfile one, and without an
    image the bytes are exactly what they were — the id of a repository
    that names no image never moves."""

    def test_the_image_line_follows_the_dockerfile_one(self) -> None:
        self.assertEqual(gateid.hashed_text(CONF, DOCKERFILE, IMAGE_ID),
                         documented_image_text(CONF, DOCKERFILE, IMAGE_ID))

    def test_without_an_image_the_bytes_are_unchanged(self) -> None:
        self.assertEqual(gateid.hashed_text(CONF, DOCKERFILE, ""), documented_text(CONF, DOCKERFILE))


@unittest.skipUnless(os.path.isfile(GATE_ID_SRC), "the repository's tools/gate-id")
class HashedTextTest(unittest.TestCase):
    """The pure core: the exact bytes of the documented algorithm."""

    def test_the_conf_line_rule(self) -> None:
        self.assertEqual(gateid.hashed_text(CONF, DOCKERFILE), documented_text(CONF, DOCKERFILE))

    def test_a_crlf_conf_keeps_its_carriage_returns(self) -> None:
        self.assertEqual(gateid.hashed_text(CRLF_CONF, DOCKERFILE), documented_text(CRLF_CONF, DOCKERFILE))

    def test_a_conf_with_no_gate_line_hashes_the_header_alone(self) -> None:
        d = hashlib.sha256(b"FROM x\n").hexdigest().encode("ascii")
        self.assertEqual(gateid.hashed_text(b"# nothing hashes\nnot_a_key=1\n", b"FROM x\n"),
                         b"polyforge-gate-id 1\ndockerfile " + d + b"\n")


@unittest.skipUnless(HAVE_GIT and os.path.isfile(GATE_ID_SRC), "git and the repository's tools/gate-id")
class ToolsGateIdConsistencyTest(unittest.TestCase):
    """Both implementations over the same commits: byte for byte."""

    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = os.path.realpath(self._td.name)

    def tearDown(self) -> None:
        self._td.cleanup()

    def fixture(self, name: str, conf: bytes, dockerfile: bytes) -> Tuple[str, str]:
        d = os.path.join(self.root, name)
        os.makedirs(d)
        git_t(d, "init", "-q", "-b", "main")
        write_files(d, {".polyforge/verify-scope.conf": conf.decode("utf-8"),
                        ".polyforge/Dockerfile.verify": dockerfile.decode("utf-8")})
        return d, commit_all(d, "tree")

    def sh_gate_id(self, cwd: str, *args: str) -> "subprocess.CompletedProcess[bytes]":
        return subprocess.run(["sh", GATE_ID_SRC, *args], cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def test_show_and_the_id_match_over_whole_commits(self) -> None:
        for name, conf in (("edge", CONF), ("crlf", CRLF_CONF)):
            with self.subTest(name):
                d, rev = self.fixture(name, conf, DOCKERFILE)
                sh = self.sh_gate_id(d, "--show", rev)
                self.assertEqual(sh.returncode, 0, sh.stderr)
                self.assertEqual(gateid.show(d, rev), sh.stdout, "the lane's --show text differs from tools/gate-id's")
                shid = self.sh_gate_id(d, rev)
                self.assertEqual(shid.returncode, 0, shid.stderr)
                self.assertEqual(gateid.gate_id(d, rev), shid.stdout.decode("utf-8").strip())

    def test_the_repository_itself_at_head(self) -> None:
        sh = self.sh_gate_id(REPO_ROOT, "--show", "HEAD")
        self.assertEqual(sh.returncode, 0, sh.stderr)
        self.assertEqual(gateid.show(REPO_ROOT, "HEAD"), sh.stdout,
                         "the lane's gate id moved on this repository's own gate")
        shid = self.sh_gate_id(REPO_ROOT, "HEAD")
        self.assertEqual(gateid.gate_id(REPO_ROOT, "HEAD"), shid.stdout.decode("utf-8").strip())

    def test_the_errors_mirror_tools_gate_id(self) -> None:
        d, rev = self.fixture("err", CONF, DOCKERFILE)
        os.remove(os.path.join(d, ".polyforge", "Dockerfile.verify"))
        gone = commit_all(d, "no image")
        sh = self.sh_gate_id(d, "--show", gone)
        self.assertEqual(sh.returncode, 2, "tools/gate-id without a Dockerfile")
        self.assertIn(b"Dockerfile.verify is not in the tree", sh.stderr)
        with self.assertRaises(gateid.GateIdError) as got:
            gateid.show(d, gone)
        self.assertIn("Dockerfile.verify", str(got.exception))
        with self.assertRaises(gateid.GateIdError):
            gateid.show(d, "no-such-rev")

    def test_with_an_image_both_implementations_match_byte_for_byte(self) -> None:
        for name, conf in (("edge", CONF), ("crlf", CRLF_CONF)):
            with self.subTest(name):
                d, rev = self.fixture(name + "-image", conf, DOCKERFILE)
                sh = self.sh_gate_id(d, "--image", IMAGE_ID, "--show", rev)
                self.assertEqual(sh.returncode, 0, sh.stderr)
                self.assertEqual(gateid.show(d, rev, image=IMAGE_ID), sh.stdout,
                                 "the lane's --show text with an image differs from tools/gate-id's")
                shid = self.sh_gate_id(d, "--show", "--image", IMAGE_ID, rev)
                self.assertEqual(shid.returncode, 0, shid.stderr)
                self.assertEqual(gateid.gate_id(d, rev, image=IMAGE_ID), shid.stdout.decode("utf-8").strip())
                # the image moves the id: never the no-image one
                self.assertNotEqual(gateid.gate_id(d, rev, image=IMAGE_ID), gateid.gate_id(d, rev))

    def test_a_tree_without_a_dockerfile_is_allowed_with_an_image(self) -> None:
        d, rev = self.fixture("bare-image", CONF, DOCKERFILE)
        os.remove(os.path.join(d, ".polyforge", "Dockerfile.verify"))
        gone = commit_all(d, "no Dockerfile, a prepared image instead")
        want = b"polyforge-gate-id 1\nimage " + IMAGE_ID.encode("ascii") + b"\n" + gate_lines_of(CONF)
        sh = self.sh_gate_id(d, "--image", IMAGE_ID, "--show", gone)
        self.assertEqual(sh.returncode, 0, sh.stderr)
        self.assertEqual(sh.stdout, want, "tools/gate-id leaves the dockerfile line out")
        self.assertEqual(gateid.show(d, gone, image=IMAGE_ID), want,
                         "the lane's gate id leaves the dockerfile line out, byte for byte")
        self.assertEqual(gateid.gate_id(d, gone, image=IMAGE_ID),
                         self.sh_gate_id(d, "--image", IMAGE_ID, gone).stdout.decode("utf-8").strip())


if __name__ == "__main__":
    unittest.main()
