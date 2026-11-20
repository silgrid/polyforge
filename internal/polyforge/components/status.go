package components

// status.go (polyforge#190, 84-inplace-upgrade): the volume's upgrade
// status file and cancel marker — the one record Pi Web reads across
// its own restart (the orchestration writes it, the page replays the
// final result from it after the reconnect) and the one way a person
// or the page asks a waiting upgrade to stand down. Both live in the
// components root, beside current/previous: the volume is what
// survives every restart in the workstation.

import (
	"errors"
)

// The root's upgrade-transaction file names.
const (
	// UpgradeStatusFilename is the volume's upgrade status record:
	// what operation ran, which phase it reached, from and to which
	// version, and — on a refusal — the reason. Crash-atomically
	// written by the orchestration at every phase change.
	UpgradeStatusFilename = "upgrade-status.json"

	// UpgradeCancelFilename is the cancel marker: present means a
	// waiting upgrade (or rollback) must stand down — restart the
	// daemon (clearing the drain) and change nothing else. The marker
	// is the whole message; it carries no body.
	UpgradeCancelFilename = "upgrade-cancel"
)

// The operations and states an UpgradeStatus names.
const (
	OperationUpgrade  = "upgrade"
	OperationRollback = "rollback"

	UpgradeStateRunning = "running"
	UpgradeStateWaiting = "waiting-drain"
	UpgradeStateDone    = "done"
	UpgradeStateFailed  = "failed"
	UpgradeStateCancelled = "cancelled"
)

// upgradeStatusSchemaVersion is the status record's shape this package
// reads and writes.
const upgradeStatusSchemaVersion = 1

// UpgradeStatus is one line of the upgrade's history: the current (or
// last) transaction as the volume records it.
type UpgradeStatus struct {
	SchemaVersion int    `json:"schemaVersion"`
	Operation     string `json:"operation"` // OperationUpgrade or OperationRollback
	State         string `json:"state"`     // one of the UpgradeState* names
	Phase         string `json:"phase"`     // the fine-grained step the run reached
	From          string `json:"from"`      // the version current pointed at when it started
	To            string `json:"to"`        // the version it aims current at ("" until known)
	Error         string `json:"error,omitempty"`
	At            string `json:"at"` // RFC3339, the write's own moment
}

// ReadUpgradeStatus answers the root's recorded status, or nil with no
// error when none is recorded.
func ReadUpgradeStatus(root string) (*UpgradeStatus, error) {
	_ = root
	return nil, errors.New("ReadUpgradeStatus: not implemented")
}

// WriteUpgradeStatus crash-atomically records s, filling the schema
// version and the write's own timestamp.
func WriteUpgradeStatus(root string, s *UpgradeStatus) error {
	_ = root
	_ = s
	return errors.New("WriteUpgradeStatus: not implemented")
}

// RequestUpgradeCancel lays the cancel marker (idempotent).
func RequestUpgradeCancel(root string) error {
	_ = root
	return errors.New("RequestUpgradeCancel: not implemented")
}

// UpgradeCancelRequested answers whether the marker is laid.
func UpgradeCancelRequested(root string) bool {
	_ = root
	return false
}

// ClearUpgradeCancel removes the marker (the run consumed it).
func ClearUpgradeCancel(root string) error {
	_ = root
	return errors.New("ClearUpgradeCancel: not implemented")
}
