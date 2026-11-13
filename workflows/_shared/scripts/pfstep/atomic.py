"""Atomic file writes: a reader sees the old file or the new one, never part.

The bytes go to a temp file beside the target (same directory, so the
rename stays on one filesystem), which is fsynced, renamed over the target
and followed by an fsync of the directory, so the rename itself survives a
crash. A target that is a symlink is replaced, never written through. The
parent directory is created when missing. The Go side's twin is
internal/common/atomicfile.
"""

import os
import tempfile
from typing import Callable, Optional


class DirSyncError(OSError):
    """The rename committed, but the directory fsync after it failed."""


def write(
    path: str,
    data: bytes,
    mode: int = 0o644,
    before_rename: Optional[Callable[[], None]] = None,
) -> None:
    """Land data at path atomically with the given permission bits.

    before_rename runs after the temp file is complete and fsynced, just
    before the rename (a test's seam for an interrupted write). Any failure
    before the rename removes the temp file and leaves the target as it was.
    """
    directory = os.path.dirname(os.path.abspath(path))
    os.makedirs(directory, mode=0o755, exist_ok=True)
    fd, tmp = tempfile.mkstemp(
        prefix="." + os.path.basename(path) + ".", suffix=".tmp", dir=directory
    )
    try:
        try:
            os.fchmod(fd, mode)
            view = memoryview(data)
            while view:
                n = os.write(fd, view)
                view = view[n:]
            os.fsync(fd)
        finally:
            os.close(fd)
        if before_rename is not None:
            before_rename()
        os.replace(tmp, path)
    except BaseException:
        try:
            os.unlink(tmp)
        except OSError:
            pass
        raise
    try:
        dfd = os.open(directory, os.O_RDONLY)
    except OSError as err:
        raise DirSyncError(err.errno, "open dir: %s" % err.strerror, directory) from err
    try:
        os.fsync(dfd)
    except OSError as err:
        raise DirSyncError(err.errno, "fsync dir: %s" % err.strerror, directory) from err
    finally:
        os.close(dfd)
