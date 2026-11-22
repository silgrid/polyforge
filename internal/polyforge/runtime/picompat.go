package runtime

// picompat.go: the pi sub-agent compatibility gate (polyforge#227). The
// daemon-owned worker may run only when the switch is on AND the installed
// pi can actually load the daemon-owned extensions and register the
// subagent tool (owner rulings 2026-11-22 Q1/Q2/Q5, D3/D4/D5).

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

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
	text := fmt.Sprintf("pi compatibility rejected [%s]: %s", e.Kind, e.Detail)
	var where []string
	if e.Target != "" {
		where = append(where, "target="+e.Target)
	}
	if e.Version != "" {
		where = append(where, "version="+e.Version)
	}
	if e.Stage != "" {
		where = append(where, "stage="+e.Stage)
	}
	if len(where) > 0 {
		text += " (" + strings.Join(where, ", ") + ")"
	}
	return text
}

// piRequiredFlags is the flag set the daemon-owned step process needs
// (polyforge#193/P6). --no-approve joined it when the dedicated step agent
// directory replaced the operator's approvals path.
var piRequiredFlags = []string{
	"--no-skills",
	"--no-context-files",
	"--no-prompt-templates",
	"--no-extensions",
	"--no-approve",
}

// piVersion answers the installed target's own version report: one bounded
// short command, the first non-empty line. An empty or failing report is a
// version-class refusal (no numeric comparison: D5 sets no version floor).
func piVersion(ctx context.Context, bin string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", err
	}
	version := firstNonEmptyLine(string(out))
	if version == "" {
		return "", errors.New("the version command printed no version")
	}
	return version, nil
}

// piFlagsCheck refuses a target whose --help does not advertise every
// required flag.
func piFlagsCheck(ctx context.Context, bin, version string) error {
	out, err := exec.CommandContext(ctx, bin, "--help").Output()
	if err != nil {
		return piFlagRefusal(bin, version, "--help", err)
	}
	text := string(out)
	for _, flag := range piRequiredFlags {
		if !strings.Contains(text, flag) {
			return piFlagRefusal(bin, version, flag, nil)
		}
	}
	return nil
}

func piFlagRefusal(bin, version, flag string, err error) *PiDispatchError {
	return &PiDispatchError{
		Kind:    PiReasonFlag,
		Detail:  "required flag is unavailable: " + flag,
		Target:  bin,
		Version: version,
		Stage:   "flag",
		Err:     err,
	}
}

func piVersionRefusal(bin string, err error) *PiDispatchError {
	return &PiDispatchError{
		Kind:   PiReasonVersion,
		Detail: "cannot determine executable version",
		Target: bin,
		Stage:  "version",
		Err:    err,
	}
}

func piTimeoutRefusal(bin, version, stage string) *PiDispatchError {
	return &PiDispatchError{
		Kind:    PiReasonTimeout,
		Detail:  "compatibility probe exceeded 10s total budget",
		Target:  bin,
		Version: version,
		Stage:   stage,
	}
}

// validatePiCompatibility answers one compatibility verdict: the target's
// version, its required flags, and (from the capability probe on) the real
// load-and-call proof. Bounded by the caller's context.
func validatePiCompatibility(ctx context.Context, bin string) error {
	version, err := piVersion(ctx, bin)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return piTimeoutRefusal(bin, "", "version")
		}
		return piVersionRefusal(bin, err)
	}
	if err := piFlagsCheck(ctx, bin, version); err != nil {
		return err
	}
	return nil
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
