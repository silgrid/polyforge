package api

import "encoding/json"

// machines_face.go: the cross-machine wire contract.
// Machines register an Ed25519 identity through the existing heartbeat,
// opt into the machine face with a capability card, and exchange signed
// progress/artifact queries and user-to-user messages through aihub.

// MachineFaceVersion is the version a daemon advertises in its heartbeat
// and aihub answers in the heartbeat result. A daemon whose heartbeat
// response lacks it disables the cross-machine face (old hub, no shim).
const MachineFaceVersion = 1

// Machine roles. Every user has at most one interactive machine; every
// other registered machine is a runner (it still answers queries).
const (
	MachineRoleInteractive = "interactive"
	MachineRoleRunner      = "runner"
)

// Launcher kinds reported by a daemon.
const (
	LauncherClaude         = "claude"
	LauncherPiWeb          = "pi-web"
	LauncherNonInteractive = "non-interactive"
)

// Machine face methods a capability card can advertise.
const (
	MethodProgressQuery  = "progress.query"
	MethodArtifactRead   = "artifact.read"
	MethodMessageDeliver = "message.deliver"
)

// CapabilityCard is the heartbeat's capability block.
type CapabilityCard struct {
	Methods []string `json:"methods"`
	Version int      `json:"version"`
}

// FullCapability is the card a current daemon sends: it answers progress
// and artifact queries; interactive daemons additionally receive live
// messages (the server checks the role separately).
func FullCapability() CapabilityCard {
	return CapabilityCard{
		Methods: []string{MethodProgressQuery, MethodArtifactRead, MethodMessageDeliver},
		Version: MachineFaceVersion,
	}
}

// Has reports whether the card advertises method.
func (c *CapabilityCard) Has(method string) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Methods {
		if m == method {
			return true
		}
	}
	return false
}

// ProgressQuery is the signed caller -> aihub query body. WorkItem is the
// id or slug; ArtifactPath is set only for an artifact read.
type ProgressQuery struct {
	WorkItem     string `json:"work_item"`
	ArtifactPath string `json:"artifact_path,omitempty"`
}

// ProgressQueryAnswer is aihub's relay of the holder's answer. Snapshot is
// the holder's unmodified protocol.WatchSnapshot JSON (progress) or the
// artifact content (artifact). Local tells the caller that it is itself
// the holder (the caller then renders its own local snapshot).
type ProgressQueryAnswer struct {
	HolderMachine string          `json:"holder_machine"`
	Local         bool            `json:"local,omitempty"`
	Snapshot      json.RawMessage `json:"snapshot,omitempty"`
	Artifact      *ArtifactAnswer `json:"artifact,omitempty"`
}

// ArtifactAnswer is the holder's answer to an artifact query.
type ArtifactAnswer struct {
	WorkItem string `json:"work_item"`
	Slug     string `json:"slug,omitempty"`
	Status   string `json:"status,omitempty"`
	Pointer  bool   `json:"pointer,omitempty"`
	Path     string `json:"path,omitempty"`
	Content  []byte `json:"content,omitempty"`
	TooLarge bool   `json:"too_large,omitempty"`
}

// MachineQuery is one pending query delivered to a holder's inbox.
type MachineQuery struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // progress|artifact
	WorkItem     string `json:"work_item"`
	ArtifactPath string `json:"artifact_path,omitempty"`
}

// MachineInbox is the long-poll answer: the pending queries and unread
// messages for the caller's operator.
type MachineInbox struct {
	Queries  []MachineQuery `json:"queries"`
	Messages []Message      `json:"messages"`
}

// QueryAnswerBody is the holder's signed answer to one query.
type QueryAnswerBody struct {
	HolderMachine string          `json:"holder_machine"`
	Local         bool            `json:"local,omitempty"`
	Snapshot      json.RawMessage `json:"snapshot,omitempty"`
	Artifact      *ArtifactAnswer `json:"artifact,omitempty"`
	Error         *Error          `json:"error,omitempty"`
}

// RekeyRequest is the POST /api/v1/machines/{id}/rekey body.
type RekeyRequest struct {
	PublicKey string `json:"public_key"`
}

// HolderInfo is the core's holder resolution for a work item.
type HolderInfo struct {
	MachineID    string          `json:"machine_id"`
	Status       string          `json:"status"`
	Fresh        bool            `json:"fresh"`
	AgeSeconds   int64           `json:"age_seconds,omitempty"`
	Capability   *CapabilityCard `json:"capability,omitempty"`
	Deregistered bool            `json:"deregistered,omitempty"`
}

// HasCapability reports whether the holder advertised method.
func (h *HolderInfo) HasCapability(method string) bool {
	return h != nil && h.Capability.Has(method)
}
