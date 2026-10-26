#!/usr/bin/env python3
"""Mechanical cross-reference checker for the Polyforge design doc set
(docs/design/polyforge-vnext/). Run by `make check` (xrefs target).

Live docs are the *.md files directly in the doc directory; archived docs
are docs/design/polyforge-vnext/archive/*.md. Archived docs are history:
their own content is NOT checked, but their anchors stay valid targets.

Anchor convention: every numbered heading (levels 2-4) of a live doc
carries an explicit marker ``<!-- §X.Y -->`` at the end of the heading
line, where ``X.Y`` is the heading's own leading section number. A marker
may also name a lettered appendix (``<!-- §E -->``). A cross-reference is
written `` `name.md#§X.Y` `` (a live doc) or `` `archive/name.md#§X.Y` ``
(an archived doc).

For every live doc this script verifies:

  1. every numbered heading carries a marker equal to its own number;
  2. every `` `[archive/]name.md#§X` `` reference names an existing file
     (a bare name must be live; an archived doc must be spelled
     ``archive/``) that has the marker ``§X``;
  3. no legacy reference form is used: shorthand `` `05` §2.9 ``, split
     `` `05-name.md` §2.9 ``, or plain ``05 §2.9``.

Exit code is 0 iff there are zero violations. Run from anywhere; the doc
directory is resolved relative to this script's location.
"""
import re
import sys
from pathlib import Path

DOC_DIR = Path(__file__).resolve().parent.parent
ARCHIVE_DIR = DOC_DIR / "archive"
LIVE_FILES = sorted(DOC_DIR.glob("*.md"))
ARCHIVE_FILES = sorted(ARCHIVE_DIR.glob("*.md"))

SECT = r"(?:\d+(?:\.\d+)*[a-z]?|[A-Z](?:\.\d+)*)"
HEADING_RE = re.compile(r"^(#{2,4}) (\d+(?:\.\d+)*[a-z]?)\.?\s+(.*)$")
ANCHOR_RE = re.compile(r"<!--\s*§(" + SECT + r")\s*-->")
REF_RE = re.compile(r"`((?:archive/)?[A-Za-z0-9][A-Za-z0-9._-]*\.md)#§(" + SECT + r")`")
LEGACY_SHORTHAND_RE = re.compile(r"`(\d{2})`\s*§(\d+(?:\.\d+)*[a-z]?)")
LEGACY_SPLIT_RE = re.compile(r"`(\d{2}-[a-z0-9-]+\.md)`\s*§(\d+(?:\.\d+)*[a-z]?)")
LEGACY_PLAIN_RE = re.compile(r"(?<![\w.`#§/])(0[0-7])\s*§(\d+(?:\.\d+)*[a-z]?)")


def load(path, check_headings):
    """Return ({section: line}, [heading violations])."""
    table, bad = {}, []
    for i, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        for m in ANCHOR_RE.finditer(line):
            table.setdefault(m.group(1), i)
        if not check_headings:
            continue
        h = HEADING_RE.match(line)
        if not h:
            continue
        num = h.group(2)
        marks = ANCHOR_RE.findall(line)
        if not marks:
            bad.append((path.name, f"heading §{num} (line {i})",
                        "numbered heading lacks an explicit `<!-- §X.Y -->` anchor marker"))
        elif marks[-1] != num:
            bad.append((path.name, f"heading §{num} (line {i})",
                        f"anchor marker says §{marks[-1]} but heading number is {num}"))
    return table, bad


def main():
    anchors, errors = {}, []
    for f in LIVE_FILES:
        anchors[f.name], bad = load(f, True)
        errors += bad
    for f in ARCHIVE_FILES:
        anchors["archive/" + f.name], _ = load(f, False)

    total = 0
    for f in LIVE_FILES:
        text = f.read_text(encoding="utf-8")
        for rx, what in ((LEGACY_SHORTHAND_RE, "shorthand"), (LEGACY_SPLIT_RE, "split"),
                         (LEGACY_PLAIN_RE, "plain")):
            for m in rx.finditer(text):
                errors.append((f.name, m.group(0), f"legacy {what} form; use `name.md#§X.Y`"))
        for m in REF_RE.finditer(text):
            total += 1
            target, sect = m.group(1), m.group(2)
            if target not in anchors:
                hint = ""
                if "archive/" + target in anchors:
                    hint = f" (it is archived: write `archive/{target}#§{sect}`)"
                errors.append((f.name, m.group(0), f"target file {target} does not exist{hint}"))
                continue
            if sect not in anchors[target]:
                errors.append((f.name, m.group(0), f"no explicit anchor §{sect} in {target}"))

    if errors:
        print(f"FAIL: {len(errors)} violation(s)\n")
        for src, ref, reason in errors[:80]:
            print(f"  {src}: {ref}  ->  {reason}")
        if len(errors) > 80:
            print(f"  ... and {len(errors) - 80} more")
        return 1
    print(f"OK: {total} cross-reference(s) in {len(LIVE_FILES)} live documents resolved "
          f"against {sum(len(a) for a in anchors.values())} anchor marker(s) "
          f"({len(ARCHIVE_FILES)} archived documents not checked), 0 violations.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
