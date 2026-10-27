package server

// machineface.go: signature enforcement for the new cross-machine
// endpoints (workbench#11). Every signed request carries the existing
// Bearer authorization PLUS X-PF-Machine-ID, X-PF-Timestamp and
// X-PF-Signature over METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID.
// The Bearer user must equal the machine's operator_id. The ±300 s window
// and the 4096/600 s replay cache live in internal/common/machinesign.
// Existing endpoints keep their auth byte-for-byte.

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
)

// machineAuth is the verified machine identity of one signed request.
type machineAuth struct {
	MachineID  string
	OperatorID string
}

type machineHandler func(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error

// signedMachine authenticates the Bearer, verifies the machine signature,
// requires the caller to speak for the machine's operator, and exposes the
// verified machine id to the handler. Reachability: a step agent cannot
// reach it — step processes hold no aihub key at all, and the daemon's
// aihub face forwards no machine-face route.
func (s *Server) signedMachine(next machineHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idn, err := s.authenticate(r)
		if err == nil {
			var m machineAuth
			m, err = s.verifyMachine(r, idn)
			if err == nil {
				err = next(w, r, idn, m)
			}
		}
		if err != nil {
			writeErr(w, err)
		}
	}
}

func (s *Server) verifyMachine(r *http.Request, idn *identity) (machineAuth, error) {
	machineID := strings.TrimSpace(r.Header.Get(machinesign.HeaderMachineID))
	tsRaw := strings.TrimSpace(r.Header.Get(machinesign.HeaderTimestamp))
	sig := strings.TrimSpace(r.Header.Get(machinesign.HeaderSignature))
	if machineID == "" || tsRaw == "" || sig == "" {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "signed machine request required (X-PF-Machine-ID, X-PF-Timestamp, X-PF-Signature)")
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "invalid X-PF-Timestamp")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "unreadable request body")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var pubB64, operatorID string
	err = s.DB.QueryRow(`SELECT COALESCE(public_key,''), COALESCE(operator_id,'') FROM machines WHERE id=?1`,
		machineID).Scan(&pubB64, &operatorID)
	if errors.Is(err, sql.ErrNoRows) {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "machine %s is not registered", machineID)
	}
	if err != nil {
		return machineAuth{}, err
	}
	if pubB64 == "" {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "machine %s has no bound public key", machineID)
	}
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "machine %s public key is unreadable", machineID)
	}
	if !machinesign.Verify(ed25519.PublicKey(pub), time.Now(), r.Method, r.URL.Path, body, ts, machineID, sig, nil) {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "machine signature is missing, invalid, or out of the ±%d s window", machinesign.Window)
	}
	// Authorization precedes the replay check: a caller that does not
	// speak for the machine gets AUTH-403 even when replaying another
	// machine's signature.
	if operatorID == "" || idn.UserID != operatorID {
		return machineAuth{}, core.Fail(api.ErrAuthForbidden, "", "the Bearer user does not speak for machine %s", machineID)
	}
	if !s.replay.Check(time.Now(), machineID, sig) {
		return machineAuth{}, core.Fail(api.ErrAuthUnauthorized, "", "machine signature replayed")
	}
	return machineAuth{MachineID: machineID, OperatorID: operatorID}, nil
}

// heartbeatProof is the heartbeat face's cryptographic verdict (workbench#57
// R2/R3/R7/R8). A beat with none of the X-PF-* headers at all is the bounded
// transition for an already-registered v2.20.6 daemon and says so in the
// proof; a partially signed beat is refused. A signed beat verifies against
// the key the body presents (a first registration, a first bind, a claim),
// else the row's bound key, through the same ±300 s window and the shared
// replay cache as the rest of the machine face — one scheme, no second one.
//
// The replay cache is in memory: an aihub restart forgets it, so at most one
// ±300 s window reopens per restart (recorded in the decision record).
func (s *Server) heartbeatProof(r *http.Request, body []byte, req core.MachineHeartbeat, idn *identity) (core.HeartbeatProof, error) {
	machineID := strings.TrimSpace(r.Header.Get(machinesign.HeaderMachineID))
	tsRaw := strings.TrimSpace(r.Header.Get(machinesign.HeaderTimestamp))
	sig := strings.TrimSpace(r.Header.Get(machinesign.HeaderSignature))
	if machineID == "" && tsRaw == "" && sig == "" {
		return core.HeartbeatProof{CallerID: idn.UserID}, nil
	}
	if machineID == "" || tsRaw == "" || sig == "" {
		return core.HeartbeatProof{}, core.Fail(api.ErrAuthUnauthorized, "",
			"a signed heartbeat carries X-PF-Machine-ID, X-PF-Timestamp and X-PF-Signature together")
	}
	if machineID != req.MachineID {
		return core.HeartbeatProof{}, core.Fail(api.ErrAuthUnauthorized, "",
			"X-PF-Machine-ID %q does not match the body's machine_id %q", machineID, req.MachineID)
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return core.HeartbeatProof{}, core.Fail(api.ErrAuthUnauthorized, "", "invalid X-PF-Timestamp")
	}
	expected := req.PublicKey
	if expected == "" {
		var bound string
		err := s.DB.QueryRow(`SELECT COALESCE(public_key,'') FROM machines WHERE id=?1`, machineID).Scan(&bound)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return core.HeartbeatProof{}, err
		}
		expected = bound
	}
	if expected == "" {
		return core.HeartbeatProof{}, core.Fail(api.ErrAuthUnauthorized, "",
			"machine %q presents no public key and has no bound one; a registration and a first bind present the key that signs the beat", machineID)
	}
	pub, err := base64.StdEncoding.DecodeString(expected)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return core.HeartbeatProof{}, core.Fail(api.ErrAuthUnauthorized, "", "machine %q public key is unreadable", machineID)
	}
	now := time.Now()
	if !machinesign.Verify(ed25519.PublicKey(pub), now, r.Method, r.URL.Path, body, ts, machineID, sig, nil) {
		return core.HeartbeatProof{}, core.Fail(api.ErrAuthUnauthorized, "",
			"machine heartbeat signature is missing, invalid, or out of the ±%d s window", machinesign.Window)
	}
	// The signature is recorded at the accepted write, not here: a refused
	// beat must not burn it (the same body and second may be retried once the
	// caller's eligibility is fixed), while an accepted one can never be
	// replayed byte-for-byte.
	return core.HeartbeatProof{CallerID: idn.UserID, Signed: true, KeyB64: expected,
		Commit: func() bool { return s.replay.Check(time.Now(), machineID, sig) }}, nil
}
