package server

// landinglock.go: the signed machine face of the generic lease base
// (polyforge#245, 245-lease). Four routes, all signed machine requests
// (the Bearer user must speak for the machine's operator):
//
//	POST /api/v1/leases/{kind}/acquire   acquire or poll (the waiter's own transaction grants)
//	POST /api/v1/leases/{kind}/renew     extend the holder's lease
//	POST /api/v1/leases/{kind}/release   release the holder, or withdraw a queued request
//	GET  /api/v1/leases/requests/{rid}   look a grant up (no token; release by request id)
//
// Every route requires write access to the ticket's project, the ticket
// to be live (not deactivated) and the signing machine to hold the
// ticket's current session. A step or task agent holds neither an aihub
// key nor a machine key, so it reaches none of them directly; the daemon
// is the only caller.
//
// The poll lane and the manage lane are separate anti-replay buckets and
// both are scoped by work item, so one waiting ticket's traffic never
// spends another ticket's or the holder's quota (decided Q12).

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// leaseAuthorize resolves one lease request's authorization: the work
// item must be writable by the machine's operator, the project live, and
// the signing machine the holder of the ticket's current session. A
// force-ended or reclaimed session therefore refuses the old machine.
func (s *Server) leaseAuthorize(idn *identity, m machineAuth, wiRef string) (*wiGrant, error) {
	g, err := s.resolveWIGrant(idn, wiRef)
	if err != nil {
		return nil, err
	}
	if err := g.requireWrite(); err != nil {
		return nil, err
	}
	if err := s.refuseDeactivated(g.projectID); err != nil {
		return nil, err
	}
	var sessionMachine string
	if err := s.DB.QueryRow(`SELECT COALESCE(session_machine,'') FROM work_items WHERE id=?1`, g.id).
		Scan(&sessionMachine); err != nil {
		return nil, err
	}
	if sessionMachine == "" || sessionMachine != m.MachineID {
		return nil, core.Fail(api.ErrAuthForbidden, "LEASE_SESSION_NOT_HELD",
			"machine %s does not hold the current session of %s", m.MachineID, g.ref)
	}
	return g, nil
}

// leaseRequest is the common identity of one lease call.
func leaseRequest(kind, key, requestID, workItem, machineID, operatorID string) core.LeaseRequest {
	return core.LeaseRequest{Kind: kind, Key: key, RequestID: requestID,
		MachineID: machineID, WorkItemID: workItem, OperatorID: operatorID}
}

// leaseGrantWire maps the core grant onto the shared wire type.
func leaseGrantWire(g *core.LeaseGrant) *api.LeaseGrant {
	if g == nil {
		return nil
	}
	return &api.LeaseGrant{Kind: g.Kind, Key: g.Key, RequestID: g.RequestID,
		Status: g.Status, Token: g.Token, Position: g.Position,
		HeldSince: g.HeldSince, ExpiresAt: g.ExpiresAt, QueuedMS: g.QueuedMS}
}

// hLeaseAcquire serves POST /leases/{kind}/acquire: a new request queues
// or grants, and an existing one polls (the grant happens in this
// request's own transaction).
func (s *Server) hLeaseAcquire(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	var req api.LeaseAcquire
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	g, err := s.leaseAuthorize(idn, m, req.WorkItem)
	if err != nil {
		return err
	}
	out, err := s.Leases.Acquire(leaseRequest(r.PathValue("kind"), req.Key, req.RequestID,
		g.id, m.MachineID, idn.UserID))
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, leaseGrantWire(out))
}

// hLeaseRenew serves POST /leases/{kind}/renew.
func (s *Server) hLeaseRenew(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	var req api.LeaseRenew
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	g, err := s.leaseAuthorize(idn, m, req.WorkItem)
	if err != nil {
		return err
	}
	out, err := s.Leases.Renew(leaseRequest(r.PathValue("kind"), req.Key, req.RequestID,
		g.id, m.MachineID, idn.UserID), req.Token)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, leaseGrantWire(out))
}

// hLeaseRelease serves POST /leases/{kind}/release: a token releases the
// holder, an empty token withdraws the queued request (or releases a
// recovered daemon's own grant by request id).
func (s *Server) hLeaseRelease(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	var req api.LeaseRelease
	if err := decodeBody(r, &req); err != nil {
		return err
	}
	g, err := s.leaseAuthorize(idn, m, req.WorkItem)
	if err != nil {
		return err
	}
	out, err := s.Leases.Release(leaseRequest(r.PathValue("kind"), req.Key, req.RequestID,
		g.id, m.MachineID, idn.UserID), req.Token)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, leaseGrantWire(out))
}

// hLeaseLookup serves GET /leases/requests/{rid}?work_item=<id>: the
// grant (or queue position) this machine and ticket hold for the request
// id, never the plaintext token.
func (s *Server) hLeaseLookup(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	workItem := strings.TrimSpace(r.URL.Query().Get("work_item"))
	if workItem == "" {
		return core.Fail(api.ErrCoreBadInput, "", "work_item is required")
	}
	g, err := s.leaseAuthorize(idn, m, workItem)
	if err != nil {
		return err
	}
	out, err := s.Leases.Lookup(r.PathValue("rid"), m.MachineID, g.id)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, leaseGrantWire(out))
}

// leaseBodyScope is the replay bucket's scope for the POST routes: the
// ticket the body names, so each ticket polls in its own bucket.
func leaseBodyScope(_ *http.Request, body []byte) string {
	var req struct {
		WorkItem string `json:"work_item"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return strings.TrimSpace(req.WorkItem)
}

// leaseQueryScope is the lookup route's scope (its ticket rides in the
// query).
func leaseQueryScope(r *http.Request, _ []byte) string {
	return strings.TrimSpace(r.URL.Query().Get("work_item"))
}
