# version — which build is this

Read-only contract: this fragment prints the pf binary's build version
and stops — it dispatches no verb, performs no wi-lifecycle action and
edits no file.

Run `pf version` and relay the output: one line, the build-injected
version string — the same `internal/version` value the global
`pf --version` flag prints. The machine spelling is `pf --json version`
(the global `--json` goes before the verb — the usage line in
`pf help version`): exactly one machine line, `{"version":"<v>"}` —
script against that shape, not the prose. The verb registers no local
flags, so there is no `pf version --json` spelling (that hits the
flag-parse error, exit 2). The
verb needs no config and touches no face (aihub, daemon): it answers
even where `pf doctor` would fail on an unloadable config, which makes
it the right query from scripts and workflow steps alike.
