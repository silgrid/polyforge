"""The step's progress file: the same progress lines a script says on its
stderr, appended to one file of the step's capsule, where a reader watching
the job (Pi Web's progress card) follows a command that has not ended.

The file is ``<PF_CAPSULE>/runtime-state/progress/<PF_STEP>.log``, one line
per say, append-only across the step's rounds and dispatches. Outside a
step — a hand run, a test — nothing is written; a write that fails is
dropped, never raised: the stderr line is the progress record, this file
only lets a reader follow a long command while it runs (the daemon keeps
no partial output of a running call).
"""

import os
import re
from typing import IO, Mapping, Optional

from . import stepenv

# A step id as a workflow names one: the file name stays inside the
# progress directory (no path escapes through a hostile PF_STEP).
_STEP_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$", re.ASCII)

# Where the progress files live inside the capsule.
PROGRESS_REL = os.path.join("runtime-state", "progress")


def path(environ: Optional[Mapping[str, str]] = None) -> str:
    """The step's progress file, or "" when there is none: no step, no
    capsule, or a step id that is not a plain name."""
    env = stepenv.read(environ)
    if not env.in_step or not env.capsule or not env.step or not _STEP_ID.match(env.step):
        return ""
    return os.path.join(env.capsule, PROGRESS_REL, env.step + ".log")


def append(line: str, environ: Optional[Mapping[str, str]] = None) -> None:
    """One progress line appended to the step's progress file. Best effort:
    outside a step nothing is written, and a write that fails is dropped
    (the stderr line is the progress record; this file only lets a reader
    follow a command that has not ended)."""
    p = path(environ)
    if not p:
        return
    try:
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "a", encoding="utf-8") as f:
            f.write(line + "\n")
    except OSError:
        pass


def emit(line: str, stream: IO[str], environ: Optional[Mapping[str, str]] = None) -> None:
    """One progress line: printed to the stream, and appended to the step's
    progress file (``append``). A script's say routes through this, so the
    stderr line and the file's line are the same."""
    print(line, file=stream, flush=True)
    append(line, environ)
