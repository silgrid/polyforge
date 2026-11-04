package server

// comment.go: the append-only work-item comment door.
// POST /api/v1/work-items/{id}/comments takes {body, reply_to} and
// appends one comment; there is no PATCH and no DELETE beside it.
//
// Reachability (AGENTS.md): aihub Bearer route — a workflow step agent
// holds no aihub key and the CLI's step guard refuses `pf file` inside a
// step before any wire call, so this entry point is not step-reachable.

import (
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
)

// hAddWIComment appends one comment. The write gate is the same as
// PATCH's (`pf file edit`): a caller with no relationship to the work
// item gets the missing-work-item answer, a member whose role is too low
// the route's AUTH-403.
func (s *Server) hAddWIComment(w http.ResponseWriter, r *http.Request, idn *identity) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireWrite(); err != nil {
		return err
	}
	var req api.AddCommentRequest
	if err := decodeBodyStrict(r, &req); err != nil {
		return err
	}
	comment, err := s.WIs.AddComment(g.id, idn.UserID, req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusCreated, comment)
}
