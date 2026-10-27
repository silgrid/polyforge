package server

// messaging.go: the user-to-user messaging HTTP face (workbench#11).
// All routes are Bearer-authenticated; read access is conversation member
// or global admin. The strict send decoder refuses unknown fields, so no
// chat/WI/machine/broadcast/bulk address exists.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

func decodeBodyStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return core.Fail(api.ErrCoreBadInput, "", "invalid body: %v", err)
	}
	return nil
}

func (s *Server) hSendMessage(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.SendMessage
	if err := decodeBodyStrict(r, &req); err != nil {
		return err
	}
	msg, err := s.Admin.Send(idn.UserID, req)
	if err != nil {
		return err
	}
	s.pending.signal()
	return reply(w, http.StatusOK, msg)
}

func (s *Server) hListConversations(w http.ResponseWriter, r *http.Request, idn *identity) error {
	list, err := s.Admin.ListConversations(idn.UserID, idn.Role == "admin")
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, api.ConversationsAnswer{Conversations: list})
}

func (s *Server) hListMessages(w http.ResponseWriter, r *http.Request, idn *identity) error {
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	page, err := s.Admin.Page(idn.UserID, r.PathValue("id"), idn.Role == "admin", after, before, limit)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, page)
}

func (s *Server) hGetMessage(w http.ResponseWriter, r *http.Request, idn *identity) error {
	msg, err := s.Admin.Get(idn.UserID, r.PathValue("id"), idn.Role == "admin")
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, msg)
}

func (s *Server) hAckMessage(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.AckMessage
	if err := decodeBodyStrict(r, &req); err != nil {
		return err
	}
	if err := s.Admin.Acknowledge(idn.UserID, req); err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]any{"ok": true})
}

// hDeliveredMessage advances the caller's machine delivery cursor only; the
// caller's Bearer credential decides the member row and read/unread never
// move. Reachability (AGENTS.md): aihub Bearer route — a workflow step agent
// holds no aihub key and the CLI's step guard refuses msg verbs before any
// wire call, so this entry point is not step-reachable.
func (s *Server) hDeliveredMessage(w http.ResponseWriter, r *http.Request, idn *identity) error {
	var req api.AckMessage
	if err := decodeBodyStrict(r, &req); err != nil {
		return err
	}
	if err := s.Admin.MarkDelivered(idn.UserID, req); err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]any{"ok": true})
}
