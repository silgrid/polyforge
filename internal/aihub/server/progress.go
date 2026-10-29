package server

// progress.go: the signed caller -> aihub query doors. The work-item
// read gate runs BEFORE anything is enqueued (a caller without access
// gets the same missing-work-item answer an unknown reference gets, so
// the relay is no existence oracle); the holder resolution answers the
// typed CORE-404/410/504/501; the relay budget answers CORE-429 before
// the holder is reached ( A2); the holder's answer is relayed
// byte-for-byte and nothing is written to the database.

import (
	"net/http"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

func (s *Server) hProgressQuery(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	return s.relayQuery(w, r, idn, m, "progress")
}

func (s *Server) hArtifactQuery(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth) error {
	return s.relayQuery(w, r, idn, m, "artifact")
}

func (s *Server) relayQuery(w http.ResponseWriter, r *http.Request, idn *identity, m machineAuth, kind string) error {
	g, err := s.resolveWIGrant(idn, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := g.requireRead(); err != nil {
		return err
	}
	var req api.ProgressQuery
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			return err
		}
	}
	view, err := s.WIs.Get(g.id, core.GetOptions{})
	if err != nil {
		return err
	}
	if view.Status != api.StatusRunning && view.Status != api.StatusBlocked {
		return core.Fail(api.ErrCoreNotFound, "", "work item %s is %s: no remote progress/artifact snapshot", view.ID, view.Status)
	}
	holder, err := s.Admin.ResolveHolder(view.ID, s.machinesStaleAfter())
	if err != nil {
		return err
	}
	if !holder.Fresh {
		return core.Fail(api.ErrCoreStaleHolder, "",
			"holder machine %s is stale (last heartbeat %ds ago); refusing to render stale progress", holder.MachineID, holder.AgeSeconds)
	}
	method := api.MethodProgressQuery
	if kind == "artifact" {
		method = api.MethodArtifactRead
	}
	if !holder.HasCapability(method) {
		return core.Fail(api.ErrCoreUnsupported, "", "holder machine %s does not advertise %s", holder.MachineID, method)
	}
	if holder.MachineID == m.MachineID {
		return reply(w, http.StatusOK, api.QueryAnswerBody{HolderMachine: holder.MachineID, Local: true})
	}
	if kind == "artifact" && req.ArtifactPath == "" {
		return core.Fail(api.ErrCoreBadInput, "", "artifact_path is required")
	}
	// The last gate before the holder is reached ( A2): every
	// relayed query costs the holder a signed inbox wake and a signed
	// answer, so the budget bounds what any member can make it send.
	if err := s.relay.admit(time.Now(), idn.UserID, holder.MachineID); err != nil {
		return err
	}
	body, err := s.enqueueQuery(holder.MachineID, kind, view.ID, req.ArtifactPath)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, body)
}
