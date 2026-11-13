# claude task notifications a reload must settle a run from

**Synthetic, not recorded.** Each `<scenario>/transcript.jsonl` is a main transcript
built by hand, entry by entry, in the shapes claude 2.1.322 writes (the `relay/`
recording in `../claude-2.1.322/` and the absorbed notification in
`../claude-2.1.319/absorbed-notification/`): a user prompt, the `Agent` call with
`run_in_background: true` and its async launch receipt (`toolUseResult.agentId`), the
`SendMessage` call and its answer (`revivedAgentId` for a revival, "Message queued …"
otherwise), and the `<task-notification>` text — `<task-id>`, `<tool-use-id>` when claude
names a call, `<output-file>`, `<status>completed</status>`, `<summary>`, `<note>`,
`<result>`, `<usage>` — either as claude's own turn (a user entry with `turnOrigin:
task_notification`) or absorbed into a running turn (`queue-operation` enqueue, a
`queued_command` attachment with `commandMode: task-notification`, `queue-operation`
remove). Summaries and notes carry claude's own wording: `Agent "<description>" finished`
or `Agent "<description>" is waiting on its own background work`, the latter with the
note that the result may be interim and the same task-id notifies again. Entries form one
`parentUuid` chain; no sidecars (the runs have no entries). Ids, paths, prompts and results
are invented: session `48100000-0000-4000-8000-000000000000`, agent ids
`a481000000000000{1..5}`, call ids `toolu_481_<role>_<scenario>`, cwd
`/tmp/claude-synthetic/work`.

Why synthetic: the shapes were measured on a real session transcript (a reload showed
finished subagents as running); that transcript cannot be published, and these scenarios
reproduce each shape it held with nothing else in them.

`lib/claude-task-notes-481.test.mjs` replays them through `claudeTranscriptToMessages()`.

| scenario | what it holds | the run after the last entry |
|---|---|---|
| `revived/` | background `Agent` → its notification naming the `Agent` call → a `SendMessage` revival → the revival's notification naming **no call**, only the `<task-id>` | completed, with the revival's result |
| `first-run/` | background `Agent` → its notification naming **no call** | completed |
| `waiting/` | background `Agent` → a notification naming the `Agent` call whose summary says the agent **is waiting on its own background work** → a later one naming no call | running after the first, completed after the second |
| `absorbed/` | background `Agent` → its notification **absorbed** into a running turn (a foreground `Bash` call) → a `SendMessage` revival → the revival's notification absorbed the same way | completed after each notification, running between the revival and its notification |
| `revived-waiting/` | background `Agent` → finished → a revival whose only notification says **waiting** → a second revival → its notification naming that `SendMessage` call | running after the first revival's notification, completed at the end, started at the second revival |
