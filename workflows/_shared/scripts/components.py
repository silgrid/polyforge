"""components.py — the epic-upgrade rule's component division
(polyforge#89): what a repository's `.polyforge/verify-scope.conf`
declares (the `component=` names and prefix ranges, the
`epic_components` / `epic_units` / `epic_files` thresholds) and what
the generic definition divides it into, read from each clone's
origin/HEAD (else HEAD) commit tree — never the work tree, never a
fetch — for the pre-filing analysis to attach to its prompt.

Usage:

  components.py --repo DIR [--repo DIR]… [--rev REV] [--json]

--repo names a repository's clone (repeatable; at least one). --rev
reads that revision's tree instead of origin/HEAD (the analysis reads
a branch the remote has not seen). --json answers schema 1 on stdout;
without it a line report, one block per repository:

  repo demo 1a2b3c4… (origin/HEAD)
    component core  [src, lib]  declared
    component web  [web]  nested conf
    component tools/py  [tools/py]  manifest
    component docs  [docs]  top-level, documentation
    component .  [README.md, go.mod]  root files
    thresholds epic_components 3 (default), epic_units 9 (declared), epic_files 30 (default)

The generic definition (no declaration, or a path no declared range
covers): a nested engine root (a subdirectory with its own
.polyforge/verify-scope.conf — only the root conf's declarations are
read; a nested conf only marks its subdirectory), then a manifest
subdirectory (go.mod, package.json, pyproject.toml, requirements.txt,
Cargo.toml), each nest at its outermost, then the first-level
directory, the root files forming the "." component; a path with a
testdata, node_modules or vendor segment is excluded from the division
entirely. A component whose every path is documentation is marked so
(the epic rules never count it). The thresholds default to 3 / 8 / 30;
the engine reads none of these keys (it accepts their lines without the
unknown-key diagnostic), the gate id hashes none of them.

Exit 0: the report answers, whatever a repository's error entry says
(each named, never faked). Exit 2: no --repo named."""

import os
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import argparse  # noqa: E402
import json  # noqa: E402
from typing import Any, Dict, List, Optional  # noqa: E402

from pfstep import components  # noqa: E402


def render(report: components.RepoReport) -> str:
    """One repository's line report."""
    if report.error != "":
        return "repo %s (%s)\n  cannot read %s: %s — no components; thresholds default\n" % (
            report.name, report.rev, report.rev, report.error)
    lines = ["repo %s %s (%s)" % (report.name, report.commit, report.rev)]
    for d in report.division:
        doc = ", documentation" if d.documentation_only else ""
        lines.append("  component %s  [%s]  %s%s" % (d.name, ", ".join(d.paths), d.source, doc))
    t = report.thresholds
    lines.append("  thresholds epic_components %d (%s), epic_units %d (%s),"
                 " epic_files %d (%s)" % (t.epic_components, t.source["epic_components"],
                                          t.epic_units, t.source["epic_units"],
                                          t.epic_files, t.source["epic_files"]))
    return "\n".join(lines) + "\n"


def entry(report: components.RepoReport) -> Dict[str, Any]:
    """One repository's --json entry (schema 1)."""
    e = {
        "repo": report.name,
        "commit": report.commit,
        "rev": report.rev,
        "components": [{"name": d.name, "paths": list(d.paths), "source": d.source,
                        "documentation_only": d.documentation_only}
                       for d in report.division],
        "thresholds": {
            "epic_components": report.thresholds.epic_components,
            "epic_units": report.thresholds.epic_units,
            "epic_files": report.thresholds.epic_files,
            "source": dict(report.thresholds.source),
        },
    }
    if report.error != "":
        e["error"] = report.error
    return e


def main(argv: Optional[List[str]] = None) -> int:
    ap = argparse.ArgumentParser(prog="components.py", description=__doc__.splitlines()[0])
    ap.add_argument("--repo", action="append", default=[], metavar="DIR",
                    help="a repository clone (repeatable)")
    ap.add_argument("--rev", default="", metavar="REV",
                    help="read REV's tree instead of origin/HEAD")
    ap.add_argument("--json", action="store_true", help="answer schema 1 on stdout")
    args = ap.parse_args(argv)
    if not args.repo:
        ap.error("at least one --repo is required")
    reports = [components.read_repo(d, args.rev) for d in args.repo]
    for report in reports:
        for d in report.diagnostics:
            sys.stderr.write("components: repo %s: %s\n" % (report.name, d))
    if args.json:
        sys.stdout.write(json.dumps({"schema": 1, "repos": [entry(r) for r in reports]}) + "\n")
    else:
        sys.stdout.write("".join(render(r) for r in reports))
    return 0


if __name__ == "__main__":
    sys.exit(main())
