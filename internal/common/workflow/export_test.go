package workflow

// export_test.go: the in-place reads of a loaded lane's files the external
// tests (package workflow_test) judge — over the shared tree the lane was
// loaded with (Lane.Shared), so a user lane's fallback on the built-in
// tree is observable.

// ReadDefinitionIn renders and decodes definition id of the loaded lane.
func ReadDefinitionIn(lane *Lane, id string) (*Definition, error) {
	var vars, params map[string]string
	if s := lane.Step(id); s != nil {
		vars, params = s.Vars, s.Params
	}
	return readDefinition(lane.Dir, lane.Shared, id, vars, params)
}

// The contract-version rule's judges (compat.go): the incompatible
// differences of two registry dumps, the pin's verdict and the entries of
// contract-pending.md.
var (
	IncompatibleChanges = incompatibleChanges
	JudgeRegistry       = judgeRegistry
	PendingEntries      = pendingEntries
)
