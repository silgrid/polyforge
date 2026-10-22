package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_workspace.go: the workspace layout checks of `pf doctor` (the
// convention is workspace/layout.go's). They run per mapped workspace
// that exists, never fail and never write — except --prune-scratch:
//
//   - "workspace layout": root and .polyforge/ entries outside the
//     convention, each with its suggested destination, a notes file
//     (workspace.Notes: AGENTS.md, else CLAUDE.md) whose polyforge-managed
//     block lacks the layout section, and a CLAUDE.md beside AGENTS.md
//     that carries a duplicate managed block.
//   - "workspace scratch": scratch/ entries older than 14 days, reported;
//     `pf doctor --prune-scratch` removes them and lists them. Deletion
//     stays an explicit operator act so doctor remains a probe anyone may
//     run at any time.
//   - "workspace memory policy": a .polyforge.yaml without a
//     memory_policy section (`pf init <ws>` appends the default).

const (
	docChLayout    = "workspace layout"
	docChScratch   = "workspace scratch"
	docChMemPolicy = "workspace memory policy"
)

// docRowCap bounds the rows one check's detail lists.
const docRowCap = 20

func capRows(rows []string) []string {
	if len(rows) > docRowCap {
		return append(rows[:docRowCap:docRowCap], fmt.Sprintf("… %d more", len(rows)-docRowCap))
	}
	return rows
}

// workspaceLayoutCheck warns about entries outside the convention.
func workspaceLayoutCheck(wss []string) doctorCheck {
	c := doctorCheck{Name: docChLayout}
	var rows, notes, errs []string
	for _, ws := range wss {
		name, dup, err := workspace.Notes(ws)
		path := filepath.Join(ws, name)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ws, err))
		} else if exists, has, err := workspace.LayoutSection(path); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, err))
		} else if exists && !has {
			notes = append(notes, fmt.Sprintf("%s has no <!-- polyforge:layout --> section in its polyforge-managed block — run `pf init %s` to add it", path, ws))
		}
		if dup {
			notes = append(notes, fmt.Sprintf("%s carries a duplicate polyforge-managed block — %s is the canonical notes file; remove the block from %s by hand (pf never edits it)",
				filepath.Join(ws, workspace.ClaudeNotes), path, workspace.ClaudeNotes))
		}
		strays, err := workspace.StrayEntries(ws)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ws, err))
			continue
		}
		for _, s := range strays {
			rows = append(rows, fmt.Sprintf("%s → %s", filepath.Join(ws, s.Path), s.Dest))
		}
	}
	switch {
	case len(rows) > 0 || len(notes) > 0 || len(errs) > 0:
		c.Status = "warn"
		parts := notes
		if len(rows) > 0 {
			parts = append(parts, fmt.Sprintf("%d entr(ies) outside the workspace layout: %s — share documents through aihub (`pf file edit <wi> --content @f` while the work item is open, else `pf remember --project <p> --type doc.design|doc.review|doc.decision|doc.audit [--work-item <wi>] @f`), move disposable output to scratch/; nothing was moved or deleted",
				len(rows), strings.Join(capRows(rows), "; ")))
		}
		if len(errs) > 0 {
			parts = append(parts, "unreadable: "+strings.Join(errs, "; "))
		}
		c.Detail = strings.Join(parts, "; ")
	default:
		c.Status = "pass"
		c.Detail = fmt.Sprintf("every mapped workspace keeps the layout (root: .repo/, capsules/, scratch/, .polyforge/, CLAUDE.md, CLAUDE.local.md, AGENTS.md, go.work, go.work.sum, %s and dot-entries other than %s backups; .polyforge/: the legacy v1 repo-map/, watchers/)", workspace.FileName, workspace.FileName)
	}
	return c
}

// memoryPolicyCheck warns about a mapped workspace whose .polyforge.yaml
// declares no memory_policy section. A file that is no readable YAML
// mapping is the "workspace yaml" check's fail, not this row's.
func memoryPolicyCheck(wss []string) doctorCheck {
	c := doctorCheck{Name: docChMemPolicy, Status: "pass"}
	var missing []string
	builtin := 0
	for _, ws := range wss {
		_, serr := os.Stat(workspace.PathFor(ws))
		cfg, err := workspace.Load(ws, "", nil)
		switch {
		case os.IsNotExist(serr):
			builtin++
		case err == nil && (cfg == nil || cfg.MemoryPolicy == nil):
			missing = append(missing, ws)
		}
	}
	if len(missing) == 0 {
		c.Detail = "every mapped workspace's " + workspace.FileName + " declares " + workspace.MemoryPolicyKey
		if builtin > 0 {
			c.Detail = fmt.Sprintf("%s; %d workspace(s) without a %s use the built-in default (`pf init <ws>` writes an editable copy)", c.Detail, builtin, workspace.FileName)
		}
		return c
	}
	c.Status = "warn"
	var parts []string
	for _, ws := range capRows(missing) {
		parts = append(parts, fmt.Sprintf("%s declares no %s — the step prompts carry the built-in default policy; run `pf init %s` to append the default section, then edit it", workspace.PathFor(ws), workspace.MemoryPolicyKey, ws))
	}
	c.Detail = strings.Join(parts, "; ")
	return c
}

// scratchCheck reports — or, with prune, removes — the stale scratch
// entries of every mapped workspace.
func scratchCheck(wss []string, now time.Time, prune bool) doctorCheck {
	c := doctorCheck{Name: docChScratch, Status: "pass"}
	var found, errs []string
	for _, ws := range wss {
		rep, err := workspace.Scratch(ws, now, workspace.ScratchMaxAge, prune)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ws, err))
			continue
		}
		for _, n := range rep.Stale {
			found = append(found, filepath.Join(ws, workspace.ScratchDir, n))
		}
		for _, sk := range rep.Skipped {
			errs = append(errs, filepath.Join(ws, workspace.ScratchDir, sk))
		}
	}
	var parts []string
	switch {
	case len(found) > 0 && !prune:
		c.Status = "warn"
		parts = append(parts, fmt.Sprintf("%d scratch entr(ies) older than 14 days: %s — `pf doctor --prune-scratch` removes them", len(found), strings.Join(capRows(found), "; ")))
	case len(found) > 0:
		parts = append(parts, fmt.Sprintf("pruned %d scratch entr(ies) older than 14 days: %s", len(found), strings.Join(found, "; ")))
	case prune:
		parts = append(parts, "no scratch entry older than 14 days: nothing pruned")
	default:
		parts = append(parts, "no scratch entry older than 14 days")
	}
	if len(errs) > 0 {
		c.Status = "warn"
		parts = append(parts, "not checked: "+strings.Join(errs, "; "))
	}
	c.Detail = strings.Join(parts, "; ")
	return c
}
