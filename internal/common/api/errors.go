package api

import (
	"encoding/json"
	"strconv"
)

// Error is the typed wire error shared by aihub and pf (reference.md §5).
// HTTP status is derived from the numeric suffix of the code.
type Error struct {
	Code   string          `json:"code"`
	Detail string          `json:"detail,omitempty"`
	Extra  json.RawMessage `json:"extra,omitempty"`
}

func (e *Error) Error() string {
	if e.Code == "" {
		return e.Detail
	}
	return e.Code + ": " + e.Detail
}

// HTTPStatus maps an error code (e.g. "CORE-409") to its HTTP status by
// parsing the numeric suffix. Unknown shapes map to 500.
func (e *Error) HTTPStatus() int {
	if len(e.Code) < 4 || e.Code[len(e.Code)-4] != '-' {
		return 500
	}
	n, err := strconv.Atoi(e.Code[len(e.Code)-3:])
	if err != nil || n < 400 || n > 599 {
		return 500
	}
	return n
}

// Error code registry. v1 surface: CORE/MEM/AUTH/GIT/DAEMON/CAP.
// SYNC-* codes are deleted (git replaces the revision chain).
const (
	// CORE — work item / project / user business errors (reference.md §5)
	ErrCoreBadInput  = "CORE-400"
	ErrCoreForbidden = "CORE-403"
	ErrCoreNotFound  = "CORE-404"
	ErrCoreConflict  = "CORE-409"
	ErrCoreGone      = "CORE-410"
	// workbench#11 cross-machine relay/messaging codes.
	ErrCoreTooLarge    = "CORE-413" // text/artifact over the bound
	ErrCoreSecret      = "CORE-422" // secret-shaped message, nothing stored
	ErrCoreTooMany     = "CORE-429" // anti-flood cap
	ErrCoreUnsupported = "CORE-501" // holder lacks the capability
	ErrCoreUpstream    = "CORE-503" // holder did not answer within the deadline
	ErrCoreStaleHolder = "CORE-504" // holder heartbeat stale
	ErrCoreInternal    = "CORE-500"
	// ErrCoreStorage: the SQLite backup (VACUUM INTO) failed.
	ErrCoreStorage  = "CORE-507"
	ErrCoreLockHeld = "CORE-508"

	// MEM — memory errors (06 §5)
	ErrMemBadInput    = "MEM-400"
	ErrMemForbidden   = "MEM-403"
	ErrMemNotFound    = "MEM-404"
	ErrMemConflict    = "MEM-409"
	ErrMemTooLarge    = "MEM-413"
	ErrMemUpstream    = "MEM-502"
	ErrMemUnavailable = "MEM-503"

	// AUTH — authentication/authorization at the HTTP boundary
	ErrAuthUnauthorized = "AUTH-401"
	ErrAuthForbidden    = "AUTH-403"

	// GIT — v1 git proxy surface (pre-receive rejects, auth, protocol)
	ErrGitBadRequest   = "GIT-400" // smart-HTTP method/target mismatch at the proxy
	ErrGitUnauthorized = "GIT-401" // attempt-scoped credential rejected (stale-session fencing)
	ErrGitForbidden    = "GIT-403"
	ErrGitNotFound     = "GIT-404"
	ErrGitGitlink      = "GIT-412" // gitlink (mode 160000) in pushed tree
	ErrGitInternal     = "GIT-500"
	// DAEMON — pf daemon UDS surface (reference.md §6.1)
	ErrDaemonBadInput  = "DAEMON-400"
	ErrDaemonForbidden = "DAEMON-403"
	ErrDaemonNotFound  = "DAEMON-404"
	// ErrDaemonUnmappedProject is the #113 claim-time placement refusal:
	// the work item's project is not in the machine's [workspace] mapping,
	// so no capsule home exists for it. Raised BEFORE any capsule is
	// created or server-side claim state changes (DAEMON-412: a
	// precondition of placement is missing — remediation is config, not
	// retry).
	ErrDaemonUnmappedProject = "DAEMON-412"
	// ErrDaemonWorkspaceYaml is the polyforge#79 pre-claim refusal: the
	// workspace's .polyforge.yaml failed to parse or violated the strict
	// schema. Raised BEFORE the aihub claim (like DAEMON-412): the work
	// item stays queued so another machine may take it, no capsule
	// exists; remediation is fixing the local file, not retrying.
	ErrDaemonWorkspaceYaml = "DAEMON-414"
	ErrDaemonConflict      = "DAEMON-409"
	ErrDaemonGone          = "DAEMON-410"
	ErrDaemonSpawnFail     = "DAEMON-507"
	ErrDaemonInternal      = "DAEMON-500"
	// ErrDaemonHubUnavailable is the transient pre-claim refusal: the
	// work-item probe could not reach the hub (transport loss or a 5xx).
	// Nothing was claimed; the caller retries.
	ErrDaemonHubUnavailable = "DAEMON-503"
	// ErrDaemonCapsuleClone is the re-claim clone refusal: the capsule
	// clone failed for a work item that has published progress (or whose
	// published state cannot be determined), so a local init — which
	// would restart the workflow from step 1 — is refused.
	ErrDaemonCapsuleClone = "DAEMON-516"
	// ErrDaemonSealedHeadMismatch is the sealed-head continue refusal
	// (v2-converge §7): a blocked WI whose aihub capsule head is not the
	// head the sealing daemon recorded, continued on a different daemon or
	// on the sealing one with no quarantined scene holding that head — the
	// clone would be pre-blocked state.
	ErrDaemonSealedHeadMismatch = "DAEMON-518"
)
