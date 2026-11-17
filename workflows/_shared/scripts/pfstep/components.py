"""pfstep.components (polyforge#89): the epic-upgrade rule's
configuration — the ``component=`` and ``epic_*`` conf keys — and a
repository's component division, the shared core of the entry script
``workflows/_shared/scripts/components.py``. This commit lands the
signatures only, with zero-value bodies, so the unit's test is red on
its assertions, never on an import error."""

from typing import Dict, List, NamedTuple


class Component(NamedTuple):
    """One component of a repository: its name, the path prefixes that
    define it, and where the division found it."""

    name: str
    paths: List[str]
    source: str


class Thresholds(NamedTuple):
    """The effective epic-upgrade thresholds and, per key, their source
    (``default`` or ``declared``)."""

    epic_components: int
    epic_units: int
    epic_files: int
    source: Dict[str, str]


class ConfReading(NamedTuple):
    """What the root conf declares: the declared components, the
    effective thresholds, and the diagnostics of the lines ignored."""

    components: List[Component]
    thresholds: Thresholds
    diagnostics: List[str]


def read_conf(text: str) -> ConfReading:
    """The root conf's component= and epic_* keys (polyforge#89, AC1)."""
    return ConfReading([], Thresholds(0, 0, 0, {}), [])
