# help — the pf quick-reference

Read-only contract: this fragment prints reference output and stops — it
dispatches no verb, performs no wi-lifecycle action and edits no file.

Run `pf help` (the full verb registry, grouped data-plane / execution-plane
/ management / diagnostics / process) or `pf help <verb>` (one verb's usage
line) and relay the output. The verb → fragment index is the intent table
in `SKILL.md`; each fragment owns its verb's real command shape, success
rendering and error branches — read it before running the verb for the
first time in a session. `pf <verb> -help` prints only that verb's own
registered flags (empty for verbs whose flags live on sub-actions) and
exits 2.

Delivery has no verb (`verbs/ship.md`). The newcomer mainline is the
workstation image: `docs/getting-started.md` (release checkout:
`~/.local/share/polyforge/release/current/docs/getting-started.md`) — Docker,
the image, compose, the first boot, a first submitted work item. Host
install / upgrade is `tools/install.sh` (`verbs/init.md`), the path for a
machine joining an existing team. The daily-use guide is
`docs/user-guide.md` (release checkout:
`~/.local/share/polyforge/release/current/docs/user-guide.md`) — the flow, the core concepts, memory and the
recovery moves.
