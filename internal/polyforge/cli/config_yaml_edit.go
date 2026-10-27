package cli

// config_yaml_edit.go: the byte-preserving `.polyforge.yaml` summarize
// editor (workbench#22). It touches only the target child line inside the
// root `summarize:` block; every other byte — comments, ordering, the
// `steps:` and `memory_policy:` sections — survives. The proposed bytes are
// validated through workspace.Load (the same strict loader the daemon and
// `pf doctor` use) before and under the writer flock.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
	"gopkg.in/yaml.v3"
)

func isSummarizeKey(key string) bool {
	switch key {
	case "summarize.models", "summarize.language", "summarize.interval_sec":
		return true
	}
	return false
}

func runWorkspaceWrite(ctx *Context, set bool, pos []string, exp configExpectation, wsFlag, cwdFlag string) error {
	key := pos[0]
	if set && len(pos) != 2 {
		return fmt.Errorf("config set %s: expected exactly one value", key)
	}
	if !set && len(pos) != 1 {
		return fmt.Errorf("config unset %s: expected no value", key)
	}
	ws, err := resolveWorkspaceTarget(ctx, wsFlag, cwdFlag)
	if err != nil {
		return err
	}
	value := ""
	if set {
		if value, err = summarizeValue(key, pos[1]); err != nil {
			return err
		}
	}
	v, err := loadConfigView(ctx)
	if err != nil {
		return err
	}
	projects := v.eff.Mapping.ProjectsOf(ws)
	workflowsDir, _, _ := workspaceCandidateScope(ctx)
	path := workspace.PathFor(ws)

	// Preflight the proposed bytes before taking the writer lock. An absent
	// yaml may be created only when the caller says it is absent.
	oldRaw, rerr := os.ReadFile(path)
	if rerr != nil && !os.IsNotExist(rerr) {
		return fmt.Errorf("workspace %s: %w", path, rerr)
	}
	oldExists := rerr == nil
	if !oldExists && !set {
		// Unsetting a key in a yaml that does not exist is a no-op: there is
		// nothing to remove and no reason to create an empty file. The target
		// workspace was already resolved, so an unmapped path still errors.
		return nil
	}
	if !oldExists && set && !expectsAbsent(exp) {
		return &api.Error{Code: api.ErrCoreConflict, Detail: "the workspace yaml is absent; creating it needs --expect-hash absent (or an --expect-state whose file_sha256 is null)"}
	}
	if err := validateWorkspaceYAML(editSummarizeYAML(string(oldRaw), key, value, set), workflowsDir, projects); err != nil {
		return err
	}

	lock, err := lockConfigFile(path)
	if err != nil {
		return fmt.Errorf("workspace: lock %s: %w", path, err)
	}
	defer unlockConfigFile(lock)
	curRaw, err := os.ReadFile(path)
	curExists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("workspace %s: %w", path, err)
	}
	if !curExists && set && !expectsAbsent(exp) {
		return &api.Error{Code: api.ErrCoreConflict, Detail: "the workspace yaml is absent; creating it needs --expect-hash absent (or an --expect-state whose file_sha256 is null)"}
	}
	curValues := map[string]any{}
	if curExists {
		if curCfg, lerr := loadWorkspaceBytes(string(curRaw), workflowsDir, projects); lerr == nil {
			curValues = workspaceSnapshotValues(curCfg)
		}
	}
	if err := checkExpectation(exp, sha256Hex(curRaw), curExists, curValues); err != nil {
		return err
	}
	final := editSummarizeYAML(string(curRaw), key, value, set)
	if err := validateWorkspaceYAML(final, workflowsDir, projects); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if m, ok := atomicfile.ReadMode(path); ok {
		perm = m
	}
	if err := atomicfile.Write(path, []byte(final), perm); err != nil {
		return fmt.Errorf("workspace: write %s: %w", path, err)
	}
	return nil
}

func resolveWorkspaceTarget(ctx *Context, wsFlag, cwdFlag string) (string, error) {
	if wsFlag != "" && cwdFlag != "" {
		return "", fmt.Errorf("config set summarize.*: --workspace and --cwd are mutually exclusive")
	}
	if wsFlag != "" {
		abs, err := filepath.Abs(wsFlag)
		if err != nil {
			return "", err
		}
		v, err := loadConfigView(ctx)
		if err != nil {
			return "", err
		}
		if v.eff.Mapping.ProjectsOf(abs) == nil {
			return "", fmt.Errorf("config set summarize.*: %s is not a mapped [workspace] in cli.toml", abs)
		}
		return abs, nil
	}
	if cwdFlag == "" {
		return "", fmt.Errorf("config set summarize.*: --workspace <path> or --cwd <dir> is required")
	}
	abs, err := filepath.Abs(cwdFlag)
	if err != nil {
		return "", err
	}
	v, err := loadConfigView(ctx)
	if err != nil {
		return "", err
	}
	ws := v.eff.Mapping.MatchWorkspace(abs)
	if ws == "" {
		return "", fmt.Errorf("config set summarize.*: %s is not mapped to a workspace in cli.toml", abs)
	}
	return ws, nil
}

func summarizeValue(key, raw string) (string, error) {
	switch key {
	case "summarize.models":
		items, err := parseModelList(raw)
		if err != nil {
			return "", err
		}
		quoted := make([]string, len(items))
		for i, item := range items {
			quoted[i] = strconv.Quote(item)
		}
		return "[" + strings.Join(quoted, ", ") + "]", nil
	case "summarize.language":
		if strings.ContainsAny(raw, "\r\n") {
			return "", fmt.Errorf("summarize.language: must be a single line")
		}
		return strconv.Quote(strings.TrimSpace(raw)), nil
	case "summarize.interval_sec":
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("summarize.interval_sec: must be an integer in %d-%d (seconds)", workspace.MinSummarizeIntervalSec, workspace.MaxSummarizeIntervalSec)
		}
		return strconv.Itoa(n), nil
	}
	return "", fmt.Errorf("config: unknown summarize key %q", key)
}

func parseModelList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var items []string
		if err := yaml.Unmarshal([]byte(raw), &items); err != nil {
			return nil, fmt.Errorf("summarize.models: %v", err)
		}
		return items, nil
	}
	if raw == "" {
		return []string{}, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// validateWorkspaceYAML runs the one strict loader over proposed bytes by
// writing them to a throwaway workspace root (the loader reads a fixed file
// name under its root).
func validateWorkspaceYAML(text, workflowsDir string, projects []string) error {
	_, err := loadWorkspaceBytes(text, workflowsDir, projects)
	return err
}

// loadWorkspaceBytes runs the one strict loader over proposed bytes and
// returns the parsed config (a temp root is needed because the loader reads a
// fixed file name under its root).
func loadWorkspaceBytes(text, workflowsDir string, projects []string) (*workspace.Config, error) {
	dir, err := os.MkdirTemp("", "polyforge-ws-probe-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, workspace.FileName), []byte(text), 0o644); err != nil {
		return nil, err
	}
	return workspace.Load(dir, workflowsDir, projects)
}

// workspaceSnapshotValues renders the yaml's effective summarize values for the
// shared CAS comparison, keyed by the same `summarize.*` names the read (and
// so the panel's snapshot) uses.
func workspaceSnapshotValues(cfg *workspace.Config) map[string]any {
	out := map[string]any{}
	if cfg == nil {
		return out
	}
	out["summarize.models"] = workspace.SummarizeModels(cfg)
	out["summarize.language"] = workspace.SummarizeLanguage(cfg)
	out["summarize.interval_sec"] = workspace.SummarizeIntervalSec(cfg)
	return out
}

// editSummarizeYAML rewrites only the requested summarize child (or removes
// the whole entry for models). The line structure outside the summarize block
// is preserved verbatim.
func editSummarizeYAML(text, key, value string, set bool) string {
	child := strings.TrimPrefix(key, "summarize.")
	lines := strings.Split(text, "\n")
	idx, flow := -1, ""
	for i, line := range lines {
		if strings.HasPrefix(line, "summarize:") {
			idx = i
			flow = strings.TrimSpace(strings.TrimPrefix(line, "summarize:"))
			break
		}
	}
	if idx < 0 {
		if !set {
			return text
		}
		base := text
		if base != "" && !strings.HasSuffix(base, "\n") {
			base += "\n"
		}
		if base != "" && !strings.HasSuffix(base, "\n\n") {
			base += "\n"
		}
		return base + "summarize:\n  " + child + ": " + value + "\n"
	}
	if flow != "" && flow != "{}" {
		// A flow-style entry is rewritten as a block, preserving the values
		// it declares and then applying the requested change.
		var m map[string]any
		_ = yaml.Unmarshal([]byte(flow), &m)
		block := []string{"summarize:"}
		for _, name := range []string{"models", "language", "interval_sec"} {
			if v, ok := m[name]; ok {
				block = append(block, "  "+name+": "+renderFlowValue(name, v))
			}
		}
		lines = append(append(append([]string{}, lines[:idx]...), block...), lines[idx+1:]...)
		return editSummarizeYAML(strings.Join(lines, "\n"), key, value, set)
	}
	end := blockEnd(lines, idx)
	insertAt := idx + 1
	for i := idx + 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if t != "" && !strings.HasPrefix(t, "#") {
			insertAt = i + 1
		}
	}
	target := -1
	indent := "  "
	for i := idx + 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, child+":") {
			target = i
			indent = lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			break
		}
	}
	if target >= 0 {
		if !set {
			if child == "models" {
				return removeLineRange(lines, idx, end)
			}
			lines = append(lines[:target], lines[target+1:]...)
			return removeEmptySummarize(strings.Join(lines, "\n"))
		}
		lines[target] = indent + child + ": " + value
		return strings.Join(lines, "\n")
	}
	if !set {
		return text
	}
	if child == "models" {
		insertAt = idx + 1
	}
	newLine := indent + child + ": " + value
	lines = append(lines[:insertAt], append([]string{newLine}, lines[insertAt:]...)...)
	return strings.Join(lines, "\n")
}

func blockEnd(lines []string, idx int) int {
	end := idx + 1
	for end < len(lines) {
		line := lines[end]
		if strings.TrimSpace(line) == "" {
			end++
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
		end++
	}
	for end > idx+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

func removeLineRange(lines []string, start, end int) string {
	return strings.Join(append(append([]string{}, lines[:start]...), lines[end:]...), "\n")
}

func removeEmptySummarize(text string) string {
	lines := strings.Split(text, "\n")
	idx := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "summarize:") {
			idx = i
			break
		}
	}
	if idx < 0 {
		return text
	}
	end := blockEnd(lines, idx)
	for i := idx + 1; i < end; i++ {
		t := strings.TrimSpace(lines[i])
		if t != "" && !strings.HasPrefix(t, "#") {
			return text
		}
	}
	return removeLineRange(lines, idx, end)
}

func renderFlowValue(name string, v any) string {
	switch name {
	case "models":
		list, ok := v.([]any)
		if !ok {
			return "[]"
		}
		parts := make([]string, len(list))
		for i, item := range list {
			parts[i] = strconv.Quote(fmt.Sprint(item))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case "language":
		return strconv.Quote(fmt.Sprint(v))
	default:
		return fmt.Sprint(v)
	}
}
