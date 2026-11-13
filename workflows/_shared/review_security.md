## Security scope

This review does no security probing: no offensive probing, no injection reproduction,
no privilege-escalation attempt. It judges functional correctness, the acceptance
criteria, the project rules and regressions. A security risk you notice anyway is one
`non-blocking` finding of one line. Two exceptions judge it like any other finding: the
change under review is itself a security feature, or the risk breaks a rule already
written — a repository's `AGENTS.md` (else `CLAUDE.md`), or a principle or checklist item
of this definition.
