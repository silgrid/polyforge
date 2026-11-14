package cli

// doctor_light_fallbacks.go: `pf doctor`'s "light verification fallbacks"
// row (U3). It reads, never writes, the latest verify run of
// every live capsule of the mapped workspaces and, when a light request
// ran full, counts those capsules and names the fallback reasons by
// category. The note rode the scoped-gates readiness row until that row's
// check moved into the workflow with the scope engine (scope_ready.py);
// the run history is the doctor's to read, so the note is
// a row of its own. It reports, never judges: always `pass` (or `skip`
// with no mapped workspace), never moves the exit code; out of --harness.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// docChLightFallbacks is the row name.
const docChLightFallbacks = "light verification fallbacks"

// lightFallbacksCheck is the row body.
func lightFallbacksCheck(r *doctorRun) (string, string) {
	if len(r.present) == 0 {
		return "skip", "no mapped workspace directory exists"
	}
	if note := lightFallbackNote(r.present); note != "" {
		return "pass", note
	}
	return "pass", "no live capsule's latest light verification ran full"
}

// lightFallbackKind classifies one tier.reasons entry of a verify
// run's result.json (`repos/<name>: <why>`, the prefix stripped) into its
// reason category, by the engine's full-tier fallback diagnostics and the
// verify run's own reasons; an unknown text is `other`.
func lightFallbackKind(reason string) string {
	s := reason
	if rest, ok := strings.CutPrefix(s, "repos/"); ok {
		if _, why, ok := strings.Cut(rest, ": "); ok {
			s = why
		}
	}
	has := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(s, p) {
				return true
			}
		}
		return false
	}
	switch {
	case has("go list failed", "unexpected go list -test output shape"):
		return "go package graph"
	case strings.Contains(s, "analyzer's own config file"):
		return "stack config file changed"
	case has("carries:") || strings.Contains(s, "covered by no carries"):
		return "carries"
	case has("conf rejects"):
		return "conf"
	case has("no supported stack manifest", "several stack manifests", "cannot read package.json"):
		return "no analyzer"
	case has("python:", "rust:"):
		return "analyzer could not resolve"
	case has("not a git work tree", "cannot resolve the base", "cannot diff") ||
		strings.Contains(s, "outside the engine root") || strings.Contains(s, "engine root's place"):
		return "git"
	case has("engine-failed:", "empty:") || s == "no touched repo has a plan":
		return "unplanned"
	case strings.Contains(s, "no fallback diagnostic"):
		return "unexplained"
	}
	return "other"
}

// lightFallbackNote reads, never writes, the latest verify run
// (<ws>/capsules/<name>/runtime-state/verify/<n>/result.json, the highest
// numeric <n>) of every live capsule — a `.quarantined.` one is a set-aside
// scene — of the mapped workspaces, counts those whose light request ran
// full and answers the note naming their reasons by category ("" when none
// did). An unreadable or unparsable result.json is
// skipped; a light run that ran full with no tier.reasons is `unexplained`.
func lightFallbackNote(wss []string) string {
	capsules := 0
	kinds := map[string]int{}
	for _, ws := range wss {
		root := filepath.Join(ws, "capsules")
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.Contains(e.Name(), ".quarantined.") {
				continue
			}
			runs := filepath.Join(root, e.Name(), filepath.FromSlash("runtime-state/verify"))
			latest := -1
			if rs, err := os.ReadDir(runs); err == nil {
				for _, r := range rs {
					if n, err := strconv.Atoi(r.Name()); err == nil && r.IsDir() && n > latest {
						latest = n
					}
				}
			}
			if latest < 0 {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(runs, strconv.Itoa(latest), "result.json"))
			if err != nil {
				continue
			}
			var res struct {
				Tier struct {
					Requested string   `json:"requested"`
					Effective string   `json:"effective"`
					Reasons   []string `json:"reasons"`
				} `json:"tier"`
			}
			if json.Unmarshal(raw, &res) != nil || res.Tier.Requested != "light" || res.Tier.Effective != "full" {
				continue
			}
			capsules++
			if len(res.Tier.Reasons) == 0 {
				kinds["unexplained"]++
			}
			for _, why := range res.Tier.Reasons {
				kinds[lightFallbackKind(why)]++
			}
		}
	}
	if capsules == 0 {
		return ""
	}
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s ×%d", k, kinds[k]))
	}
	return fmt.Sprintf("the latest light verification of %d capsule(s) ran full — reasons: %s", capsules, strings.Join(parts, ", "))
}
