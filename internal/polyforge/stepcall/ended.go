package stepcall

// ended.go: how a dispatch ended, read from the ledger alone — the facts
// the daemon's message wake (a parked step's kind) and
// the driver's resume shape need without reading execution.json's
// contract 3 records: whether the dispatch handed off, with which status,
// and whether the hub writes a `done` recorded are all made.

// EndedWith answers the status the dispatch (step, spawn) handed off
// with, "" when the ledger records no accepted hand-off of it.
func EndedWith(lines []Line, step string, spawn int64) string {
	if h := HandoffOf(lines, step, spawn); h != nil {
		return h.Handoff.Status
	}
	return ""
}

// LastHandoff answers the newest accepted hand-off of step, nil when the
// step never handed off.
func LastHandoff(lines []Line, step string) *Line {
	all := StepHandoffs(lines, step)
	if len(all) == 0 {
		return nil
	}
	l := all[len(all)-1]
	return &l
}

// WritesUnfinished reports whether the step round (step, round) recorded a
// hub write the ledger does not record made (an exec line `done`): the
// writes of a `done` hand-off that the executor has not finished — a
// failed write, or a crash before or between them. Writes belong to the
// step round, not to one dispatch (owner ruling R8, PendingOps); a round
// with no pending writes, or whose writes were dropped, has none
// unfinished.
func WritesUnfinished(lines []Line, step string, round int64) bool {
	execs := Executions(lines)
	for _, op := range PendingOps(lines, step, round) {
		if e, ok := execs[op.ID]; !ok || e.Outcome != ExecDone {
			return true
		}
	}
	return false
}
