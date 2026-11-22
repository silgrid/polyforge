package runtime

// picompat.go: the pi sub-agent compatibility gate (polyforge#227). The
// daemon-owned worker may run only when the switch is on AND the installed
// pi can actually load the daemon-owned extensions and register the
// subagent tool (owner rulings 2026-11-22 Q1/Q2/Q5, D3/D4/D5).

import "fmt"

// PiDispatchReason is the stable class of one refused pi dispatch.
type PiDispatchReason string

const (
	PiReasonVersion          PiDispatchReason = "pi_version"
	PiReasonFlag             PiDispatchReason = "pi_flag"
	PiReasonAPI              PiDispatchReason = "pi_api"
	PiReasonToolRegistration PiDispatchReason = "pi_tool_registration"
	PiReasonTimeout          PiDispatchReason = "pi_timeout"
	PiReasonResources        PiDispatchReason = "pi_resources"
	PiReasonInvalidParam     PiDispatchReason = "pi_subagents_invalid"
)

// PiDispatchError is one refused pi dispatch: a stable reason, a fixed
// human-readable detail and the bounded facts (target, version, stage) that
// located it. It never carries a provider response payload or a credential.
type PiDispatchError struct {
	Kind    PiDispatchReason
	Detail  string
	Target  string
	Version string
	Stage   string
	Err     error
}

func (e *PiDispatchError) Reason() string { return string(e.Kind) }

func (e *PiDispatchError) Unwrap() error { return e.Err }

func (e *PiDispatchError) Error() string {
	return fmt.Sprintf("pi compatibility rejected [%s]: %s", e.Kind, e.Detail)
}
