"""A script's deadline: what the step environment bounds, less a margin.

The rule the Go step commands had (DefaultDeadlineFor): the
tightest bound the step environment declares — BASH_MAX_TIMEOUT_MS (a
claude step's shell-call maximum) and PF_TOOL_WINDOW_MS (the daemon's
supervision tool window, every harness) — less two minutes, or less half
the bound when that is smaller, so the script ends (its processes killed,
its result written) before the harness cuts the call or the daemon judges
it hung. Without either value: 25 minutes.
"""

import os
import re
from typing import Mapping, Optional

from . import duration
from .stepenv import ENV_BASH_MAX_TIMEOUT, ENV_TOOL_WINDOW

DEFAULT_DEADLINE = 25 * duration.MINUTE
MARGIN = 2 * duration.MINUTE

# Go's strconv.Atoi after strings.TrimSpace: an optional sign and ASCII
# digits, nothing else (no "_" separators, no other digits).
_ATOI = re.compile(r"[+-]?[0-9]+", re.ASCII)


def _millis(raw: str) -> Optional[int]:
    s = raw.strip()
    if not _ATOI.fullmatch(s):
        return None
    ms = int(s)
    if ms <= 0 or ms > ((1 << 63) - 1) // duration.MILLISECOND:
        return None
    return ms


def default_deadline_for(environ: Optional[Mapping[str, str]] = None) -> int:
    """The deadline (nanoseconds) the step environment allows a script."""
    env = os.environ if environ is None else environ
    bound = 0
    for name in (ENV_BASH_MAX_TIMEOUT, ENV_TOOL_WINDOW):
        if name not in env:
            continue
        ms = _millis(env[name])
        if ms is None:
            continue
        d = ms * duration.MILLISECOND
        if bound == 0 or d < bound:
            bound = d
    if bound <= 0:
        return DEFAULT_DEADLINE
    d = bound - min(MARGIN, bound // 2)
    if d > 0:
        return d
    return duration.MILLISECOND
