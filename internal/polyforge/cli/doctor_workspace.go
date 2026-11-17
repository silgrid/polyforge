package cli

import (
	"fmt"
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
//     block lacks the layout section or carries an outdated one (a
//     pre-#121 text — `pf init` refreshes it), and a CLAUDE.md beside
//     AGENTS.md that carries a duplicate managed block.
//   - "workspace scratch": the scratch/ category layout (polyforge#121)
//     — every entry outside the categories (a loose top-level file, an
//     unknown top-level directory, a category occupied by a file or
//     symlink, a loose file inside a category) and every batch past its
//     category's retention, each by its full path, with no row cap;
//     `pf doctor --prune-scratch` removes only the stale batches inside
//     the categories and lists them. Deletion stays an explicit operator
//     act so doctor remains a probe anyone may run at any time.
//   - "workspace memory policy": a mapped workspace path whose cli.toml
//     declares no [memory_policy."<path>".<entry>] block (the built-in
//     default applies; `pf init <ws>` writes an editable copy). A yaml
//     still carrying the retired `memory_policy:`/`summarize:` section is
//     the "workspace yaml" row's fail (the loader refuses it whole).

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
		} else if has {
			// a section that is not the current one is an outdated
			// convention (a pre-#121 layout, before the scratch
			// categories) — `pf init` replaces it in place. The trailing
			// newline is trimmed on both sides: a section at the very end
			// of a file may lack the one LayoutBlock carries.
			text, _, err := workspace.LayoutSectionText(path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", path, err))
			} else if strings.TrimSuffix(text, "\n") != strings.TrimSuffix(workspace.LayoutBlock(), "\n") {
				notes = append(notes, fmt.Sprintf("%s carries an outdated layout section — run `pf init %s` to refresh it", path, ws))
			}
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

// memoryPolicyCheck warns about a mapped workspace path whose cli.toml
// declares no memory_policy block: the step prompts carry
// the built-in default, and `pf init <ws>` writes an editable copy. It
// reads cli.toml only — a workspace yaml is no source of the policy.
func memoryPolicyCheck(cfg Config, wss []string) doctorCheck {
	c := doctorCheck{Name: docChMemPolicy, Status: "pass"}
	var missing []string
	for _, ws := range wss {
		if len(cfg.MemoryPolicy[ws]) == 0 {
			missing = append(missing, ws)
		}
	}
	if len(missing) == 0 {
		c.Detail = fmt.Sprintf("every mapped workspace path declares a [memory_policy.\"<path>\".<entry>] block in cli.toml (%d)", len(wss))
		return c
	}
	c.Status = "warn"
	var parts []string
	for _, ws := range capRows(missing) {
		parts = append(parts, fmt.Sprintf("cli.toml declares no [memory_policy.%q.default] block — the step prompts carry the built-in default policy; run `pf init %s` to write an editable copy, then edit it", ws, ws))
	}
	c.Detail = strings.Join(parts, "; ")
	return c
}

// scratchRetention is the one wording of the categories' retentions —
// the pass detail, the report and the prune line all spell it from here.
const scratchRetention = "raw/ 3 days, tasks/ 14 days, sessions/ 14 days"

// scratchCheck reports — or, with prune, removes — every mapped
// workspace's stale scratch batches, and reports every scratch entry
// outside the categories one by one (polyforge#121: the scratch row
// lists full paths with no cap; strays are never removed, not even with
// prune).
func scratchCheck(wss []string, now time.Time, prune bool) doctorCheck {
	c := doctorCheck{Name: docChScratch, Status: "pass"}
	var stale, stray, errs []string
	for _, ws := range wss {
		rep, err := workspace.Scratch(ws, now, prune)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ws, err))
			continue
		}
		for _, n := range rep.Stale {
			stale = append(stale, filepath.Join(ws, workspace.ScratchDir, n))
		}
		for _, n := range rep.Stray {
			stray = append(stray, filepath.Join(ws, workspace.ScratchDir, n))
		}
		for _, sk := range rep.Skipped {
			errs = append(errs, filepath.Join(ws, workspace.ScratchDir, sk))
		}
	}
	var parts []string
	switch {
	case len(stray) > 0:
		c.Status = "warn"
		parts = append(parts, fmt.Sprintf("%d scratch entr(ies) outside the layout: %s — move them into raw/, tasks/ or sessions/, or delete them by hand; --prune-scratch never removes them (the top level holds only raw/, tasks/, sessions/)",
			len(stray), strings.Join(stray, "; ")))
	}
	switch {
	case len(stale) > 0 && !prune:
		c.Status = "warn"
		parts = append(parts, fmt.Sprintf("%d batch(es) past retention (%s): %s — `pf doctor --prune-scratch` removes them",
			len(stale), scratchRetention, strings.Join(stale, "; ")))
	case len(stale) > 0:
		parts = append(parts, fmt.Sprintf("pruned %d batch(es) past retention: %s", len(stale), strings.Join(stale, "; ")))
	case prune:
		parts = append(parts, "no batch past retention: nothing pruned")
	case len(stray) == 0:
		parts = append(parts, "no stray scratch entry, no batch past its retention ("+scratchRetention+")")
	}
	if len(errs) > 0 {
		c.Status = "warn"
		parts = append(parts, "not checked: "+strings.Join(errs, "; "))
	}
	c.Detail = strings.Join(parts, "; ")
	return c
}
