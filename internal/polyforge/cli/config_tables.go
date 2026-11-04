package cli

// config_tables.go: the two machine-level table families cli.toml carries
// for the workflow lanes the one [summarize] table and
// the path-keyed [memory_policy."<path>".<entry>] family. The parser
// (config.go) collects their raw rows with file lines; this file validates
// the values. Every refusal names the file, the line and the key.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// summarizeTableKeys is the [summarize] table's closed vocabulary.
var summarizeTableKeys = []string{"models", "language", "interval_sec"}

// policyEntryKeys is a [memory_policy."<path>".<entry>] table's closed
// vocabulary.
var policyEntryKeys = []string{"record", "skip"}

// TableKeyError is the typed refusal of one [summarize] or
// [memory_policy...] row: the file, the line, the table and the key.
type TableKeyError struct {
	File   string
	Line   int
	Table  string
	Key    string
	Reason string
}

func (e *TableKeyError) Error() string {
	where := e.Table
	if e.Key != "" {
		where += " " + e.Key
	}
	return fmt.Sprintf("config %s: line %d: %s: %s", e.File, e.Line, where, e.Reason)
}

// parseSummarizeTable validates the [summarize] table (nil = absent):
// `models` mandatory, a non-empty list of unique `<harness>:<model>`
// candidates (harness.Normalize, the step-entry bar); `language` optional
// free text, trimmed, non-empty and single-line; `interval_sec` optional,
// an unquoted integer in MinSummarizeIntervalSec..MaxSummarizeIntervalSec.
func parseSummarizeTable(file string, t *tableBlock) (*workspace.SummarizeSettings, error) {
	if t == nil {
		return nil, nil
	}
	const table = "[summarize]"
	s := &workspace.SummarizeSettings{}
	seen := map[string]bool{}
	hasModels := false
	for _, kv := range t.rows {
		refuse := func(reason string) error {
			return &TableKeyError{File: file, Line: kv.line, Table: table, Key: kv.key, Reason: reason}
		}
		if seen[kv.key] {
			return nil, refuse("duplicate key")
		}
		seen[kv.key] = true
		switch kv.key {
		case "models":
			list, reason := parseModelsValue(kv.val, kv.line)
			if reason != "" {
				return nil, refuse(reason)
			}
			s.Models, hasModels = list, true
		case "language":
			lang, reason := parseLanguageValue(kv.val, kv.line)
			if reason != "" {
				return nil, refuse(reason)
			}
			s.Language = lang
		case "interval_sec":
			n, reason := parseIntervalValue(kv.val)
			if reason != "" {
				return nil, refuse(reason)
			}
			s.IntervalSec = n
		default:
			if coreFlatKeys[kv.key] {
				return nil, refuse("a top-level key: move it above the [summarize] header (every key below the header belongs to the table)")
			}
			return nil, refuse("unknown key (the [summarize] table carries only " + strings.Join(summarizeTableKeys, ", ") + ")")
		}
	}
	if !hasModels {
		return nil, &TableKeyError{File: file, Line: t.line, Table: table, Reason: "`models` is mandatory (the table's keys are " + strings.Join(summarizeTableKeys, ", ") + "; remove the table to turn the summarize lane off)"}
	}
	return s, nil
}

// parseModelsValue validates one summarize models array value.
func parseModelsValue(val string, ln int) ([]string, string) {
	items, err := parseQuotedArray(val, ln)
	if err != nil {
		return nil, "must be a non-empty array of quoted `<harness>:<model>` candidates: " + err.Error()
	}
	if len(items) == 0 {
		return nil, "is empty — name at least one `<harness>:<model>` candidate, or remove the table"
	}
	return normalizeSummarizeModels(items)
}

// normalizeSummarizeModels answers the canonical candidate list or the
// refusal: every item a `<harness>:<model>` candidate, no repeat.
func normalizeSummarizeModels(items []string) ([]string, string) {
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		c, err := harness.Normalize(item)
		if err != nil {
			return nil, fmt.Sprintf("item %d: %v", i, err)
		}
		if seen[c] {
			return nil, fmt.Sprintf("item %d: repeats %q", i, c)
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, ""
}

// parseLanguageValue validates one summarize language value: a quoted
// string, trimmed, non-empty and single-line; free text otherwise.
func parseLanguageValue(val string, ln int) (string, string) {
	v, err := parseQuotedValue(val, ln)
	if err != nil {
		return "", "must be a quoted single-line string (e.g. \"English\")"
	}
	return checkLanguage(v)
}

func checkLanguage(v string) (string, string) {
	lang := strings.TrimSpace(v)
	if lang == "" {
		return "", "is empty — name a language, or remove the key (absent = " + workspace.DefaultSummarizeLanguage + ")"
	}
	if strings.ContainsAny(lang, "\r\n") {
		return "", "must be a single line"
	}
	return lang, ""
}

// parseIntervalValue validates one summarize interval_sec value: an
// unquoted integer inside the documented bounds, never clamped.
func parseIntervalValue(val string) (int, string) {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || n < workspace.MinSummarizeIntervalSec || n > workspace.MaxSummarizeIntervalSec {
		return 0, fmt.Sprintf("must be an unquoted integer in %d-%d (seconds; absent = %d)",
			workspace.MinSummarizeIntervalSec, workspace.MaxSummarizeIntervalSec, workspace.DefaultSummarizeIntervalSec)
	}
	return n, ""
}

// parsePolicyTables validates the [memory_policy."<path>".<entry>] family
// against the built mapping: the path must be a mapped workspace (spelled
// as its [workspace] section key, or as that key expanded), the entry
// `default` or a project of that path, and the table declares `record`
// and/or `skip` only — arrays of unique non-empty quoted strings. sections
// are the parsed [workspace] sections in file order (the plan keeps that
// order, so sections[i] is plan.Workspaces()[i]).
func parsePolicyTables(file string, tables []tableBlock, sections []workspaceSection, plan *mapping.Plan) (map[string]map[string]workspace.PolicyEntry, error) {
	if len(tables) == 0 {
		return nil, nil
	}
	mapped := plan.Workspaces()
	resolve := map[string]string{}
	for i, ws := range mapped {
		resolve[ws] = ws
		if i < len(sections) {
			resolve[sections[i].key] = ws
		}
	}
	out := map[string]map[string]workspace.PolicyEntry{}
	for _, t := range tables {
		table := fmt.Sprintf("[memory_policy.%q.%s]", t.path, t.entry)
		ws, ok := resolve[t.path]
		if !ok {
			return nil, &TableKeyError{File: file, Line: t.line, Table: table, Reason: fmt.Sprintf("%s is not a mapped workspace — map it with a [workspace.%q] section first (mapped: %s)", t.path, t.path, strings.Join(mapped, ", "))}
		}
		projects := plan.ProjectsOf(ws)
		if t.entry != workspace.DefaultKey && !containsString(projects, t.entry) {
			return nil, &TableKeyError{File: file, Line: t.line, Table: table, Reason: fmt.Sprintf("entry %q is neither %q nor a project of %s (it maps: %s)", t.entry, workspace.DefaultKey, ws, strings.Join(projects, ", "))}
		}
		entry, err := parsePolicyEntry(file, table, t)
		if err != nil {
			return nil, err
		}
		if _, dup := out[ws][t.entry]; dup {
			return nil, &TableKeyError{File: file, Line: t.line, Table: table, Reason: "duplicate entry (the same workspace is spelled twice)"}
		}
		if out[ws] == nil {
			out[ws] = map[string]workspace.PolicyEntry{}
		}
		out[ws][t.entry] = entry
	}
	return out, nil
}

// parsePolicyEntry validates one entry table's rows.
func parsePolicyEntry(file, table string, t tableBlock) (workspace.PolicyEntry, error) {
	var e workspace.PolicyEntry
	seen := map[string]bool{}
	for _, kv := range t.rows {
		refuse := func(reason string) error {
			return &TableKeyError{File: file, Line: kv.line, Table: table, Key: kv.key, Reason: reason}
		}
		if seen[kv.key] {
			return e, refuse("duplicate key")
		}
		seen[kv.key] = true
		if kv.key != "record" && kv.key != "skip" {
			if coreFlatKeys[kv.key] {
				return e, refuse("a top-level key: move it above the first table header (every key below a header belongs to that table)")
			}
			return e, refuse("unknown key (an entry carries only " + strings.Join(policyEntryKeys, ", ") + ")")
		}
		items, err := parseQuotedArray(kv.val, kv.line)
		if err != nil {
			return e, refuse(err.Error())
		}
		list, reason := checkPolicyList(items)
		if reason != "" {
			return e, refuse(reason)
		}
		if kv.key == "record" {
			e.Record = list
		} else {
			e.Skip = list
		}
	}
	if len(seen) == 0 {
		return e, &TableKeyError{File: file, Line: t.line, Table: table, Reason: "the entry declares no list — declare `record` and/or `skip`, or remove the table"}
	}
	return e, nil
}

// checkPolicyList refuses an empty or repeated item (trimmed, as the
// wrap prompt reads them).
func checkPolicyList(items []string) ([]string, string) {
	list := make([]string, 0, len(items))
	for i, item := range items {
		s := strings.TrimSpace(item)
		switch {
		case s == "":
			return nil, fmt.Sprintf("item %d is empty", i)
		case containsString(list, s):
			return nil, fmt.Sprintf("item %d repeats %q", i, s)
		}
		list = append(list, s)
	}
	return list, ""
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
