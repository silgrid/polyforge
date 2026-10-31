package server

import (
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
)

// hProjectMemberContacts serves GET /api/v1/projects/{id}/members: the
// project's human members other than the caller, as id + display name.
// The explicit query include_caller=true keeps the caller's own row too
// (any other value, or none, excludes it): the daemon's spawn-time
// reporter resolution sends it with the daemon's key, so a task spawned by
// that key's owner resolves to their own display name.
// The gate is the existing project read (mustRead: member or global
// admin, api-key project scope applied); a non-reader gets the concealed
// CORE-404 and a key scoped to another project the AUTH-403, both before
// any member row is read. Reachability: a step agent cannot reach this
// route — it holds no aihub credential, and the daemon's step routes
// forward only memory reads/writes and the result submit, never this
// route (the daemon's reporter read is internal to its spawn route); its
// only CLI caller, `pf msg contacts`, sends no include_caller and is
// refused inside a workflow step (cli/stepctx.go).
func (s *Server) hProjectMemberContacts(w http.ResponseWriter, r *http.Request, idn *identity) error {
	projectID, err := s.projectIDOf(r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.mustRead(idn, projectID, "project %q", r.PathValue("id")); err != nil {
		return err
	}
	contacts, err := s.Admin.ListProjectMemberContacts(projectID, idn.UserID, r.URL.Query().Get("include_caller") == "true")
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, api.ProjectMemberContacts{Contacts: contacts})
}
