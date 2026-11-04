package cli

// config_yaml_edit.go: the byte-preserving `.polyforge.yaml` editor
//. A step write
// (steps.<project>.<step-id>.models / .attrs.<name>) touches only the
// addressed line inside the `steps:` block, creating a missing section,
// project or step entry in block style and expanding a flow-style entry
// once. Every other byte — comments, ordering, the other sections —
// survives. The proposed bytes are validated through workspace.Load (the
// same strict loader the daemon and `pf doctor` use) before and under the
// writer flock; a step write first needs the running daemon's steps_attrs
// capability (stepsWriteCapability), checked before the file is read.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
	"gopkg.in/yaml.v3"
)

// isWorkspaceKey answers whether key is written to the workspace
// `.polyforge.yaml` (a step key) rather than cli.toml.
func isWorkspaceKey(key string) bool {
	_, ok := parseStepKey(key)
	return ok || strings.HasPrefix(key, "steps.")
}

// stepKey is one addressed step key: steps.<project>.<step>.models or
// steps.<project>.<step>.attrs.<attr>.
type stepKey struct {
	project, step, attr string
}

func parseStepKey(key string) (stepKey, bool) {
	parts := strings.Split(key, ".")
	if parts[0] != "steps" {
		return stepKey{}, false
	}
	for _, p := range parts {
		if p == "" {
			return stepKey{}, false
		}
	}
	switch {
	case len(parts) == 4 && parts[3] == "models":
		return stepKey{project: parts[1], step: parts[2]}, true
	case len(parts) == 5 && parts[3] == "attrs":
		return stepKey{project: parts[1], step: parts[2], attr: parts[4]}, true
	}
	return stepKey{}, false
}

func runWorkspaceWrite(ctx *Context, set bool, pos []string, exp configExpectation, wsFlag, cwdFlag string) error {
	key := pos[0]
	if set && len(pos) != 2 {
		return fmt.Errorf("config set %s: expected exactly one value", key)
	}
	if !set && len(pos) != 1 {
		return fmt.Errorf("config unset %s: expected no value", key)
	}
	sk, isStep := parseStepKey(key)
	if !isStep {
		return fmt.Errorf("config: unknown workspace key %q (steps.<project>.<step-id>.models or steps.<project>.<step-id>.attrs.<name>)", key)
	}
	ws, err := resolveWorkspaceTarget(ctx, key, wsFlag, cwdFlag)
	if err != nil {
		return err
	}
	// The capability gate comes before anything reads or touches the
	// file: an older daemon would refuse the schema the write produces.
	facts := readDaemonFacts(ctx)
	if ok, reason := stepsWriteCapability(facts); !ok {
		return &api.Error{Code: api.ErrCoreUnsupported, Detail: "config " + map[bool]string{true: "set", false: "unset"}[set] + " " + key + ": " + reason}
	}
	workflowsDir := facts.workflowsDir
	if sk.attr != "" && (sk.step == workspace.DefaultKey || sk.step == workspace.TaskKey) {
		return fmt.Errorf("config: %s", workspace.AttrsNotAllowedReason("steps."+sk.project+"."+sk.step, sk.step))
	}
	value := ""
	if set {
		if value, err = stepValue(sk, pos[1], workflowsDir); err != nil {
			return err
		}
	}
	edit := func(text string) string {
		return editStepsYAML(text, sk, value, set)
	}
	v, err := loadConfigView(ctx)
	if err != nil {
		return err
	}
	projects := v.eff.Mapping.ProjectsOf(ws)
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
	if err := validateWorkspaceYAML(edit(string(oldRaw)), workflowsDir, projects); err != nil {
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
	if err := checkExpectation(exp, sha256Hex(curRaw), curExists, map[string]any{}); err != nil {
		return err
	}
	final := edit(string(curRaw))
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

func resolveWorkspaceTarget(ctx *Context, key, wsFlag, cwdFlag string) (string, error) {
	if wsFlag != "" && cwdFlag != "" {
		return "", fmt.Errorf("config set %s: --workspace and --cwd are mutually exclusive", key)
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
			return "", fmt.Errorf("config set %s: %s is not a mapped [workspace] in cli.toml", key, abs)
		}
		return abs, nil
	}
	if cwdFlag == "" {
		return "", fmt.Errorf("config set %s: --workspace <path> or --cwd <dir> is required", key)
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
		return "", fmt.Errorf("config set %s: %s is not mapped to a workspace in cli.toml", key, abs)
	}
	return ws, nil
}

// stepValue renders the yaml text of one step-key value: a models list
// (the summarize.models parser: a JSON/flow list or a comma list) as a
// quoted flow list, an attr scalar by its declared type (an int attr as a
// bare integer, a string attr quoted). Whether the attr is declared, and
// the candidate format, are the strict loader's judgment afterwards.
func stepValue(sk stepKey, raw, workflowsDir string) (string, error) {
	if sk.attr == "" {
		items, err := parseModelList(raw)
		if err != nil {
			return "", err
		}
		return quotedList(items), nil
	}
	raw = strings.TrimSpace(raw)
	decls, _ := workflow.StepAttrs(workflowsDir)
	if decl, ok := decls[sk.step][sk.attr]; ok && decl.Type == workflow.AttrTypeString {
		if strings.ContainsAny(raw, "\r\n") {
			return "", fmt.Errorf("steps.%s.%s.attrs.%s: must be a single line", sk.project, sk.step, sk.attr)
		}
		return strconv.Quote(raw), nil
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return strconv.Itoa(n), nil
	}
	return strconv.Quote(raw), nil
}

func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = strconv.Quote(item)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func parseModelList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var items []string
		if err := yaml.Unmarshal([]byte(raw), &items); err != nil {
			return nil, fmt.Errorf("models list: %v", err)
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

// ---------------------------------------------------------------------------
// The steps block editor: line edits over an indentation walk. A "node" is
// a key line; its block is every following line indented deeper (blank and
// comment lines inside it included, trailing ones excluded).

func lineIndent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func isContentLine(line string) bool {
	t := strings.TrimSpace(line)
	return t != "" && !strings.HasPrefix(t, "#")
}

// nodeEnd answers the end (exclusive) of the block of the key line at i
// (i = -1 is the document root, whose block is every line).
func nodeEnd(lines []string, i int) int {
	if i < 0 {
		return len(lines)
	}
	last := i
	for j := i + 1; j < len(lines); j++ {
		if !isContentLine(lines[j]) {
			continue
		}
		if lineIndent(lines[j]) <= lineIndent(lines[i]) {
			break
		}
		last = j
	}
	return last + 1
}

// childIndent answers the indent of the block's direct children (the first
// content line's), or the parent's indent + 2 when it has none.
func childIndent(lines []string, i int) int {
	for j := i + 1; j < nodeEnd(lines, i); j++ {
		if isContentLine(lines[j]) {
			return lineIndent(lines[j])
		}
	}
	if i < 0 {
		return 0
	}
	return lineIndent(lines[i]) + 2
}

// findChild answers the line of the direct child key of the node at i,
// or -1.
func findChild(lines []string, i int, key string) int {
	ind := childIndent(lines, i)
	for j := i + 1; j < nodeEnd(lines, i); j++ {
		if !isContentLine(lines[j]) || lineIndent(lines[j]) != ind {
			continue
		}
		if k, _, ok := splitKeyLine(lines[j]); ok && k == key {
			return j
		}
	}
	return -1
}

// splitKeyLine splits `<indent>key: value  # comment` into the key and the
// inline value (comment stripped).
func splitKeyLine(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(line)
	c := strings.Index(t, ":")
	if c <= 0 || (c+1 < len(t) && t[c+1] != ' ') {
		return "", "", false
	}
	key, value = t[:c], strings.TrimSpace(t[c+1:])
	if strings.HasPrefix(value, "#") {
		value = ""
	} else if h := strings.Index(value, " #"); h >= 0 && !strings.ContainsAny(value[:h], "\"'") {
		value = strings.TrimSpace(value[:h])
	}
	return strings.Trim(key, "\"'"), value, true
}

// expandFlow rewrites a key line whose inline value is a flow mapping
// (`key: { a: 1 }`) as a block, preserving every value it declares; any
// other line is left alone.
func expandFlow(lines []string, i int) []string {
	_, value, _ := splitKeyLine(lines[i])
	if !strings.HasPrefix(value, "{") {
		return lines
	}
	var n yaml.Node
	if yaml.Unmarshal([]byte(value), &n) != nil || len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
		return lines
	}
	ind := strings.Repeat(" ", lineIndent(lines[i]))
	head := ind + strings.SplitN(strings.TrimSpace(lines[i]), ":", 2)[0] + ":"
	block := []string{head}
	block = append(block, blockLines(n.Content[0], ind+"  ")...)
	return append(append(append([]string{}, lines[:i]...), block...), lines[i+1:]...)
}

// blockLines renders a mapping node in block style: nested mappings as
// blocks, sequences and scalars inline (flow).
func blockLines(m *yaml.Node, ind string) []string {
	var out []string
	for k := 0; k+1 < len(m.Content); k += 2 {
		key, val := m.Content[k].Value, m.Content[k+1]
		if val.Kind == yaml.MappingNode && len(val.Content) > 0 {
			out = append(out, ind+key+":")
			out = append(out, blockLines(val, ind+"  ")...)
			continue
		}
		val.Style = yaml.FlowStyle
		b, _ := yaml.Marshal(val)
		out = append(out, ind+key+": "+strings.TrimSpace(string(b)))
	}
	return out
}

// ensureChild answers the line of the child key of the node at i, inserting
// `key:` at the end of its block when absent and expanding it when it is a
// flow mapping.
func ensureChild(lines []string, i int, key string) ([]string, int) {
	if j := findChild(lines, i, key); j >= 0 {
		return expandFlow(lines, j), j
	}
	at := nodeEnd(lines, i)
	line := strings.Repeat(" ", childIndent(lines, i)) + key + ":"
	return append(append(append([]string{}, lines[:at]...), line), lines[at:]...), at
}

// setLeaf sets `key: value` under the node at i: the existing line (and
// any block below it) replaced in place, else a new line — first in the
// block when first is set, else last.
func setLeaf(lines []string, i int, key, value string, first bool) []string {
	line := func() string { return strings.Repeat(" ", childIndent(lines, i)) + key + ": " + value }
	if j := findChild(lines, i, key); j >= 0 {
		ind := strings.Repeat(" ", lineIndent(lines[j]))
		return append(append(append([]string{}, lines[:j]...), ind+key+": "+value), lines[nodeEnd(lines, j):]...)
	}
	at := nodeEnd(lines, i)
	if first {
		at = i + 1
	}
	return append(append(append([]string{}, lines[:at]...), line()), lines[at:]...)
}

// dropNode removes the key line at j and its block.
func dropNode(lines []string, j int) []string {
	return append(append([]string{}, lines[:j]...), lines[nodeEnd(lines, j):]...)
}

// hasChildren answers whether the node at i declares any child.
func hasChildren(lines []string, i int) bool {
	for j := i + 1; j < nodeEnd(lines, i); j++ {
		if isContentLine(lines[j]) {
			return true
		}
	}
	return false
}

// editStepsYAML applies one step-key write: set replaces (or inserts) the
// addressed models/attr line, creating the steps section, the project and
// the step entry in block style when absent; unset removes the line and
// then every entry the removal left empty (an empty attrs map, a step entry
// that declares nothing, a project with no entry, an empty steps section),
// so a cleared chain falls back one level while an attrs-carrying entry
// keeps its attrs.
func editStepsYAML(text string, sk stepKey, value string, set bool) string {
	lines := strings.Split(text, "\n")
	trailingNL := strings.HasSuffix(text, "\n")
	if trailingNL {
		lines = lines[:len(lines)-1]
	}
	join := func(ls []string) string {
		out := strings.Join(ls, "\n")
		if trailingNL || (set && text == "") {
			out += "\n"
		}
		return out
	}
	if set {
		if text == "" {
			lines = nil
		}
		var s, p, st int
		lines, s = ensureChild(lines, -1, "steps")
		lines, p = ensureChild(lines, s, sk.project)
		lines, st = ensureChild(lines, p, sk.step)
		if sk.attr == "" {
			return join(setLeaf(lines, st, "models", value, true))
		}
		var a int
		lines, a = ensureChild(lines, st, "attrs")
		return join(setLeaf(lines, a, sk.attr, value, false))
	}
	s := findChild(lines, -1, "steps")
	if s < 0 {
		return text
	}
	lines = expandFlow(lines, s)
	p := findChild(lines, s, sk.project)
	if p < 0 {
		return text
	}
	lines = expandFlow(lines, p)
	st := findChild(lines, p, sk.step)
	if st < 0 {
		return text
	}
	lines = expandFlow(lines, st)
	if sk.attr == "" {
		m := findChild(lines, st, "models")
		if m < 0 {
			return text
		}
		lines = dropNode(lines, m)
	} else {
		a := findChild(lines, st, "attrs")
		if a < 0 {
			return text
		}
		lines = expandFlow(lines, a)
		n := findChild(lines, a, sk.attr)
		if n < 0 {
			return text
		}
		lines = dropNode(lines, n)
		if !hasChildren(lines, a) {
			lines = dropNode(lines, a)
		}
	}
	if !hasChildren(lines, st) {
		lines = dropNode(lines, st)
		if !hasChildren(lines, p) {
			lines = dropNode(lines, p)
			if !hasChildren(lines, s) {
				lines = dropNode(lines, s)
			}
		}
	}
	return join(lines)
}
