"""A private TMPDIR per command, and the sweep of the ones a killed run left.

A command's TMPDIR is removed when the command is done, so a run killed
hard (SIGKILL with its step) leaves it behind. Each directory therefore
carries an owner lock (``fcntl.flock`` on ``owner.lock``) held for the life
of the process that made it: the kernel drops the lock however that
process dies, and ``sweep_stale`` removes every marked directory whose lock
is free. The directory is made under a staging name and renamed into the
swept pattern only after the lock is held, so a concurrent sweep never sees
it unlocked.

The directories live under ``/tmp`` (ROOT), never under the process's own
``$TMPDIR``, and their names are short: a Go test that listens on a unix
socket under ``t.TempDir()`` binds at
``$TMPDIR/<test name, up to 64 bytes><suffix, up to 10 digits>/001/<name>``,
and the whole path must fit sun_path (107 bytes plus the NUL). ``/tmp/pfv-``
plus mkdtemp's 8 characters is 17 bytes, which leaves a 64-byte test name
11 bytes for ``/<name>``; an operator's ``$TMPDIR`` can be any length. The
container's TMPDIR (container.Box.tmp_path) takes the same root and prefix.
"""

import fcntl
import os
import shutil
import tempfile
from types import TracebackType
from typing import List, Optional, Type

ROOT = "/tmp"
DIR_PREFIX = "pfv-"
STAGE_PREFIX = ".pfv-"
OWNER_LOCK = "owner.lock"


class PrivateTmp:
    """One private TMPDIR, locked by this process until ``release``."""

    def __init__(self, path: str, lock_fd: int) -> None:
        self.path = path
        self._fd = lock_fd

    def release(self) -> None:
        """Release the owner lock, then remove the directory (idempotent)."""
        if self._fd >= 0:
            try:
                fcntl.flock(self._fd, fcntl.LOCK_UN)
            finally:
                os.close(self._fd)
                self._fd = -1
        shutil.rmtree(self.path, ignore_errors=True)

    def __enter__(self) -> "PrivateTmp":
        return self

    def __exit__(
        self,
        exc_type: Optional[Type[BaseException]],
        exc: Optional[BaseException],
        tb: Optional[TracebackType],
    ) -> None:
        self.release()


def _root(root: Optional[str]) -> str:
    return root if root is not None else ROOT


def new(root: Optional[str] = None) -> PrivateTmp:
    """Make one private TMPDIR under root (default ROOT)."""
    base = _root(root)
    stage = tempfile.mkdtemp(prefix=STAGE_PREFIX, dir=base)
    try:
        fd = os.open(os.path.join(stage, OWNER_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
    except OSError:
        shutil.rmtree(stage, ignore_errors=True)
        raise
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        os.close(fd)
        shutil.rmtree(stage, ignore_errors=True)
        raise
    path = os.path.join(base, DIR_PREFIX + os.path.basename(stage)[len(STAGE_PREFIX):])
    try:
        os.rename(stage, path)
    except OSError:
        fcntl.flock(fd, fcntl.LOCK_UN)
        os.close(fd)
        shutil.rmtree(stage, ignore_errors=True)
        raise
    return PrivateTmp(path, fd)


def sweep_stale(root: Optional[str] = None) -> List[str]:
    """Remove every marked private TMPDIR whose owner lock is free; answer
    the removed paths. A directory without the lock file is left alone: it
    carries no proof that its owner is gone."""
    base = _root(root)
    removed: List[str] = []
    try:
        names = sorted(os.listdir(base))
    except OSError:
        return removed
    for name in names:
        path = os.path.join(base, name)
        if not name.startswith(DIR_PREFIX) or not os.path.isdir(path) or os.path.islink(path):
            continue
        try:
            fd = os.open(os.path.join(path, OWNER_LOCK), os.O_RDWR)
        except OSError:
            continue
        try:
            try:
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError:
                continue
            fcntl.flock(fd, fcntl.LOCK_UN)
            shutil.rmtree(path, ignore_errors=True)
            removed.append(path)
        finally:
            os.close(fd)
    return removed
