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
// The poll lane and the manage lane are separate anti-replay buckets. The
// POST routes are scoped by the ticket the signed body names, so one
// waiting ticket's traffic never spends another ticket's or the holder's
// quota (decided Q12); the lookup is a read and keeps the machine-wide
// manage bucket, so its unsigned query never decides a bucket.

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
	kind := r.PathValue("kind")
	key, err := s.leaseKey(g, kind, req)
	if err != nil {
		return err
	}
	out, err := s.Leases.Acquire(leaseRequest(kind, key, req.RequestID, g.id, m.MachineID, idn.UserID))
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, leaseGrantWire(out))
}

// leaseKey answers the key of one acquire. The landing kind derives it on
// the server from the repository the ticket's project declares plus the
// target branch (已定 Q2): the URL is normalized (ssh / https / a name
// collapse), the explicit alias table applies, and a repository the
// project does not declare is refused — so no caller can take another
// project's repository lock by naming it. Kinds that carry their own
// opaque key pass it through.
func (s *Server) leaseKey(g *wiGrant, kind string, req api.LeaseAcquire) (string, error) {
	if kind != "landing" {
		if strings.TrimSpace(req.Key) == "" {
			return "", core.Fail(api.ErrCoreBadInput, "LEASE_BAD_REQUEST",
				"a lease request carries a key")
		}
		return req.Key, nil
	}
	repo := strings.TrimSpace(req.Repo)
	branch := strings.TrimSpace(req.Branch)
	if repo == "" || branch == "" {
		return "", core.Fail(api.ErrCoreBadInput, "LEASE_BAD_REQUEST",
			"the landing kind requires the repository and the target branch")
	}
	canonical, err := s.landingRepo(g.projectID, repo)
	if err != nil {
		return "", err
	}
	return canonical + "\n" + branch, nil
}

// landingRepo normalizes one repository through the ticket's project
// declaration: a declared name resolves to its URL, the normalized
// identity must be one the project declares, and the explicit alias table
// then answers the canonical key (an ambiguous alias is CORE-403, never a
// silent pick).
func (s *Server) landingRepo(projectID, repo string) (string, error) {
	var raw string
	if err := s.DB.QueryRow(`SELECT repos FROM projects WHERE id=?1`, projectID).Scan(&raw); err != nil {
		return "", err
	}
	var declared []api.ProjectRepo
	if err := json.Unmarshal([]byte(raw), &declared); err != nil {
		return "", core.Fail(api.ErrCoreInternal, "",
			"project %s repositories are not JSON: %v", projectID, err)
	}
	for _, r := range declared {
		if r.Name == repo && strings.TrimSpace(r.URL) != "" {
			repo = r.URL
			break
		}
	}
	id, err := core.NormalizeRepoURL(repo)
	if err != nil {
		return "", err
	}
	trusted := false
	for _, r := range declared {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		d, err := core.NormalizeRepoURL(r.URL)
		if err == nil && d.Key() == id.Key() {
			trusted = true
			break
		}
	}
	if !trusted {
		return "", core.Fail(api.ErrAuthForbidden, "LEASE_REPO_NOT_DECLARED",
			"repository %q is not declared by project %s", id.Key(), projectID)
	}
	return s.Repos.Key(repo)
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

// decorateLeaseProjections fills each work item's read-only landing-lock
// projection (polyforge#245 epic 3/3, Q11) from the lease store. It runs on
// the read paths only, never on a write, and a row it cannot project keeps
// no field (unknown) instead of failing the read.
func (s *Server) decorateLeaseProjections(items []core.WIView) {
	if s.Leases == nil {
		return
	}
	for i := range items {
		ll, err := s.Leases.LandingSnapshot(items[i].ID)
		if err != nil || ll == nil {
			continue
		}
		items[i].LeaseProjection = ll
	}
}
