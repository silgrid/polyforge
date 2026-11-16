"""pfstep.gateid — the lane's own gate id.

The identity of a repository's test gate at a commit — the sha256 over
the ``polyforge-gate-id 1`` header, the sha256 of
``.polyforge/Dockerfile.verify`` and the conf's gate lines, both files
read from that commit's tree — as a library, for a repository whose tree
carries no ``tools/gate-id``: the gate falls back to this one
(gate.py, polyforge#29). ``tools/gate-id`` (awk over ``git show``) stays
the release's; the two must stay byte-identical, and the consistency
test (``tests/test_gateid.py``) runs both over the same commits to pin
it. The algorithm is the documented one (``tools/gate-id``'s header,
``docs/reference/release.md`` "The gate id"): a gate line is a conf line
with its leading whitespace removed that is neither blank nor a ``#``
comment and whose key — the text before its first ``=`` — is one of
``stack``, ``fast_gate``, ``full_command``, ``check``,
``check_requires_env``, ``check_requires_cmd`` or ``check_timeout``,
kept byte for byte in file order, each ending in a newline.
"""

from typing import List, Mapping, Optional

CONF = ".polyforge/verify-scope.conf"
DOCKERFILE = ".polyforge/Dockerfile.verify"
HEADER = b"polyforge-gate-id 1"
# The conf keys whose lines decide a --full plan on an empty diff: the
# same set as tools/gate-id's (the id is the gate's identity, not the
# engine's).
GATE_KEYS = (
    b"stack",
    b"fast_gate",
    b"full_command",
    b"check",
    b"check_requires_env",
    b"check_requires_cmd",
    b"check_timeout",
)


class GateIdError(Exception):
    """Why the gate id of a commit could not be computed."""


def _gate_lines(conf: bytes) -> List[bytes]:
    """conf's gate lines, byte for byte — stub."""
    return []


def hashed_text(conf: bytes, dockerfile: bytes) -> bytes:
    """The exact bytes the id hashes (tools/gate-id --show's output) — stub."""
    return b""


def show(directory: str, commit: str, environ: Optional[Mapping[str, str]] = None) -> bytes:
    """The exact bytes the id of commit hashes, read from that commit's
    tree — stub."""
    return b""


def gate_id(directory: str, commit: str, environ: Optional[Mapping[str, str]] = None) -> str:
    """The gate id of commit: the sha256 of show's bytes — stub."""
    return ""

# The "environ" parameter is the hardened git environment gate.py runs
# the tree's tools/gate-id under; the implementation reads the blobs
# through it (pfstep.git). The stub imports nothing yet: its signatures
# are the contract, and the first red is the test's assertion.
