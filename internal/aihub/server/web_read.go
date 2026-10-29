package server

// web_read.go: the console reads' shared gate helpers. No
// web-specific policy lives here — every gate is the one the CLI and
// /api/v1 faces use (readableProjects, projectIDOf/canRead, resolveWIGrant
// and the memory visibility helpers), so a key sees exactly its
// CLI/API read gates, and unknown and unreadable are one answer.

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/aihub/memory"
	"github.com/silgrid/polyforge/internal/common/api"
)

// webProject resolves one project name-or-id and answers whether the
// caller may read it. Unknown and unreadable (including a key scoped
// away) are the same answer (false, no error); a storage failure is
// the error the caller's 500 answer renders.
func (s *Server) webProject(idn *identity, name string) (string, bool, error) {
	projectID, err := s.projectIDOf(name)
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.ErrCoreNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	ok, rerr := s.canRead(idn, projectID)
	if rerr != nil {
		var ae *api.Error
		if errors.As(rerr, &ae) && ae.Code == api.ErrAuthForbidden {
			// A key scoped away from the project reads nothing — the
			// same 404 as an unknown project.
			return "", false, nil
		}
		return "", false, rerr
	}
	if !ok {
		return "", false, nil
	}
	return projectID, true, nil
}

// webContextProjects answers the caller's readable projects with their
// ids and names, sorted by name (the picker and the /web/api/context
// answer): every project for an unscoped admin (readable == nil), the
// named set otherwise, nothing for an empty readable set.
func (s *Server) webContextProjects(idn *identity) ([]webContextProject, error) {
	readable, err := s.readableProjects(idn)
	if err != nil {
		return nil, err
	}
	if readable != nil && len(readable) == 0 {
		return []webContextProject{}, nil
	}
	var (
		rows *sql.Rows
		qerr error
	)
	if readable == nil {
		rows, qerr = s.DB.Query(`SELECT id, name FROM projects ORDER BY name`)
	} else {
		ph := make([]string, len(readable))
		args := make([]any, len(readable))
		for i, p := range readable {
			ph[i] = "?" + strconv.Itoa(i+1)
			args[i] = p
		}
		rows, qerr = s.DB.Query(`SELECT id, name FROM projects WHERE id IN (`+
			strings.Join(ph, ",")+`) ORDER BY name`, args...)
	}
	if qerr != nil {
		return nil, qerr
	}
	defer rows.Close()
	out := []webContextProject{}
	for rows.Next() {
		var p webContextProject
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// webReadableNames answers the readable set's project names in one
// statement, sorted by name (the readable-set narrowing helper the
// memory routes reuse).
func (s *Server) webReadableNames(readable []string) ([]string, error) {
	if readable != nil && len(readable) == 0 {
		return nil, nil
	}
	var (
		rows *sql.Rows
		err  error
	)
	if readable == nil {
		rows, err = s.DB.Query(`SELECT name FROM projects ORDER BY name`)
	} else {
		ph := make([]string, len(readable))
		args := make([]any, len(readable))
		for i, p := range readable {
			ph[i] = "?" + strconv.Itoa(i+1)
			args[i] = p
		}
		rows, err = s.DB.Query(`SELECT name FROM projects WHERE id IN (`+
			strings.Join(ph, ",")+`) ORDER BY name`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// webWIReadable answers whether the caller may read the work item id
// (its project's read gate; unknown ids read nothing).
func (s *Server) webWIReadable(idn *identity, wiID string) (bool, error) {
	var projectID string
	err := s.DB.QueryRow(`SELECT project_id FROM work_items WHERE id=?1`, wiID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ok, rerr := s.canRead(idn, projectID)
	if rerr != nil {
		var ae *api.Error
		if errors.As(rerr, &ae) && ae.Code == api.ErrAuthForbidden {
			return false, nil
		}
		return false, rerr
	}
	return ok, nil
}

// webMemEntry projects one memory row for the console lists and search
// groups: the public fields alone, the derived title, the sanitized
// body never included here (the detail route renders it).
func webMemEntry(e *memory.Entry) webMemoryRow {
	row := webMemoryRow{
		ID: e.ID, Type: e.Type, Visibility: e.Visibility,
		Tags: e.Tags, UpdatedAt: e.UpdatedAt, Title: webMemTitle(e),
	}
	if e.WorkItemID != nil {
		row.WorkItemID = *e.WorkItemID
	}
	return row
}

// webMemTitle answers the entry's title (a derived fallback when the
// row carries none).
func webMemTitle(e *memory.Entry) string {
	if e.Title != nil && strings.TrimSpace(*e.Title) != "" {
		return *e.Title
	}
	return "Untitled memory"
}

// webWISlug answers one work item's slug ("<project>#<seq>"), or ""
// when the row is missing.
func (s *Server) webWISlug(wiID string) string {
	var project string
	var seq int64
	err := s.DB.QueryRow(`SELECT p.name, w.seq FROM work_items w JOIN projects p ON p.id=w.project_id WHERE w.id=?1`, wiID).
		Scan(&project, &seq)
	if err != nil {
		return ""
	}
	return project + "#" + strconv.FormatInt(seq, 10)
}
