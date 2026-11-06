"""The step environment: what the daemon starts every step process with.

The names and their meaning are the workflow contract's ("The step
interface", "Environment", and "Scripts"; docs/reference/workflow-contract.md).
A hand run outside a step leaves them unset; ``read`` answers each one's
default then. A script's commands run under the step's own ``PATH`` (the
daemon's ``[daemon] command_path`` when the machine sets one).
"""

import os
import sys
from dataclasses import dataclass
from typing import Mapping, Optional

ENV_WI_ID = "PF_WI_ID"
ENV_DAEMON_ID = "PF_DAEMON_ID"
ENV_DAEMON_SOCKET = "PF_DAEMON_SOCKET"
ENV_ROUND = "PF_ROUND"
ENV_STEP = "PF_STEP"
ENV_SPAWN = "PF_SPAWN"
ENV_TOOL_WINDOW = "PF_TOOL_WINDOW_MS"
ENV_CAPSULE = "PF_CAPSULE"
ENV_LANE_DIR = "PF_LANE_DIR"
ENV_SHARED_DIR = "PF_SHARED_DIR"
ENV_PYTHON = "PF_PYTHON"
# A claude step's shell-call maximum (the harness's own variable).
ENV_BASH_MAX_TIMEOUT = "BASH_MAX_TIMEOUT_MS"

# MARKERS are the step markers: every process a script starts keeps them,
# so the daemon's sweep at the step's end finds it (proc.child_env).
MARKERS = (ENV_WI_ID, ENV_DAEMON_ID)

# The shared tree this package was loaded from (<...>/_shared): PF_SHARED_DIR's
# default for a hand run.
_OWN_SHARED_DIR = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


@dataclass(frozen=True)
class StepEnv:
    """The step environment as one value ("" = unset, after the defaults)."""

    wi_id: str
    daemon_id: str
    daemon_socket: str
    round: str
    step: str
    spawn: str
    tool_window_ms: str
    capsule: str
    lane_dir: str
    shared_dir: str
    python: str

    @property
    def in_step(self) -> bool:
        """Whether this process runs inside a step (both markers set)."""
        return self.wi_id != "" and self.daemon_id != ""


def read(environ: Optional[Mapping[str, str]] = None) -> StepEnv:
    """The step environment of environ (default the process's).

    Defaults for a hand run: the shared dir the ``_shared`` tree this package
    was loaded from, the interpreter the one running this code; every other
    name stays "".
    """
    env = os.environ if environ is None else environ
    return StepEnv(
        wi_id=env.get(ENV_WI_ID, ""),
        daemon_id=env.get(ENV_DAEMON_ID, ""),
        daemon_socket=env.get(ENV_DAEMON_SOCKET, ""),
        round=env.get(ENV_ROUND, ""),
        step=env.get(ENV_STEP, ""),
        spawn=env.get(ENV_SPAWN, ""),
        tool_window_ms=env.get(ENV_TOOL_WINDOW, ""),
        capsule=env.get(ENV_CAPSULE, ""),
        lane_dir=env.get(ENV_LANE_DIR, ""),
        shared_dir=env.get(ENV_SHARED_DIR, "") or _OWN_SHARED_DIR,
        python=env.get(ENV_PYTHON, "") or sys.executable,
    )

