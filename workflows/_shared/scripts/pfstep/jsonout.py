"""JSON output in the order the value was built.

A script's documents (its result file, the records it writes beside it)
keep their keys in the order the code built them, as Go's encoder keeps a
struct's field order: a dict's insertion order, never ``sort_keys``.
Non-ASCII text stays UTF-8 and NaN or an infinity is refused, as Go
refuses them.
"""

import json
from typing import Any


def dumps(value: Any) -> str:
    """The indented document (two spaces) with a trailing newline."""
    return json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + "\n"


def line(value: Any) -> str:
    """The compact one-line form (no trailing newline), as --json prints it."""
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False)
