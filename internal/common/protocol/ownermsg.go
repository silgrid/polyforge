package protocol

// ownermsg.go: the owner-message relay's answer (workflow contract v2
// §5.2, PR 6d) — what `POST /jobs/{wi}/answer` without a v1 question
// answers and `pf answer <wi> --text` prints. The driver fills it
// (driver/relay.go); the runtime's answer face and the daemon's route
// carry it as is.

// OwnerMessageAck is the relay's answer to one owner message.
type OwnerMessageAck struct {
	Status string `json:"status"`
	// At is the recorded line's stamp: the message's identity in the
	// capsule record, the events and the delivery marker `[pf-msg:<at>]`.
	At string `json:"at"`
	// Kind is `reply` (an ask of the current dispatch was open; AskAt
	// names it) or `message` (a live interjection while no ask is open).
	Kind   string `json:"kind"`
	AskAt  string `json:"ask_at,omitempty"`
	Step   string `json:"step"`
	SpawnN int64  `json:"spawn_n"`
	// Delivered: the marker was observed in the dispatch's stream before
	// the caller's wait ended. False: the delivery goes on without the
	// caller — a running turn takes the message at its next boundary, or
	// the step's next dispatch carries it (Disposition says which).
	Delivered bool `json:"delivered"`
	// Disposition is the harness's word at acceptance (`started`,
	// `queued`), or `resume` when no process was alive to take the message
	// and the session is resumed to carry it.
	Disposition string `json:"disposition,omitempty"`
	// Note names why a live process did not take the message (it is
	// recorded; the next dispatch carries it); absent otherwise.
	Note string `json:"note,omitempty"`
	// Park and Wake describe a parked work item (workflow contract 4):
	// the park (`blocked` or `paused`) and how a message
	// wakes it (`step_blocked`, `step`, `write_failed`, `paused`). With
	// Status `parked` nothing was recorded — the caller opens the park's
	// door and asks again naming the park; with Status `accepted` and
	// Disposition `wake` the message is recorded and the park woken.
	Park string `json:"park,omitempty"`
	Wake string `json:"wake,omitempty"`
}
