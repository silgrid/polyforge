# extensions — how do I customize / extend / configure polyforge

Read-only contract: this fragment points the user at where polyforge's
extension points are documented and stops — it dispatches no verb,
performs no wi-lifecycle action and edits no file.

Run `pf help extensions` and relay the output: one line per extension
point (per-repo `AGENTS.md`, `docs/agents/` area notes, the
`.polyforge.yaml` sections, the `cli.toml` keys, custom workflow
bundles), each naming the README "Extending" subsection that owns it.
The README (release checkout:
`~/.local/share/polyforge/release/current/README.md`, section
"Extending") is the one authority for every shape — read the named
subsection before proposing or writing any file; never restate a shape
from memory. The topic needs no config and touches no face (aihub,
daemon), so it answers anywhere, including inside a workflow step.
