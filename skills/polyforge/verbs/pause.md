# pause — pause a running work item on this machine

Execution-plane verb: routed to the local daemon (the pause route: a close
of the current run with reason `paused`). The daemon seals the
capsule read-only and closes the session as `paused`; the work item
becomes `paused` and the only way back is the `resume` verb. The session
is KEPT (it is not cleared like a wrap/fail/block): `pf resume` on a paused
work item clears `clean_pause` on the hub and tells the holder daemon, which
re-attaches to the kept session and continues in place (a holder that was
down attaches on its next start).

## Triggers

- EN: pause it / stop it for now / hold on / park this one / freeze it
- 中文: 暂停 / 先停一下 / 挂起 / 停一停 / 先放一放

## Command

```
pf pause <id>
```

No flags. The daemon performs the close itself: the final commit and the
push of the capsule's revision history, then its local run (the agent
process tree) stops. The hub keeps the work item's session, marked
`clean_pause`, so the holder daemon can re-attach it on `pf resume`.

## Success

`pause requested for <slug>` (`--json`: the daemon's raw answer instead). The local run stops but the session is kept; the sealed capsule stays on
disk as the read-only scene a later resume continues from.

## Failure branches

| Code | When | What to tell the user |
|---|---|---|
| `DAEMON-404` | No local job for this work item on this machine | It is not running here — pause it on the machine that runs it. |
| `DAEMON-409` `the close is parked …` / `the release of this work item is parked …` | The work item's previous driver Run did not stop within its bounded teardown and may still be writing the capsule, so the close (or pause) — or the daemon's release of a session that is no longer its own — waits: nothing is pushed, saved, sealed or quarantined next to it | Nothing to redo: the close finishes by itself once that Run exits (`pf watch <id> --once` shows `close_parked`, then the close's own events); a daemon restart also finishes it. Do not repeat the verb meanwhile — it gets the same answer. |
| `DAEMON-409` | The job's close is already in progress: it is retried with backoff up to the chain's bound (24 hours from the first retry), or the record is parked after a typed 4xx refusal or `retry_exhausted` — an exhausted chain's answer says `retry chain is exhausted` | Wait only while it is still retrying. If the answer says the retry chain is exhausted, or `pf watch <id> --once` shows `close_refused`, `session_verify_outage` or `retry_exhausted`, the close will not settle by itself: fix the credential or hub state and restart the daemon (`pf ops shutdown`, then `pf daemon ensure`), which reconciles the record afresh. `pf cancel <id>` frees only the hub side. |
| `CORE-*` passthrough | The server-side pause was refused | Read the typed Core verdict — typically the session is no longer this daemon's; act per its detail. |
| `DAEMON-500` | The close failed on the daemon side | The job is left mid-seal, recorded as such; the next daemon start finishes the close. Check the daemon log before retrying. |
| `dial unix …` | Daemon not running | Nothing is executing here — run `pf daemon ensure`, then retry once (or check `--socket`). |

**Pause while the work item is still being claimed** (`pf ops status` shows
`claiming`: the capsule and task repos are still cloning, no agent ran
yet): there is no scene to keep a paused record for, so the daemon
cancels the clone, quarantines the partial capsule and ends the session
**blocked** (event `claim_paused_ended_blocked`). Continue it with
`pf resume <id>` — the blocked door (owner/maintainer/admin) re-claims
it from the start; `pf cancel <id>` abandons it.

A paused work item is still visible through `pf file show`; its live
execution view is gone (the local job is sealed).
