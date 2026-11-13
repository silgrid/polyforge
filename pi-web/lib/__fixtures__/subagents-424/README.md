# subagents-424 — a pi step's recorded subagent

`pi/session/session-tok/subagents/` is a capsule's subagent directory as the
daemon's driver writes it for a pi step (`internal/polyforge/driver/
subagentrec.go`): the child conversation of one `subagent` call
(`call_bcb397f329d148e18d52f97f-0.jsonl`, one pi `{"type":"message"}` record
per child message) and its meta (`.meta.json`: `toolUseId`, `description`,
`agentType`, `index`, `finished`, `exitCode`, `isError`).

Generated, not hand-written: the recorder's output for the replay
`internal/polyforge/driver/testdata/subagents-424/pi-stdout.jsonl` (a recorded
subagent call, redacted — its README says how). The Go test
`TestPiSubagentRecorderMatchesThePiWebFixture` holds the two byte-equal, so a
change to the recorder's layout or record shape fails there until this
fixture moves with it. `lib/polyforge-subagents-424.test.mjs` and
`e2e/polyforge-424.mjs` read it.
