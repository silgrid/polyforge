"""The secret shapes a text is scanned for and masked by.

The same list, in the same order, as the Go side (internal/common/secretpat):
``scan`` answers the first shape a text carries, ``mask`` replaces every
one (a shape by ``[masked <name>]``; a URL's userinfo, a credential
assignment and the ``--password`` flag's value by ``***``). Every pattern is
``re.ASCII``: ``\\b`` and the classes mean what Go's RE2 means, and the
whitespace class is Go's ``\\s`` (``[\\t\\n\\f\\r ]``, no vertical tab).
"""

import re
from typing import List, Optional, Pattern, Tuple

_WS = r"\t\n\f\r "  # Go RE2's \s

# PATTERNS in scan order: (name, pattern).
PATTERNS: List[Tuple[str, Pattern[str]]] = [
    ("polyforge_key", re.compile(r"pf_k1_[A-Za-z0-9]{16,}", re.ASCII)),
    ("polyforge_short_key", re.compile(r"\bkey_[A-Za-z0-9]{12}", re.ASCII)),
    ("pem_private_key", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----", re.ASCII)),
    ("openai_key", re.compile(r"sk-[A-Za-z0-9]{20,}", re.ASCII)),
    ("anthropic_key", re.compile(r"sk-ant-[A-Za-z0-9_-]{20,}", re.ASCII)),
    ("aws_access_key", re.compile(r"AKIA[0-9A-Z]{16}", re.ASCII)),
    ("github_token", re.compile(r"gh[pousr]_[A-Za-z0-9]{30,}", re.ASCII)),
    ("slack_token", re.compile(r"xox[baprs]-[A-Za-z0-9-]{10,}", re.ASCII)),
    ("google_api_key", re.compile(r"AIza[0-9A-Za-z_-]{35}", re.ASCII)),
    ("hex64", re.compile(r"\b[0-9a-fA-F]{64}\b", re.ASCII)),
]

# _MASKS: the credential shapes beside PATTERNS (over-masking costs only
# fidelity; under-masking leaks, so the values are greedy).
_MASKS: List[Tuple[Pattern[str], str]] = [
    (re.compile(r"\b([a-z][a-z0-9+.-]*://)[^/" + _WS + r"@]+@", re.ASCII | re.IGNORECASE), r"\g<1>***@"),
    (
        re.compile(r"\b([a-z0-9_]*(?:api_key|token|secret|password|passwd)=)[^" + _WS + r"]+", re.ASCII | re.IGNORECASE),
        r"\g<1>***",
    ),
    (re.compile(r"(--password[" + _WS + r"]+)[^" + _WS + r"]+", re.ASCII | re.IGNORECASE), r"\g<1>***"),
]


def scan(text: str) -> Optional[str]:
    """The first secret shape text carries (its name), or None when clean."""
    for name, pat in PATTERNS:
        if pat.search(text):
            return name
    return None


def mask(text: str) -> str:
    """text with every credential-shaped substring replaced."""
    for pat, repl in _MASKS:
        text = pat.sub(repl, text)
    for name, pat in PATTERNS:
        text = pat.sub("[masked " + name + "]", text)
    return text
