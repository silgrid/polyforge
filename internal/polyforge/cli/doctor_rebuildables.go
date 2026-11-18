package cli

// doctor_rebuildables.go: the "rebuildable artifacts" row of `pf
// doctor`, run once per Cell — the rebuildable build products a
// `--prune-rebuildables` may remove from the terminal work items'
// capsules without waiting for the 14-day retention, and the explicit
// flag that removes them. The row reuses the retention row's scan and
// the work-item state reads; the declaration, the probe and the
// anchored removal live in internal/polyforge/capsulegc (polyforge#127).

// docChRebuildables is the row name (sibling convention: one const
// beside the layer it implements).
const docChRebuildables = "rebuildable artifacts"

// rebuildablesCheck is the row. Stub: the row lands registered but
// answering skip, so the unit's assertions run red on the status they
// mean, never on a missing row (polyforge#127, u4).
func rebuildablesCheck(r *doctorRun, dataDir string) (string, string) {
	return "skip", "not implemented"
}
