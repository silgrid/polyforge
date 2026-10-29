package server

// machineface.go: signature enforcement for the new cross-machine
// endpoints. Every signed request carries the existing
// Bearer authorization PLUS X-PF-Machine-ID, X-PF-Timestamp and
// X-PF-Signature over METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID.
// The Bearer user must equal the machine's operator_id. The ±300 s window
// and the replay cache (512 live entries per bucket / 600 s) live in
// internal/common/machinesign; aihub keys one bucket per machine id per
// signed-route lane (replayLane below, A2).
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

// replayLane is the signed-route class whose anti-replay bucket a
// machine's signature is recorded in ( A2). One shared
// bucket per machine let traffic another member induces — the answers to
// its relayed queries and the inbox wakes they cause — fill the quota the
// machine's own heartbeat needs, so a third party could push a holder
// stale. Each lane has its own machinesign.ReplayQuota: the heartbeat
// lane holds only the machine's own beats, which no other party can add
// to. The duplicate check loses nothing: a signature covers its method
// and path, every path is served by exactly one route, and every route
// names exactly one lane, so a signature is only ever checked in one
// bucket.
type replayLane string

const (
	laneHeartbeat replayLane = "heartbeat" // POST /machines/heartbeat
	laneInbox     replayLane = "inbox"     // GET /machines/inbox
	laneAnswer    replayLane = "answer"    // POST /queries/{qid}/answer
	laneQuery     replayLane = "query"     // POST /work-items/{id}/progress-query, /artifact-query
)

// replayBucket is the cache key of one machine's lane. Lanes carry no
// newline, so the last one splits a key unambiguously.
func replayBucket(machineID string, lane replayLane) string {
	return machineID + "\n" + string(lane)
}

// signedMachine authenticates the Bearer, verifies the machine signature,
// requires the caller to speak for the machine's operator, records the
// signature in the route's replay lane, and exposes the verified machine
// id to the handler. Reachability: a step agent cannot reach it — step
// processes hold no aihub key at all, and the daemon's aihub face
// forwards no machine-face route.
func (s *Server) signedMachine(lane replayLane, next machineHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idn, err := s.authenticate(r)
		if err == nil {
			var m machineAuth
			m, err = s.verifyMachine(r, idn, lane)
			if err == nil {
				err = next(w, r, idn, m)
			}
		}
		if err != nil {
			writeErr(w, err)
		}
	}
}

func (s *Server) verifyMachine(r *http.Request, idn *identity, lane replayLane) (machineAuth, error) {
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
	// The cache's verdict, mapped: a verbatim replay stays AUTH-401; a
	// machine over its lane's quota is CORE-429 (only that
	// machine's lane is refused, and its own entries expire to recover).
	if err := replayVerdict(s.replay.Check(time.Now(), replayBucket(machineID, lane), sig), machineID, lane, "machine signature replayed"); err != nil {
		return machineAuth{}, err
	}
	return machineAuth{MachineID: machineID, OperatorID: operatorID}, nil
}

// replayVerdict maps the shared anti-replay cache's verdict
// to the machine face's two refusals — one mapping for every signed face,
// verifyMachine and the heartbeat's Commit alike. A verbatim replay stays
// AUTH-401 with the caller's own detail; a machine over its lane's quota
// is CORE-429 naming the lane, and the quota and TTL in that detail text
// come from machinesign's constants so the numbers cannot drift from the
// cache's real bounds.
func replayVerdict(v machinesign.ReplayVerdict, machineID string, lane replayLane, replayDetail string) error {
	switch v {
	case machinesign.ReplayDuplicate:
		return core.Fail(api.ErrAuthUnauthorized, "", "%s", replayDetail)
	case machinesign.ReplayOverQuota:
		ttl := int(machinesign.ReplayTTL / time.Second)
		return core.Fail(api.ErrCoreTooMany, "",
			"machine %s filled its %s anti-replay quota (%d live signatures per machine id per lane, %d s TTL); its own oldest entries expire within %d s",
			machineID, lane, machinesign.ReplayQuota, ttl, ttl)
	}
	return nil
}

// heartbeatProof is the heartbeat face's cryptographic verdict (
// R2/R3/R7/R8). A beat with none of the X-PF-* headers at all is the bounded
// transition for an already-registered pre-signing daemon and says so in
// the proof (core's version gate accepts it only while both
// the request version and the stored runtime_version predate v2.20.8 — a
// signed v2.20.8+ beat ratchets the row closed); a partially signed beat is
// refused. A signed beat verifies against
// the key the body presents (a first registration, a first bind, a claim),
// else the row's bound key, through the same ±300 s window and the shared
// replay cache as the rest of the machine face — one scheme, no second one
// — in the machine's heartbeat lane ( A2: no other party can
// add an entry to it, so relayed traffic can never refuse a beat).
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
	// replayed byte-for-byte. The commit maps the cache's verdict: a
	// verbatim repeat answers AUTH-401, a machine over its heartbeat
	// lane's quota answers CORE-429.
	return core.HeartbeatProof{CallerID: idn.UserID, Signed: true, KeyB64: expected,
		Commit: func() error {
			return replayVerdict(s.replay.Check(time.Now(), replayBucket(machineID, laneHeartbeat), sig),
				machineID, laneHeartbeat, "machine heartbeat signature replayed")
		}}, nil
}
