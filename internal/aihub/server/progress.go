package server

// progress.go: the signed caller -> aihub query doors. The project read is
// authorized BEFORE anything is enqueued; the holder resolution answers the
// typed CORE-404/410/504/501; the holder's answer is relayed byte-for-byte
// and nothing is written to the database.

import (
	"net/http"

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
	var req api.ProgressQuery
	if r.ContentLength != 0 {
		if err := decodeBody(r, &req); err != nil {
			return err
		}
	}
	wi := r.PathValue("id")
	view, err := s.WIs.Get(wi, core.GetOptions{})
	if err != nil {
		return err
	}
	ok, err := s.canRead(idn, view.ProjectID)
	if err != nil {
		return err
	}
	if !ok {
		return core.Fail(api.ErrAuthForbidden, "", "the caller may not read work item %s", view.ID)
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
	body, err := s.enqueueQuery(holder.MachineID, kind, view.ID, req.ArtifactPath)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, body)
}
