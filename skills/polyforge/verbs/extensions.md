# extensions — how do I customize / extend / configure polyforge

Read-only contract: this fragment points the user at where polyforge's
extension points are documented and stops — it dispatches no verb,
performs no wi-lifecycle action and edits no file.

Run `pf help extensions` and relay the output: one line per extension
point (per-repo `AGENTS.md`, `docs/agents/` area notes, the
`.polyforge.yaml` sections, the `cli.toml` keys, custom workflow
bundles), each naming the docs/extending.md subsection that owns it.
docs/extending.md (release checkout:
`~/.local/share/polyforge/release/current/docs/extending.md`) is the
extension-point index — each config-shape subsection routes to the
one authority for its shape (docs/reference/config.md; custom bundles:
docs/bundles.md) — read the named subsection before proposing or
writing any file; never restate a shape from memory. The topic needs no
config and touches no face (aihub, daemon), so it answers anywhere,
including inside a workflow step.
