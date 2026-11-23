package server

// landinglock.go: the signed machine face of the generic lease base
// (polyforge#245, 245-lease). Four signed machine routes — acquire/poll,
// renew, release and lookup-by-request-id — authorized by the machine's
// operator, the ticket's project write and the ticket's current session.
// The poll and the manage lanes are separate anti-replay buckets scoped
// by work item (decided Q12).

import (
	"net/http"
	"strings"
)

func (s *Server) hLeaseAcquire(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	return errLeaseNotImplemented
}

func (s *Server) hLeaseRenew(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	return errLeaseNotImplemented
}

func (s *Server) hLeaseRelease(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	return errLeaseNotImplemented
}

func (s *Server) hLeaseLookup(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	return errLeaseNotImplemented
}

// leaseBodyScope is the replay bucket's scope for the POST routes.
func leaseBodyScope(_ *http.Request, _ []byte) string { return "" }

// leaseQueryScope is the lookup route's scope.
func leaseQueryScope(r *http.Request, _ []byte) string {
	return strings.TrimSpace(r.URL.Query().Get("work_item"))
}

var errLeaseNotImplemented = &notImplementedError{}

type notImplementedError struct{}

func (*notImplementedError) Error() string { return "lease: not implemented" }
