**Answer `scope_adequate` and record your `scope` when the work item opted
into the scoped tier** — its content carries the marker line
`verification: scoped` and the lane is scoped-capable (you run BEFORE
`verification`, so the obligation keys on the work item, never on
verification's output). Declare the scope you judged the diff against in
the `scope` object (`mode: scoped` + non-empty `declared_paths`), record a
boolean `scope_adequate` for whether that scope covers the change, and
expect the following `verification` to use exactly this scope — the
automatic merge needs the two declarations to agree. `false` does **not**
fail this review and does not rewind to `{{build_step}}` — it only blocks
the automatic merge, so the delivery asks the human. The driver rejects a
completed review of a scoped work item that omits `scope_adequate`; a
full-tier work item requires nothing and an absent field there is never a
finding.
