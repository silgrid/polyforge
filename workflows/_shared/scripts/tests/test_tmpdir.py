"""pfstep.tmpdir: a private TMPDIR is locked while its maker lives; the sweep
removes only a marked directory whose lock is free (its maker died), never
a live one or an unmarked one."""

import os
import signal
import subprocess
import sys
import tempfile
import unittest

from pfstep import tmpdir

SCRIPTS = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


class TmpDirTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.root = self._td.name

    def tearDown(self) -> None:
        self._td.cleanup()

    def test_live_dir_survives_the_sweep_and_release_removes_it(self) -> None:
        with tmpdir.new(self.root) as t:
            self.assertTrue(os.path.basename(t.path).startswith(tmpdir.DIR_PREFIX))
            self.assertTrue(os.path.isfile(os.path.join(t.path, tmpdir.OWNER_LOCK)))
            self.assertEqual(tmpdir.sweep_stale(self.root), [])
            self.assertTrue(os.path.isdir(t.path))
            path = t.path
        self.assertFalse(os.path.exists(path))
        self.assertEqual(os.listdir(self.root), [], "no staging dir is left")

    def test_a_killed_makers_dir_is_swept(self) -> None:
        child = (
            "import sys, time\n"
            "sys.path.insert(0, sys.argv[1])\n"
            "from pfstep import tmpdir\n"
            "t = tmpdir.new(sys.argv[2])\n"
            "print(t.path, flush=True)\n"
            "time.sleep(60)\n"
        )
        p = subprocess.Popen([sys.executable, "-B", "-c", child, SCRIPTS, self.root], stdout=subprocess.PIPE)
        try:
            assert p.stdout is not None
            path = p.stdout.readline().decode().strip()
            self.assertTrue(os.path.isdir(path), path)
            self.assertEqual(tmpdir.sweep_stale(self.root), [], "a live maker's dir was swept")
            os.kill(p.pid, signal.SIGKILL)
            p.wait()
        finally:
            if p.poll() is None:
                p.kill()
                p.wait()
            if p.stdout is not None:
                p.stdout.close()
        self.assertTrue(os.path.isdir(path), "SIGKILL leaves the dir behind")
        self.assertEqual(tmpdir.sweep_stale(self.root), [path])
        self.assertFalse(os.path.exists(path))

    def test_unmarked_or_unrelated_dirs_are_left(self) -> None:
        bare = os.path.join(self.root, tmpdir.DIR_PREFIX + "nolock")
        os.mkdir(bare)
        other = os.path.join(self.root, "pf-other")
        os.mkdir(other)
        with open(os.path.join(other, tmpdir.OWNER_LOCK), "w", encoding="utf-8"):
            pass
        self.assertEqual(tmpdir.sweep_stale(self.root), [])
        self.assertTrue(os.path.isdir(bare) and os.path.isdir(other))

    def test_release_is_idempotent(self) -> None:
        t = tmpdir.new(self.root)
        t.release()
        t.release()
        self.assertFalse(os.path.exists(t.path))


if __name__ == "__main__":
    unittest.main()
