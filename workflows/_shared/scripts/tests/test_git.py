"""pfstep.git: one hardened git call — its argv and environment, and that a
repository's fsmonitor and hooks, an inherited GIT_DIR and a hung git
cannot reach or stall it."""

import os
import shutil
import stat
import subprocess
import tempfile
import time
import unittest

from pfstep import git

from .procutil import alive

HAVE_GIT = shutil.which("git") is not None


def _sh(path: str, body: str) -> None:
    with open(path, "w", encoding="utf-8") as f:
        f.write("#!/bin/sh\n" + body)
    os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


class EnvTest(unittest.TestCase):
    def test_argv_carries_the_hardening(self) -> None:
        self.assertEqual(
            git.argv("status", "--porcelain"),
            ["git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
             "--no-optional-locks", "--literal-pathspecs", "status", "--porcelain"],
        )

    def test_env_drops_git_and_locale_and_keeps_the_path(self) -> None:
        e = git.env({
            "PATH": "/opt/bin:/usr/bin", "GIT_DIR": "/elsewhere",
            "GIT_CONFIG_GLOBAL": "/x", "GIT_EXTERNAL_DIFF": "/evil", "LC_ALL": "de_DE.UTF-8",
            "HOME": "/h", "PF_WI_ID": "wi_1",
        })
        self.assertEqual(e["PATH"], "/opt/bin:/usr/bin")
        self.assertEqual({k for k in e if k.startswith("GIT_")}, {"GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT"})
        self.assertEqual((e["GIT_CONFIG_NOSYSTEM"], e["GIT_TERMINAL_PROMPT"], e["LC_ALL"]), ("1", "0", "C"))
        self.assertEqual((e["HOME"], e["PF_WI_ID"]), ("/h", "wi_1"))


@unittest.skipUnless(HAVE_GIT, "git is not on PATH")
class GitCallTest(unittest.TestCase):
    def setUp(self) -> None:
        self._td = tempfile.TemporaryDirectory()
        self.dir = os.path.realpath(self._td.name)
        self.repo = os.path.join(self.dir, "repo")
        os.mkdir(self.repo)
        env = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", HOME=self.dir)
        for args in (["init", "-q"], ["config", "user.email", "t@example.invalid"], ["config", "user.name", "t"]):
            subprocess.run(["git", *args], cwd=self.repo, env=env, check=True)
        with open(os.path.join(self.repo, "a.txt"), "w", encoding="utf-8") as f:
            f.write("a\n")
        subprocess.run(["git", "add", "a.txt"], cwd=self.repo, env=env, check=True)
        subprocess.run(["git", "commit", "-qm", "one"], cwd=self.repo, env=env, check=True)
        self.env = {"PATH": os.environ["PATH"], "HOME": self.dir}

    def tearDown(self) -> None:
        self._td.cleanup()

    def test_a_pattern_pathspec_only_on_request(self) -> None:
        self.assertNotIn("--literal-pathspecs", git.argv("ls-files", literal_pathspecs=False))
        self.assertEqual(git.git(self.repo, "ls-files", "--", "*.txt", environ=self.env), "")
        self.assertEqual(git.git(self.repo, "ls-files", "--", "*.txt", environ=self.env, literal_pathspecs=False), "a.txt\n")

    def test_reads_and_reports_failures(self) -> None:
        head = git.git(self.repo, "rev-parse", "HEAD", environ=self.env).strip()
        self.assertRegex(head, r"^[0-9a-f]{40,64}$")
        with self.assertRaises(git.GitError) as cm:
            git.git(self.repo, "rev-parse", "--verify", "no-such-ref", environ=self.env)
        self.assertTrue(str(cm.exception).startswith("git rev-parse --verify no-such-ref: "), str(cm.exception))
        self.assertNotEqual(cm.exception.completed.exit_code, 0)

    def test_the_repositorys_programs_never_run(self) -> None:
        marker = os.path.join(self.dir, "ran")
        _sh(os.path.join(self.dir, "fsmon"), 'echo fsmonitor >> "%s"\n' % marker)
        hooks = os.path.join(self.dir, "hooks")
        os.mkdir(hooks)
        _sh(os.path.join(hooks, "post-checkout"), 'echo hook >> "%s"\n' % marker)
        cfg = dict(os.environ, HOME=self.dir)
        subprocess.run(["git", "config", "core.fsmonitor", os.path.join(self.dir, "fsmon")], cwd=self.repo, env=cfg, check=True)
        subprocess.run(["git", "config", "core.hooksPath", hooks], cwd=self.repo, env=cfg, check=True)
        git.git(self.repo, "status", "--porcelain", environ=self.env)
        git.git(self.repo, "checkout", "-q", "-b", "side", environ=self.env)
        self.assertFalse(os.path.exists(marker), "a repository program ran under the hardened git")
        # positive control: plain git runs them
        subprocess.run(["git", "checkout", "-q", "-b", "side2"], cwd=self.repo, env=cfg, check=True)
        self.assertTrue(os.path.exists(marker), "the control did not run the hook: the test proves nothing")

    def test_an_inherited_git_dir_does_not_redirect_it(self) -> None:
        other = os.path.join(self.dir, "other")
        os.mkdir(other)
        subprocess.run(["git", "init", "-q", other], check=True, env=dict(os.environ, HOME=self.dir))
        env = dict(self.env, GIT_DIR=os.path.join(other, ".git"))
        top = git.git(self.repo, "rev-parse", "--show-toplevel", environ=env).strip()
        self.assertEqual(top, self.repo)

    def test_a_hung_git_is_killed_at_its_timeout(self) -> None:
        fake = os.path.join(self.dir, "fakebin")
        os.mkdir(fake)
        pidfile = os.path.join(self.dir, "git.pid")
        _sh(os.path.join(fake, "git"), 'sleep 300 & echo $! > "%s"; wait\n' % pidfile)
        start = time.monotonic()
        with self.assertRaises(git.GitError) as cm:
            git.git(self.repo, "status", timeout=1.0, environ={"PATH": fake + ":" + os.environ["PATH"]})
        self.assertLess(time.monotonic() - start, 30)
        self.assertIn("timed out after 1s", str(cm.exception))
        self.assertTrue(cm.exception.completed.timed_out)
        with open(pidfile, encoding="utf-8") as f:
            pid = int(f.read())
        end = time.monotonic() + 5
        while alive(pid) and time.monotonic() < end:
            time.sleep(0.05)
        self.assertFalse(alive(pid), "the hung git's child outlived the timeout")

    def test_git_resolves_on_the_environments_path(self) -> None:
        fake = os.path.join(self.dir, "cmdpath")
        os.mkdir(fake)
        _sh(os.path.join(fake, "git"), 'echo from-the-step-path\n')
        out = git.git(self.repo, "status", environ={"PATH": fake + ":/usr/bin:/bin"})
        self.assertEqual(out, "from-the-step-path\n")


if __name__ == "__main__":
    unittest.main()
