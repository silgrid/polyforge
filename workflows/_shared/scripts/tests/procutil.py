"""Test helpers: whether a pid is still a live (non-zombie) process."""

import os
import subprocess


def alive(pid: int) -> bool:
    """True when pid names a running process (a zombie counts as gone: a
    container's init may never reap an orphan)."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    if os.path.isdir("/proc/self"):
        # Linux: a pid whose stat went away between the kill and the read
        # is gone (ps may not be installed: a slim image has none)
        try:
            with open("/proc/%d/stat" % pid, encoding="utf-8", errors="replace") as f:
                return f.read().rsplit(")", 1)[1].split()[0] != "Z"
        except OSError:
            return False
    out = subprocess.run(["ps", "-o", "stat=", "-p", str(pid)], stdout=subprocess.PIPE, check=False).stdout
    state = out.decode().strip()
    return state != "" and not state.startswith("Z")
