package core

// Admin face: users, api keys, machines and projects. Secrets are stored
// hash-only and returned in plaintext exactly once at issuance.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// AdminManager serves the management endpoints (M1–M5 shape, v1 subset).
type AdminManager struct {
	db *DB

	// flood is the in-memory anti-flood window (60 messages per sender per
	// rolling 60 s), one per aihub process. A restart resetting it is
	// accepted: this is flood control, not a guarantee.
	floodMu sync.Mutex
	flood   map[string][]time.Time
}

func NewAdminManager(db *DB) *AdminManager {
	return &AdminManager{db: db, flood: map[string][]time.Time{}}
}

// ---------------------------------------------------------------------------
// Users

// CreateUser inserts a human or machine identity. Email is required for
// humans, generated for machines.
func (m *AdminManager) CreateUser(displayName, email, role, userType string) (*api.User, error) {
	if strings.TrimSpace(displayName) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "display_name required")
	}
	if role == "" {
		role = "writer"
	}
	if role != "admin" && role != "writer" {
		return nil, Fail(api.ErrCoreBadInput, "", "role must be admin or writer")
	}
	if userType == "" {
		userType = "human"
	}
	if userType != "human" && userType != "machine" {
		return nil, Fail(api.ErrCoreBadInput, "", "user_type must be human or machine")
	}
	if userType == "human" && strings.TrimSpace(email) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "email required for human users")
	}
	id, err := NewID("usr_")
	if err != nil {
		return nil, err
	}
	var emailAny any
	if email != "" {
		emailAny = email
	}
	if _, err := m.db.Exec(`INSERT INTO users(id, display_name, email, kind, role, created_at)
		VALUES (?1,?2,?3,?4,?5,?6)`, id, displayName, emailAny, userType, role, Now()); err != nil {
		return nil, err
	}
	return m.GetUser(id)
}

func (m *AdminManager) GetUser(id string) (*api.User, error) {
	var u api.User
	var email sql.NullString
	err := m.db.QueryRow(`SELECT id, display_name, email, kind, role, created_at FROM users WHERE id=?1`, id).
		Scan(&u.ID, &u.DisplayName, &email, &u.UserType, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "user %s", id)
	}
	if err != nil {
		return nil, err
	}
	u.Email = email.String
	u.APITokens = []api.APIToken{}
	return &u, nil
}

// PatchUser updates display_name and/or the global role (M1). Empty
// fields mean "leave unchanged".
func (m *AdminManager) PatchUser(id, displayName, role string) (*api.User, error) {
	if strings.TrimSpace(displayName) == "" && role == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "nothing to update: display_name and/or role required")
	}
	if role != "" && role != "admin" && role != "writer" {
		return nil, Fail(api.ErrCoreBadInput, "", "role must be admin or writer")
	}
	res, err := m.db.Exec(`UPDATE users SET display_name=CASE WHEN ?1<>'' THEN ?1 ELSE display_name END,
		role=CASE WHEN ?2<>'' THEN ?2 ELSE role END WHERE id=?3`, displayName, role, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, Fail(api.ErrCoreNotFound, "", "user %s", id)
	}
	return m.GetUser(id)
}

// The admin lists answer one capped page (no cursor) beside the true
// row count, so a caller can tell a capped answer from a complete one.
const (
	listUsersCap    = 200
	listAPIKeysCap  = 500
	listProjectsCap = 200
)

// countRows answers the row count of one admin-listed table.
func (m *AdminManager) countRows(table string) (int, error) {
	var n int
	err := m.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
	return n, err
}

// ListUsers returns the newest listUsersCap users (with token metadata,
// never hashes) and the total user count.
func (m *AdminManager) ListUsers() ([]api.User, int, error) {
	total, err := m.countRows("users")
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.Query(`SELECT id FROM users ORDER BY created_at DESC LIMIT ?1`, listUsersCap)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	out := []api.User{}
	for _, id := range ids {
		u, err := m.GetUser(id)
		if err != nil {
			return nil, 0, err
		}
		rows, err := m.db.Query(`SELECT id, name, created_at FROM api_keys WHERE user_id=?1 AND revoked_at IS NULL`, id)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var t api.APIToken
			if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
				rows.Close()
				return nil, 0, err
			}
			u.APITokens = append(u.APITokens, t)
		}
		rows.Close()
		out = append(out, *u)
	}
	return out, total, nil
}

// ---------------------------------------------------------------------------
// API keys

// CreateAPIKey issues a key for a user; plaintext is returned ONCE.
type IssuedKey struct {
	Token api.APIToken `json:"token"`
	Plain string       `json:"plain"` // shown exactly once
}

func (m *AdminManager) CreateAPIKey(userID, name, projectScope string) (*IssuedKey, error) {
	var one int
	if err := m.db.QueryRow(`SELECT 1 FROM users WHERE id=?1`, userID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, Fail(api.ErrCoreNotFound, "", "user %s", userID)
		}
		return nil, err
	}
	if projectScope != "" {
		var scopeID string
		if err := m.db.QueryRow(`SELECT id FROM projects WHERE id=?1 OR name=?1`, projectScope).Scan(&scopeID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, Fail(api.ErrCoreNotFound, "", "project %q", projectScope)
			}
			return nil, err
		}
		projectScope = scopeID
	}
	id, err := NewID("key_")
	if err != nil {
		return nil, err
	}
	plain, err := NewSecret(24)
	if err != nil {
		return nil, err
	}
	var scopeAny any
	if projectScope != "" {
		scopeAny = projectScope
	}
	if _, err := m.db.Exec(`INSERT INTO api_keys(id, user_id, project_id, name, key_hash, created_at)
		VALUES (?1,?2,?3,?4,?5,?6)`, id, userID, scopeAny, name, Sha256Hex(plain), Now()); err != nil {
		return nil, err
	}
	return &IssuedKey{
		Token: api.APIToken{ID: id, Name: name, CreatedAt: Now()},
		Plain: plain,
	}, nil
}

// RevokeAPIKey records the revocation time in revoked_at. Both bind
// positions are numbered: SQLite gives a bare `?` the number after the
// highest one used so far, so mixing `?` and `?1` would bind the same
// argument twice.
func (m *AdminManager) RevokeAPIKey(keyID string) error {
	res, err := m.db.Exec(`UPDATE api_keys SET revoked_at=?1 WHERE id=?2 AND revoked_at IS NULL`,
		Now(), keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Either absent or already revoked: both are terminal for the key.
		var one int
		if err := m.db.QueryRow(`SELECT 1 FROM api_keys WHERE id=?1`, keyID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Fail(api.ErrCoreNotFound, "", "api key %s", keyID)
			}
			return err
		}
	}
	return nil
}

// APIKeyInfo is one admin-list row (M2 list): metadata only — never the
// hash, never material.
type APIKeyInfo struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	ProjectID *string `json:"project_id"`
	Name      string  `json:"name"`
	CreatedAt string  `json:"created_at"`
	RevokedAt *string `json:"revoked_at"`
}

// ListAPIKeys lists the newest listAPIKeysCap keys' metadata (M2),
// newest first, revoked rows last within a user, and the total key count.
// Only metadata is returned; plaintext exists exactly once at issuance
// and the hash never leaves the store.
func (m *AdminManager) ListAPIKeys() ([]APIKeyInfo, int, error) {
	total, err := m.countRows("api_keys")
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.Query(`SELECT id, user_id, project_id, name, created_at, revoked_at
		FROM api_keys ORDER BY created_at DESC, id LIMIT ?1`, listAPIKeysCap)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []APIKeyInfo{}
	for rows.Next() {
		var k APIKeyInfo
		var projectID, revoked sql.NullString
		if err := rows.Scan(&k.ID, &k.UserID, &projectID, &k.Name, &k.CreatedAt, &revoked); err != nil {
			return nil, 0, err
		}
		if projectID.Valid {
			p := projectID.String
			k.ProjectID = &p
		}
		if revoked.Valid {
			r := revoked.String
			k.RevokedAt = &r
		}
		out = append(out, k)
	}
	return out, total, rows.Err()
}

// ---------------------------------------------------------------------------
// Machines

// MachineHeartbeat is the machines/heartbeat body. First call registers
// (creates the machines row; no machine user and no api key — a daemon runs
// on its operator-issued key); repeats refresh the timestamp, the liveness
// projection and the machine identity. Authorization lives at the server
// face. The caller's authenticated user id is the machine's operator_id
// (never a body field, and makes it the machine's one
// identity). There is no client timestamp: last_heartbeat_at is
// server-stamped.
type MachineHeartbeat = api.MachineHeartbeat

// HeartbeatResult is the heartbeat response.
type HeartbeatResult = api.MachineHeartbeatResult

// machineStaleDefault mirrors the server's 5×60s heartbeat window; the
// core uses it for the multi_machine projection.
const machineStaleDefault = 5 * time.Minute

// HeartbeatProof is the server's cryptographic verdict on one heartbeat:
// who sent it, whether it proved possession of a key, and which key the
// signature verified against. The server owns the verdict (signature,
// freshness, replay); this package owns the write decision. Signed=false is
// the bounded transition for an already-registered pre-signing daemon —
// accepted only while both the request version and the stored runtime_version
// predate v2.20.8 ('s gate, see refreshLivenessOnly; the
// deletion condition stays at its branch in refreshMachine).
type HeartbeatProof struct {
	CallerID string // the authenticated caller's user id
	Signed   bool   // a machine signature verified
	KeyB64   string // the public key it verified against
	// Commit records the signature in the server's anti-replay cache at
	// the accepted write and answers nil when it was fresh. Core calls it
	// once, immediately before the write: a refused beat must not burn its
	// signature (an eligible caller may retry with the same body and second,
	// which the authored claim case does), while an accepted one can never
	// be replayed byte-for-byte. The commit maps the cache's verdict
	// itself: a verbatim repeat answers AUTH-401 and a
	// machine over its heartbeat lane's quota answers CORE-429 (the lane
	// holds only the machine's own beats,).
	Commit func() error
}

// commitProof enforces the replay rule at the write boundary.
func commitProof(proof HeartbeatProof) error {
	if proof.Commit != nil {
		return proof.Commit()
	}
	return nil
}

// HeartbeatCallerCheck answers the refusal a heartbeat gets when the caller
// does not operate the machine row. The server runs it before verifying a
// signature the caller may not even hold, so a machine_id collision is
// AUTH-403 whether the beat is signed, unsigned or a first-registration
// attempt (spec R9). A row with no operator is open to a claim, and an
// absent row is a registration.
func (m *AdminManager) HeartbeatCallerCheck(machineID, callerID string) error {
	var operator string
	err := m.db.QueryRow(`SELECT COALESCE(operator_id,'') FROM machines WHERE id=?1`, machineID).Scan(&operator)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if operator == "" || operator == callerID {
		return nil
	}
	return m.machineCollisionError(machineID)
}

// machineCollisionError is the one refusal for a caller who is not the
// row's operator: it says the machine is registered to another
// operator — WITHOUT naming who (the collision must not
// disclose the operator's id or display name) — and suggests a
// different --machine-id. The admin machine inventory (GET
// /api/v1/machines) remains the place that maps a machine to its
// operator. Nothing is written either way.
func (m *AdminManager) machineCollisionError(machineID string) error {
	return Fail(api.ErrAuthForbidden, "",
		"machine %q is registered to another operator; choose a different --machine-id (a heartbeat cannot move a machine to another operator)", machineID)
}

// HeartbeatAuthorized is the one heartbeat write path, and one beat is
// one transaction: the row is read, decided and written inside a single
// transaction on the store's one connection (core.Open pins
// SetMaxOpenConns(1)), so a concurrent beat waits at Begin and then sees
// the committed row — the loser answers exactly what it would have
// answered had the two requests run one after the other.
// proof is the server's cryptographic verdict; registrationGate is the
// registration gate's verdict, already evaluated by the server — the
// gate's membership query cannot run from inside this transaction (the
// single connection is held) — and is enforced here only on the two
// registration acts (a fresh machine_id, and the claim of a legacy row
// with no operator); a refresh ignores it (membership gates registration
// only, spec R5). The refusal order stays's: caller 403
// (HeartbeatCallerCheck, then the in-transaction collision check),
// signature 401, registration gate 403.
func (m *AdminManager) HeartbeatAuthorized(req MachineHeartbeat, proof HeartbeatProof, registrationGate error) (*HeartbeatResult, error) {
	if strings.TrimSpace(req.MachineID) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "machine_id required")
	}
	if req.Role != "" && req.Role != api.MachineRoleInteractive && req.Role != api.MachineRoleRunner {
		return nil, Fail(api.ErrCoreBadInput, "", "role must be interactive or runner")
	}
	live := nullableJSON(req.LiveWIIDs)
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var curRole, curSuperseded, curOperator, curPublicKey, curVersion string
	err = tx.QueryRow(`SELECT COALESCE(role,'runner'),
		COALESCE(superseded_at,''), COALESCE(operator_id,''),
		COALESCE(public_key,''), COALESCE(runtime_version,'')
		FROM machines WHERE id=?1`, req.MachineID).
		Scan(&curRole, &curSuperseded, &curOperator, &curPublicKey, &curVersion)
	switch {
	case err == nil:
		if curOperator != "" && curOperator != proof.CallerID {
			return nil, m.machineCollisionError(req.MachineID)
		}
		if curOperator == "" {
			// Claiming a NULL-operator legacy row is the registration act:
			// the same gate, and the beat must be signed by a key it
			// presents (the COALESCE hole is closed).
			if registrationGate != nil {
				return nil, registrationGate
			}
			if !proof.Signed || req.PublicKey == "" || proof.KeyB64 != req.PublicKey {
				return nil, Fail(api.ErrAuthUnauthorized, "", "claiming machine %q requires a signature by the public key it presents", req.MachineID)
			}
		}
		res, rerr := m.refreshMachine(tx, req, proof, curRole, curSuperseded, curOperator, curPublicKey, curVersion, live)
		if rerr != nil {
			return nil, rerr
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		res.MultiMachine = m.operatorHasPeer(res.OperatorID, req.MachineID, machineStaleDefault)
		return res, nil
	case errors.Is(err, sql.ErrNoRows):
		// Register: gate, then key possession, then one machines row. No
		// machine user is minted — the caller recorded as operator_id is the
		// machine's one identity.
		if registrationGate != nil {
			return nil, registrationGate
		}
		if proof.CallerID == "" {
			return nil, Fail(api.ErrCoreBadInput, "", "machine registration requires an authenticated caller")
		}
		if !proof.Signed || req.PublicKey == "" || proof.KeyB64 != req.PublicKey {
			return nil, Fail(api.ErrAuthUnauthorized, "", "machine registration requires a public key and a signature by it")
		}
		if err := commitProof(proof); err != nil {
			return nil, err
		}
		operator := proof.CallerID
		role := req.Role
		if role == "" {
			role = api.MachineRoleRunner
		}
		var refused string
		if role == api.MachineRoleInteractive {
			if req.Force {
				if err := m.supersedeOthersTx(tx, operator, req.MachineID); err != nil {
					return nil, err
				}
			} else {
				holder, herr := interactiveHolderTx(tx, operator, req.MachineID)
				if herr != nil {
					return nil, herr
				}
				if holder != "" {
					role = api.MachineRoleRunner
					refused = "interactive held by " + holder
				}
			}
		}
		if err := m.insertMachineRow(tx, req, operator, role, live); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		res := &HeartbeatResult{
			Registered:         true,
			MachineID:          req.MachineID,
			OperatorID:         operator,
			MachineFaceVersion: api.MachineFaceVersion,
			Role:               role,
			InteractiveRefused: refused,
		}
		res.MultiMachine = m.operatorHasPeer(operator, req.MachineID, machineStaleDefault)
		return res, nil
	default:
		return nil, err
	}
}

// insertMachineRow writes the one registration row. The read and the write
// share the heartbeat's single transaction on the store's one connection,
// so two registrations of one id serialize and the loser refreshes
// instead; the UNIQUE mapping is a backstop for anything that still slips
// through — never CORE-500.
func (m *AdminManager) insertMachineRow(tx *sql.Tx, req MachineHeartbeat, operator, role string, live any) error {
	if _, err := tx.Exec(`INSERT INTO machines(id, runtime_version, mode, live_wi_ids, last_heartbeat_at,
		public_key, launcher, role, operator_id, capability)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10)`,
		req.MachineID, req.RuntimeVersion, req.Mode, live, Now(),
		nullableText(req.PublicKey), nullableText(req.Launcher), role, operator, capabilityJSON(req.Capability)); err != nil {
		if isUniqueViolation(err) {
			return Fail(api.ErrCoreConflict, "", "machine %q was registered concurrently", req.MachineID)
		}
		return err
	}
	return nil
}

// refreshMachine updates an existing row the caller operates, inside the
// heartbeat's open transaction: every read and write below shares that one
// transaction on the store's single connection, so a concurrent beat
// serializes and its refresh sees this write (MultiMachine is filled by the
// caller after the commit).
//
// A bound key is never rewritten by a signed heartbeat (the only rebind is
// an admin /rekey); a NULL-key legacy row binds the key that signed the
// binding beat. Role, launcher, capability and force are the operator's own
// act. No loser path rewrites another operator's operator_id or public_key:
// the collision check in HeartbeatAuthorized refuses first.
//
// An UNSIGNED heartbeat is the one bounded transition (AGENTS.md's
// rollback exception): it is accepted only from the row's operator, only on
// a row that already has a bound key, only while BOTH the request version
// and the stored runtime_version parse and predate the signing release
// v2.20.8 ('s gate — see refreshLivenessOnly), and it touches
// liveness fields only.
// DELETION CONDITION: the owner confirms `GET /machines` shows no live
// unsigned machine — a live row (stale=false) whose runtime_version
// predates this release, because this release's daemon signs — and the next
// release deletes this branch.
func (m *AdminManager) refreshMachine(tx *sql.Tx, req MachineHeartbeat, proof HeartbeatProof, curRole, curSuperseded, curOperator, curPublicKey, curVersion string, live any) (*HeartbeatResult, error) {
	operator := curOperator
	if operator == "" {
		operator = proof.CallerID
	}
	if !proof.Signed {
		if curPublicKey == "" {
			return nil, Fail(api.ErrAuthUnauthorized, "", "machine %q has no bound public key; only a beat signed by the key it presents can bind one", req.MachineID)
		}
		return m.refreshLivenessOnly(tx, req, operator, curRole, curSuperseded, curVersion, live)
	}
	if req.PublicKey != "" && curPublicKey != "" && req.PublicKey != curPublicKey {
		return nil, Fail(api.ErrAuthForbidden, "", "machine %q is bound to a different public key; a heartbeat cannot rebind it (admin rekey only)", req.MachineID)
	}
	if curPublicKey == "" && (req.PublicKey == "" || proof.KeyB64 != req.PublicKey) {
		return nil, Fail(api.ErrAuthUnauthorized, "", "binding machine %q requires a signature by the public key it presents", req.MachineID)
	}
	if err := commitProof(proof); err != nil {
		return nil, err
	}
	role := curRole
	if role == "" {
		role = api.MachineRoleRunner
	}
	var refused, supersededAt string
	if req.Role == api.MachineRoleInteractive {
		if req.Force {
			if err := m.supersedeOthersTx(tx, operator, req.MachineID); err != nil {
				return nil, err
			}
			role = api.MachineRoleInteractive
			supersededAt = ""
		} else {
			holder, err := interactiveHolderTx(tx, operator, req.MachineID)
			if err != nil {
				return nil, err
			}
			if holder != "" && holder != req.MachineID {
				// A superseded daemon asking for interactive again stays a
				// runner and is told who holds the role; an ordinary refused
				// caller is a runner too.
				if curSuperseded != "" {
					// reported below from the interactive holder
				} else {
					refused = "interactive held by " + holder
				}
				role = api.MachineRoleRunner
			} else if holder == "" {
				role = api.MachineRoleInteractive
				supersededAt = ""
			}
		}
	}
	resultSuperseded := ""
	if role == api.MachineRoleRunner {
		supersededAt = curSuperseded
		if supersededAt != "" {
			if by, err := interactiveHolderTx(tx, operator, req.MachineID); err == nil {
				resultSuperseded = by
			}
		}
	}
	var rv, md any
	if req.RuntimeVersion != "" {
		rv = req.RuntimeVersion
	}
	if req.Mode != "" {
		md = req.Mode
	}
	var cap any
	if req.Capability != nil {
		cap = capabilityJSON(req.Capability)
	}
	var launcher any
	if req.Launcher != "" {
		launcher = req.Launcher
	}
	// A bound key is never rewritten by a heartbeat; a legacy row (NULL)
	// binds the key that signed this beat.
	var bindKey any
	if curPublicKey == "" {
		bindKey = nullableText(req.PublicKey)
	}
	if _, err := tx.Exec(`UPDATE machines SET last_heartbeat_at=?2,
		runtime_version=COALESCE(?3, runtime_version),
		mode=COALESCE(?4, mode), live_wi_ids=COALESCE(?5, live_wi_ids),
		public_key=COALESCE(?6, public_key), launcher=COALESCE(?7, launcher),
		role=?8, operator_id=?9, superseded_at=?10, capability=COALESCE(?11, capability)
		WHERE id=?1`, req.MachineID, Now(), rv, md, live,
		bindKey, launcher, role, operator, nullableText(supersededAt), cap); err != nil {
		return nil, err
	}
	return &HeartbeatResult{
		MachineID:          req.MachineID,
		OperatorID:         operator,
		MachineFaceVersion: api.MachineFaceVersion,
		Role:               role,
		Superseded:         resultSuperseded != "",
		SupersededBy:       resultSuperseded,
		InteractiveRefused: refused,
	}, nil
}

// heartbeatSigningVersion is the release whose daemon signs every beat;
// the unsigned-heartbeat transition accepts only daemons older than it.
var heartbeatSigningVersion = [3]int{2, 20, 8}

// parseMachineVersion parses "vX.Y.Z" (the v is optional, exactly three
// numeric components). dev, empty and every other unparseable shape answer
// ok=false — the version gate fails closed.
func parseMachineVersion(v string) ([3]int, bool) {
	s := strings.TrimSpace(v)
	if strings.HasPrefix(s, "v") {
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		if p == "" {
			return [3]int{}, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return [3]int{}, false
			}
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// belowSigningRelease answers whether v parses (see parseMachineVersion) and
// predates the signing release v2.20.8.
func belowSigningRelease(v string) bool {
	got, ok := parseMachineVersion(v)
	if !ok {
		return false
	}
	for i := range got {
		if got[i] != heartbeatSigningVersion[i] {
			return got[i] < heartbeatSigningVersion[i]
		}
	}
	return false // exactly the signing release: its daemon signs
}

// refreshLivenessOnly is the bounded unsigned transition (see refreshMachine
// for its deletion condition): only last_heartbeat_at, runtime_version, mode
// and live_wi_ids are written, through the heartbeat's open transaction.
// Role, launcher, capability, public_key and operator_id are never touched —
// an old daemon restates them in its body, and the identity fields on an
// unproven beat mean nothing.
//
// The version gate and the ratchet: the unsigned beat is
// accepted only while BOTH the request version and the stored runtime_version
// parse and predate v2.20.8 — dev, empty or unparseable on either side is
// refused (AUTH-401) and writes nothing. The ratchet needs no extra state:
// an unsigned beat can never raise the stored version to the signing
// release (both sides must already be below it), so a stored runtime_version
// that parses at v2.20.8 or later means a signed v2.20.8+ beat refreshed
// this row — and the row is closed. Accepted residual (the owner's ruling):
// a signed beat that omits runtime_version leaves the stored value in place
// (COALESCE), and one that claims a pre-v2.20.8 version writes that value —
// both require the machine key (the daemon itself), not the leaked operator
// API key this gate targets; and on the gate's other side a leaked operator
// API key alone keeps a retired pre-v2.20.8 machine online (its unsigned
// beats never ratchet the row closed, so the row stays open for as long as
// the key is held); only deleting the branch closes both.
func (m *AdminManager) refreshLivenessOnly(tx *sql.Tx, req MachineHeartbeat, operator, curRole, curSuperseded, curVersion string, live any) (*HeartbeatResult, error) {
	if !belowSigningRelease(req.RuntimeVersion) || !belowSigningRelease(curVersion) {
		return nil, Fail(api.ErrAuthUnauthorized, "",
			"machine %q unsigned heartbeat refused: both the request version %q and the stored runtime_version %q must parse and predate v2.20.8 (a signed v2.20.8+ beat ratchets the row closed)",
			req.MachineID, req.RuntimeVersion, curVersion)
	}
	var rv, md any
	if req.RuntimeVersion != "" {
		rv = req.RuntimeVersion
	}
	if req.Mode != "" {
		md = req.Mode
	}
	if _, err := tx.Exec(`UPDATE machines SET last_heartbeat_at=?2,
		runtime_version=COALESCE(?3, runtime_version),
		mode=COALESCE(?4, mode), live_wi_ids=COALESCE(?5, live_wi_ids)
		WHERE id=?1`, req.MachineID, Now(), rv, md, live); err != nil {
		return nil, err
	}
	if curRole == "" {
		curRole = api.MachineRoleRunner
	}
	res := &HeartbeatResult{
		MachineID:          req.MachineID,
		OperatorID:         operator,
		MachineFaceVersion: api.MachineFaceVersion,
		Role:               curRole,
	}
	if curRole == api.MachineRoleRunner && curSuperseded != "" {
		if by, err := interactiveHolderTx(tx, operator, req.MachineID); err == nil && by != "" {
			res.Superseded, res.SupersededBy = true, by
		}
	}
	return res, nil
}

func interactiveHolderTx(tx *sql.Tx, operatorID, except string) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM machines WHERE operator_id=?1 AND role='interactive' AND id<>?2
		ORDER BY last_heartbeat_at DESC LIMIT 1`, operatorID, except).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// supersedeOthersTx demotes the operator's other interactive machines
// (inside the caller's transaction); the standalone query pair it used
// to have is gone — one heartbeat is one transaction.
func (m *AdminManager) supersedeOthersTx(tx *sql.Tx, operatorID, except string) error {
	_, err := tx.Exec(`UPDATE machines SET role='runner', superseded_at=?1
		WHERE operator_id=?2 AND role='interactive' AND id<>?3`, Now(), operatorID, except)
	return err
}

func (m *AdminManager) operatorHasPeer(operatorID, machineID string, staleAfter time.Duration) bool {
	if operatorID == "" {
		return false
	}
	if staleAfter <= 0 {
		staleAfter = machineStaleDefault
	}
	cutoff := time.Now().UTC().Add(-staleAfter).Format(time.RFC3339Nano)
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE operator_id=?1 AND id<>?2
		AND last_heartbeat_at >= ?3`, operatorID, machineID, cutoff).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// OperatorHasPeer is the server-side multi_machine check (the caller's
// operator has another machine with a fresh heartbeat, excluding callers).
func (m *AdminManager) OperatorHasPeer(operatorID, machineID string, staleAfter time.Duration) (bool, error) {
	return m.operatorHasPeer(operatorID, machineID, staleAfter), nil
}

// Rekey overwrites a machine's bound public key (admin only at the server
// face). There is no key-history table: the old key simply stops verifying.
func (m *AdminManager) Rekey(machineID, publicKey string) error {
	if strings.TrimSpace(machineID) == "" {
		return Fail(api.ErrCoreBadInput, "", "machine id required")
	}
	if strings.TrimSpace(publicKey) == "" {
		return Fail(api.ErrCoreBadInput, "", "public_key required")
	}
	res, err := m.db.Exec(`UPDATE machines SET public_key=?2 WHERE id=?1`, machineID, publicKey)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Fail(api.ErrCoreNotFound, "", "machine %q", machineID)
	}
	return nil
}

// ResolveHolder answers the machine currently holding wi (id or slug), its
// freshness and capability. A running/blocked WI with no machine, or a
// machine row that no longer exists, is CORE-410; an unknown WI is
// CORE-404.
func (m *AdminManager) ResolveHolder(wi string, staleAfter time.Duration) (*api.HolderInfo, error) {
	var id, status, machine, capJSON, lastHB string
	err := m.db.QueryRow(`SELECT w.id, w.status, COALESCE(w.session_machine,''),
		COALESCE(m.capability,''), COALESCE(m.last_heartbeat_at,'')
		FROM work_items w
		LEFT JOIN machines m ON m.id = w.session_machine
		WHERE w.id=?1 OR (SELECT p.name FROM projects p WHERE p.id=w.project_id)||'#'||w.seq=?1`, wi).
		Scan(&id, &status, &machine, &capJSON, &lastHB)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "work item %q", wi)
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(machine) == "" {
		return nil, Fail(api.ErrCoreGone, "", "work item %s has no registered holder machine (queued, terminal, or its machine was deregistered)", id)
	}
	var exists int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id=?1`, machine).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, Fail(api.ErrCoreGone, "", "holder machine %s of work item %s is deregistered", machine, id)
	}
	if staleAfter <= 0 {
		staleAfter = machineStaleDefault
	}
	h := &api.HolderInfo{MachineID: machine, Status: status}
	if hb, perr := time.Parse(time.RFC3339, lastHB); perr == nil {
		age := int64(time.Since(hb).Seconds())
		if age < 0 {
			age = 0
		}
		h.AgeSeconds = age
		h.Fresh = time.Duration(age)*time.Second <= staleAfter
	}
	if capJSON != "" {
		var c api.CapabilityCard
		if json.Unmarshal([]byte(capJSON), &c) == nil {
			h.Capability = &c
		}
	}
	return h, nil
}

// HolderOfWI is ResolveHolder with the default stale window.
func (m *AdminManager) HolderOfWI(wi string) (*api.HolderInfo, error) {
	return m.ResolveHolder(wi, machineStaleDefault)
}

// MachineRow is one machines read-model row (the admin machines face).
// SupersededAt is additive: the supersession stamp the
// console's health view marks a machine with; the /api/v1 mapping
// ignores it.
type MachineRow struct {
	ID              string   `json:"id"`
	RuntimeVersion  string   `json:"runtime_version,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	LiveWIIDs       []string `json:"live_wi_ids"`
	LastHeartbeatAt string   `json:"last_heartbeat_at"`
	PublicKey       string   `json:"public_key,omitempty"`
	Launcher        string   `json:"launcher,omitempty"`
	Role            string   `json:"role,omitempty"`
	OperatorID      string   `json:"operator_id,omitempty"`
	SupersededAt    string   `json:"superseded_at,omitempty"`
}

// Machines lists every registered machine oldest-heartbeat-first (the
// admin read face behind GET /api/v1/machines; the server computes the
// stale projection — staleness is presentation, not storage).
func (m *AdminManager) Machines() ([]MachineRow, error) {
	rows, err := m.db.Query(`SELECT id, COALESCE(runtime_version,''), COALESCE(mode,''),
		COALESCE(live_wi_ids,'[]'), last_heartbeat_at, COALESCE(public_key,''),
		COALESCE(launcher,''), COALESCE(role,'runner'), COALESCE(operator_id,''),
		COALESCE(superseded_at,'')
		FROM machines ORDER BY last_heartbeat_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MachineRow
	for rows.Next() {
		var r MachineRow
		var live string
		if err := rows.Scan(&r.ID, &r.RuntimeVersion, &r.Mode, &live, &r.LastHeartbeatAt,
			&r.PublicKey, &r.Launcher, &r.Role, &r.OperatorID, &r.SupersededAt); err != nil {
			return nil, err
		}
		r.LiveWIIDs = []string{}
		_ = json.Unmarshal([]byte(live), &r.LiveWIIDs)
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func capabilityJSON(c *api.CapabilityCard) any {
	if c == nil {
		return nil
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	return string(b)
}

// nullableJSON marshals ids for a nullable JSON-array column: nil stays
// NULL ("keep the stored value" in the upsert's COALESCE), an empty slice
// stores [] ("replace with nothing held live").
func nullableJSON(ids []string) any {
	if ids == nil {
		return nil
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return nil // []string always marshals; defensive only
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Projects

var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-_]{0,39}$`)

// CreateProjectRequest is the projects create body.
type CreateProjectRequest = api.ProjectCreateRequest

// CreateProject inserts a project.
func (m *AdminManager) CreateProject(req CreateProjectRequest) (*api.Project, error) {
	if !projectNameRe.MatchString(req.Name) {
		return nil, Fail(api.ErrCoreBadInput, "", "project name must be 1-40 chars of [a-z0-9-_]")
	}
	if req.Repos == nil {
		req.Repos = []api.ProjectRepo{}
	}
	repos, err := json.Marshal(req.Repos)
	if err != nil {
		return nil, err
	}
	id, err := NewID("prj_")
	if err != nil {
		return nil, err
	}
	now := Now()
	if _, err := m.db.Exec(`INSERT INTO projects(id, name, description, repos, created_at, updated_at)
		VALUES (?1,?2,?3,?4,?5,?5)`,
		id, req.Name, req.Description, string(repos), now); err != nil {
		if isUniqueViolation(err) {
			return nil, Fail(api.ErrCoreConflict, "", "project %q already exists", req.Name)
		}
		return nil, err
	}
	return m.GetProject(id)
}

func (m *AdminManager) GetProject(idOrName string) (*api.Project, error) {
	var p api.Project
	var repos string
	err := m.db.QueryRow(`SELECT id, name, description, repos, created_at, updated_at
		FROM projects WHERE id=?1 OR name=?1`, idOrName).
		Scan(&p.ID, &p.Name, &p.Description, &repos, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "project %q", idOrName)
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(repos), &p.Repos)
	if p.Repos == nil {
		p.Repos = []api.ProjectRepo{}
	}
	p.Members, err = m.projectMembers(p.ID)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (m *AdminManager) projectMembers(projectID string) ([]api.ProjectMember, error) {
	rows, err := m.db.Query(`SELECT user_id, role FROM project_members WHERE project_id=?1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.ProjectMember{}
	for rows.Next() {
		var mem api.ProjectMember
		if err := rows.Scan(&mem.UserID, &mem.Role); err != nil {
			return nil, err
		}
		out = append(out, mem)
	}
	return out, rows.Err()
}

// ListProjectMemberContacts returns the project's human members as id +
// display name ordered by display name then id; callerID is left out
// unless includeCaller is set (the daemon's spawn-time reporter
// resolution asks for its own key owner's row too). One join decides the
// scope: machine members never leave this function, so no client
// re-classifies peers. An empty display name is kept (clients render
// their own placeholder).
func (m *AdminManager) ListProjectMemberContacts(projectID, callerID string, includeCaller bool) ([]api.ProjectMemberContact, error) {
	excludeCaller, args := "", []any{projectID}
	if !includeCaller {
		excludeCaller, args = " AND u.id<>?2", append(args, callerID)
	}
	rows, err := m.db.Query(`SELECT u.id, u.display_name
		FROM project_members pm JOIN users u ON u.id=pm.user_id
		WHERE pm.project_id=?1 AND u.kind='human'`+excludeCaller+`
		ORDER BY u.display_name, u.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.ProjectMemberContact{}
	for rows.Next() {
		var c api.ProjectMemberContact
		if err := rows.Scan(&c.ID, &c.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListProjects returns the oldest listProjectsCap projects and the total
// project count (the caller's object access is decided per object at the
// business endpoints).
func (m *AdminManager) ListProjects() ([]api.Project, int, error) {
	total, err := m.countRows("projects")
	if err != nil {
		return nil, 0, err
	}
	rows, err := m.db.Query(`SELECT id FROM projects ORDER BY created_at LIMIT ?1`, listProjectsCap)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	out := []api.Project{}
	for _, id := range ids {
		p, err := m.GetProject(id)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *p)
	}
	return out, total, nil
}

// AddMember inserts/updates a membership row.
func (m *AdminManager) AddMember(projectID, userID, role string) error {
	switch role {
	case "viewer", "writer", "maintainer":
	default:
		return Fail(api.ErrCoreBadInput, "", "role must be viewer|writer|maintainer")
	}
	return upsertMember(m.db, projectID, userID, role)
}

// upsertMember writes one membership row through ex (the DB or a tx).
func upsertMember(ex interface {
	Exec(string, ...any) (sql.Result, error)
}, projectID, userID, role string) error {
	_, err := ex.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at)
		VALUES (?1,?2,?3,?4) ON CONFLICT(project_id, user_id) DO UPDATE SET role=excluded.role`,
		projectID, userID, role, Now())
	return err
}

// ---------------------------------------------------------------------------
// Project patch (M3): description and repos plus membership management.

// ProjectPatchRequest is the PATCH /api/v1/projects/{id} body.
type ProjectPatchRequest = api.ProjectPatchRequest

// PatchProject applies a project patch. Membership CAS tradeoff vs v0:
// v0 guarded a wholesale member-list replacement with members_version
// plus expected_removals (a shorter-than-intended list could otherwise
// silently drop members). The v1 wire shape cannot express that hazard —
// every mutation names its user explicitly — so the version token is
// dropped: upserts and removes are per-row idempotent operations and
// concurrent patches converge row-wise under the single SQLite writer.
// Removal runs AFTER the upsert loop, so an explicit removal wins when
// the same user appears in both lists of one request. The whole patch is
// one transaction: a refused entry (bad role, unknown user) leaves the
// project exactly as it was.
func (m *AdminManager) PatchProject(idOrName string, req ProjectPatchRequest) (*api.Project, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var projectID string
	err = tx.QueryRow(`SELECT id FROM projects WHERE id=?1 OR name=?1`, idOrName).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "project %q", idOrName)
	}
	if err != nil {
		return nil, err
	}
	if req.Members != nil {
		for _, mem := range *req.Members {
			switch mem.Role {
			case "viewer", "writer", "maintainer":
			default:
				return nil, Fail(api.ErrCoreBadInput, "", "member role must be viewer|writer|maintainer (got %q)", mem.Role)
			}
			// Pre-check the user so a bad id answers 404, not an FK 500.
			var one int
			if err := tx.QueryRow(`SELECT 1 FROM users WHERE id=?1`, mem.UserID).Scan(&one); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, Fail(api.ErrCoreNotFound, "", "user %s", mem.UserID)
				}
				return nil, err
			}
			if err := upsertMember(tx, projectID, mem.UserID, mem.Role); err != nil {
				return nil, err
			}
		}
	}
	if req.RemoveMembers != nil {
		for _, uid := range *req.RemoveMembers {
			if _, err := tx.Exec(`DELETE FROM project_members WHERE project_id=?1 AND user_id=?2`,
				projectID, uid); err != nil {
				return nil, err
			}
		}
	}
	sets := []string{"updated_at=?"}
	args := []any{Now()}
	if req.Description != nil {
		sets = append(sets, "description=?")
		args = append(args, *req.Description)
	}
	if req.Repos != nil {
		blob, err := json.Marshal(*req.Repos)
		if err != nil {
			return nil, err
		}
		sets = append(sets, "repos=?")
		args = append(args, string(blob))
	}
	args = append(args, projectID)
	if _, err := tx.Exec(`UPDATE projects SET `+strings.Join(sets, ",")+` WHERE id=?`, args...); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return m.GetProject(projectID)
}

// ---------------------------------------------------------------------------
// Project hard delete

// DeleteProject hard-deletes a project and everything it owns — work
// items, events, dependency edges, memories (with their FTS rows),
// memberships and revoked project-scoped api keys — in ONE SQLite
// transaction, recording one admin_audit row. The checks and the
// cascade run inside that same transaction (no check-then-act gap):
//
//   - an unknown id/name answers CORE-404;
//   - any work item not in a terminal status (wrapped|failed|canceled —
//     bundled execution tickets and bundle-less registry/epic tickets
//     alike; the check is a plain row query) answers CORE-409 naming
//     every offending <project>#<seq> slug;
//   - any unrevoked api key scoped to the project answers CORE-409 naming
//     the key ids (a revoked scoped key is deleted silently instead).
//
// dryRun runs the same checks and answers the same result with nothing
// written (the transaction rolls back; no audit row). The per-WI bare
// git repos are NOT touched here: removing them is disk I/O, so it
// belongs to the caller AFTER the commit (no I/O while the single-writer
// connection is held). The deleted work-item ids travel in the answer
// and are recorded in admin_audit.wi_ids — the durable intent the
// startup replay uses to converge a delete that died between its commit
// and the removal (crash-equals-shutdown: before the commit nothing
// happened, after it the rows are gone and the next start removes any
// leftover repos).
//
// Cross-project references cannot exist (dependency edges refuse them,
// parents must share the project, a memory's work item must belong to
// the memory's project), so the explicit delete list is complete; with
// foreign_keys(1) on, any reference the list misses fails the
// transaction loudly and rolls back instead of leaving an orphan.
func (m *AdminManager) DeleteProject(idOrName, actorID string, dryRun bool) (*api.ProjectDeleteResult, []string, error) {
	tx, err := m.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	var projectID, name string
	err = tx.QueryRow(`SELECT id, name FROM projects WHERE id=?1 OR name=?1`, idOrName).Scan(&projectID, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, Fail(api.ErrCoreNotFound, "", "project %q", idOrName)
	}
	if err != nil {
		return nil, nil, err
	}
	// Non-terminal work items: every one of them blocks the delete.
	var slugs []string
	rows, err := tx.Query(`SELECT seq FROM work_items WHERE project_id=?1
		AND status NOT IN ('wrapped','failed','canceled') ORDER BY seq`, projectID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			rows.Close()
			return nil, nil, err
		}
		slugs = append(slugs, fmt.Sprintf("%s#%d", name, seq))
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(slugs) > 0 {
		return nil, nil, Fail(api.ErrCoreConflict, "",
			"project %s has non-terminal work items (move them to wrapped|failed|canceled first): %s",
			name, strings.Join(slugs, ", "))
	}
	// Unrevoked project-scoped api keys: every one of them blocks the
	// delete (a project no key can reach must not strand its scope).
	var keyIDs []string
	rows, err = tx.Query(`SELECT id FROM api_keys WHERE project_id=?1 AND revoked_at IS NULL ORDER BY id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		keyIDs = append(keyIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(keyIDs) > 0 {
		return nil, nil, Fail(api.ErrCoreConflict, "",
			"project %s has unrevoked project-scoped api keys (revoke them first): %s",
			name, strings.Join(keyIDs, ", "))
	}
	// The work items the cascade deletes (the audit row's wi_ids).
	wiIDs := []string{}
	rows, err = tx.Query(`SELECT id FROM work_items WHERE project_id=?1`, projectID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		wiIDs = append(wiIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	res := &api.ProjectDeleteResult{ID: projectID, Name: name, WorkItems: len(wiIDs), DryRun: dryRun}
	if dryRun {
		return res, wiIDs, nil // nothing written: the deferred rollback discards the transaction
	}
	// The cascade, children before parents. work_items deletes parents
	// and children in ONE statement: SQLite checks the parent_id
	// self-references at statement end, so a parent and its child inside
	// the set go together.
	for _, stmt := range []string{
		`DELETE FROM memory_fts WHERE mem_id IN (SELECT id FROM memories WHERE project_id=?1)`,
		`DELETE FROM memories WHERE project_id=?1`,
		`DELETE FROM wi_events WHERE work_item_id IN (SELECT id FROM work_items WHERE project_id=?1)`,
		`DELETE FROM wi_dependencies WHERE blocking_wi_id IN (SELECT id FROM work_items WHERE project_id=?1)
			OR blocked_wi_id IN (SELECT id FROM work_items WHERE project_id=?1)`,
		`DELETE FROM work_items WHERE project_id=?1`,
		`DELETE FROM project_members WHERE project_id=?1`,
		`DELETE FROM api_keys WHERE project_id=?1 AND revoked_at IS NOT NULL`,
		`DELETE FROM projects WHERE id=?1`,
	} {
		if _, err := tx.Exec(stmt, projectID); err != nil {
			return nil, nil, err
		}
	}
	// The audit row — plain text columns, no FK, so it survives the
	// cascade it records.
	blob, err := json.Marshal(wiIDs)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(`INSERT INTO admin_audit(actor, created_at, project_id, project_name, wi_ids)
		VALUES (?1,?2,?3,?4,?5)`, actorID, Now(), projectID, name, string(blob)); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return res, wiIDs, nil
}

// DeletedWorkItemIDs answers the union of every admin_audit row's
// wi_ids — the work items whose bare git repos a performed delete
// recorded. The startup replay removes those repos (idempotently: a
// repo already gone is a no-op), converging a delete that died between
// its commit and the removal.
func (m *AdminManager) DeletedWorkItemIDs() ([]string, error) {
	rows, err := m.db.Query(`SELECT wi_ids FROM admin_audit`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []string{}
	for rows.Next() {
		var blob string
		if err := rows.Scan(&blob); err != nil {
			return nil, err
		}
		var ids []string
		if err := json.Unmarshal([]byte(blob), &ids); err != nil {
			return nil, fmt.Errorf("admin_audit: decode wi_ids %q: %v", blob, err)
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Backup (M4)

// sanitizeFilename keeps a backup name filesystem-safe.
func sanitizeFilename(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// Backup takes an on-demand SQLite snapshot (M4) via VACUUM INTO under
// <dataDir>/backups and returns the snapshot file name. Scope: the SQLite
// file only — memory content files (dataDir/memory) are NOT part of this
// snapshot (the v0 backupBarrier machinery is not part of the v1 lane).
func (m *AdminManager) Backup(dataDir string) (string, error) {
	if dataDir == "" {
		return "", Fail(api.ErrCoreInternal, "", "backup: data dir not configured")
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "backups"), 0o750); err != nil {
		return "", err
	}
	name := sanitizeFilename("aihub-" + Now() + ".db")
	target := filepath.Join(dataDir, "backups", name)
	if _, err := m.db.Exec(`VACUUM INTO ?1`, target); err != nil {
		return "", Fail(api.ErrCoreStorage, "", "vacuum into %s: %v", target, err)
	}
	return name, nil
}
