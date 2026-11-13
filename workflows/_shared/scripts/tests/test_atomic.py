"""pfstep.atomic: a reader sees the old file or the new one, never part of
one — an interrupted write (an exception, a failed fsync, a SIGKILL between
the write and the rename) leaves the target as it was; a symlink target is
replaced, not written through; every write is a new inode."""

import os
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest import mock

from pfstep import atomic

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _leftovers(d: str, keep: "set[str]") -> "list[str]":
    return sorted(n for n in os.listdir(d) if n not in keep)


class AtomicWriteTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.dir = self._td.name
        self.target = os.path.join(self.dir, "result.json")

    def tearDown(self) -> None:
        self._td.cleanup()

    def read(self, p: str) -> bytes:
        with open(p, "rb") as f:
            return f.read()

    def test_writes_with_mode_and_creates_the_parent(self) -> None:
        p = os.path.join(self.dir, "a", "b", "out.json")
        atomic.write(p, b"{}\n", 0o640)
        self.assertEqual(self.read(p), b"{}\n")
        self.assertEqual(os.stat(p).st_mode & 0o777, 0o640)
        self.assertEqual(_leftovers(os.path.dirname(p), {"out.json"}), [])

    def test_a_symlink_is_replaced_not_written_through(self) -> None:
        other = os.path.join(self.dir, "other.json")
        with open(other, "wb") as f:
            f.write(b"untouched\n")
        os.symlink("other.json", self.target)
        atomic.write(self.target, b"new\n")
        self.assertFalse(os.path.islink(self.target))
        self.assertEqual(self.read(other), b"untouched\n")
        first = os.stat(self.target).st_ino
        atomic.write(self.target, b"second\n")
        self.assertNotEqual(os.stat(self.target).st_ino, first, "a rewrite must rename a new file over the old")
        self.assertEqual(self.read(self.target), b"second\n")

    def test_an_exception_before_the_rename_leaves_the_old_file(self) -> None:
        atomic.write(self.target, b"old complete\n")

        def boom() -> None:
            raise RuntimeError("interrupted")

        with self.assertRaises(RuntimeError):
            atomic.write(self.target, b"new" * 100000, before_rename=boom)
        self.assertEqual(self.read(self.target), b"old complete\n")
        self.assertEqual(_leftovers(self.dir, {"result.json"}), [], "the temp file is removed")

    def test_a_failed_fsync_leaves_the_old_file(self) -> None:
        atomic.write(self.target, b"old complete\n")
        with mock.patch("pfstep.atomic.os.fsync", side_effect=OSError(5, "EIO")):
            with self.assertRaises(OSError):
                atomic.write(self.target, b"new\n")
        self.assertEqual(self.read(self.target), b"old complete\n")
        self.assertEqual(_leftovers(self.dir, {"result.json"}), [])

    def test_a_kill_mid_write_never_leaves_a_half_file(self) -> None:
        # a writer SIGKILLed after its temp file is complete and before the
        # rename: the target still holds the old, whole document
        atomic.write(self.target, b"old complete\n")
        flag = os.path.join(self.dir, "ready")
        child = (
            "import os, sys, time\n"
            "sys.path.insert(0, sys.argv[1])\n"
            "from pfstep import atomic\n"
            "def hold():\n"
            "    open(sys.argv[3], 'w').close()\n"
            "    time.sleep(60)\n"
            "atomic.write(sys.argv[2], b'x' * (4 << 20), before_rename=hold)\n"
        )
        p = subprocess.Popen([sys.executable, "-B", "-c", child, SCRIPTS, self.target, flag])
        try:
            deadline = time.monotonic() + 20
            while not os.path.exists(flag):
                self.assertIsNone(p.poll(), "the writer died before its rename point")
                self.assertLess(time.monotonic(), deadline, "the writer never reached its rename point")
                time.sleep(0.02)
            os.kill(p.pid, signal.SIGKILL)
            p.wait()
        finally:
            if p.poll() is None:
                p.kill()
                p.wait()
        self.assertEqual(self.read(self.target), b"old complete\n")
        temps = _leftovers(self.dir, {"result.json", "ready"})
        self.assertEqual(len(temps), 1, temps)  # the killed writer's temp file, never the target
        self.assertTrue(temps[0].startswith(".result.json.") and temps[0].endswith(".tmp"), temps)


if __name__ == "__main__":
    unittest.main()
