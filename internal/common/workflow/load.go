package workflow

// load.go: the one lane loader (docs/reference/workflow-contract.md).
// Every reader of a lane definition — the driver and its spawn adapter,
// the claim check, `pf file create`, the model-candidate composition,
// the step id enumeration, `pf workflows validate` and `pf doctor` —
// calls Load and nothing else: there is no second parse of
// a workflow.yaml. Load decodes strictly (every unknown key at every level
// and every duplicate key is a defect), judges the lane against the
// registry (registry.go), resolves and parses every step's definition
// file and skill and renders every definition with its vars and
// fragments, so a placeholder that does not resolve fails the load instead
// of a dispatch. It collects every defect before it answers.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"gopkg.in/yaml.v3"
)

// WorkflowFile is a lane's definition file: one YAML mapping of the lane
// keys (registry.go). The lane's prose is its README.md, which the daemon
// never reads.
const WorkflowFile = "workflow.yaml"

// Contract4File is the lane file of the earlier releases: a
// Markdown file whose YAML frontmatter held the lane keys. The loader
// refuses it with its rewrite (Contract4Hint) — WFC-101 when it is the
// lane's only lane file, reading nothing of it but its `contract` for the
// report (a capsule snapshot a drain left in flight parks blocked, kind
// contract), WFC-110 beside a workflow.yaml.
const Contract4File = "workflow.md"

// Contract4Hint is the rewrite of a lane that carries a workflow.md.
const Contract4Hint = "workflow contract 1 reads workflow.yaml: move workflow.md's frontmatter (the YAML between its --- fences) into workflow.yaml with `contract: 1`, its Markdown body into README.md, and delete workflow.md"

// Lane is one loaded lane.
type Lane struct {
	// Dir is the lane directory Load read.
	Dir string
	// Shared is the shared tree its definitions and fragments fall back
	// on: the `_shared` beside the lane, or for a user lane the built-in
	// one (LoadUser).
	Shared      string
	Contract    int
	Name        string
	Description string
	// MaxRewinds is the lane-wide automatic rewind budget (0 when the
	// lane declares no on_fail, which is also when it may not declare one).
	MaxRewinds int
	// UncountedRewinds is the lane's `uncounted_rewinds`: the lane-declared
	// class of gate-FAIL rewinds charged to no budget — a FAIL whose
	// one-line hand-off reason contains ReasonContains — counted on its
	// own toward Max (nil when the lane declares none).
	UncountedRewinds *UncountedRewinds
	// Exclusive is the lane's `exclusive: true`: the daemon's proxy
	// forwards a work-item create on the lane exclusive, so aihub refuses
	// it while another work item of its project on the lane is unfinished.
	Exclusive bool
	// Acceptance is the lane's `acceptance` (AcceptanceRequired or
	// AcceptanceNone; "" when the lane does not declare it, which is
	// required): RequiresAcceptance answers it.
	Acceptance string
	// ChildrenBranch is the lane's `children_branch`: the branch-name
	// template the daemon checks a sub-ticket of a work item whose parent
	// runs on this lane out on (`{seq}` = the parent's seq, `{project}` =
	// the parent's project name; "" when the lane declares none, which
	// leaves such a sub-ticket's claim untouched).
	ChildrenBranch string
	// Epic is the lane's `epic: true` (polyforge#117): the lane's own
	// work items are epics — each checks the children branch of its OWN
	// seq out (children_branch, which the lane must then declare) and
	// merges into the origin's default branch (the runtime fixes the
	// checkout branch into the capsule and snapshots the item's
	// children).
	Epic bool
	// ResumeReset is the lane's `resume_reset`: the step id an
	// owner-resumed epic's changed-children marker resets the lane to
	// — required when the lane declares epic, refused without it, and
	// it must name a step of this lane (the runtime writes it into the
	// marker, the driver rewinds to it; polyforge#117 r4).
	ResumeReset string
	Defaults    *Defaults
	Steps       []Step
}

// UncountedRewinds is the lane's declared uncounted rewind class
// (`uncounted_rewinds`): a gate FAIL whose one-line hand-off reason
// carries ReasonContains falls in it. Its rewinds are charged to no
// budget and counted on their own, toward Max.
type UncountedRewinds struct {
	// ReasonContains is the substring a gate FAIL's one-line reason
	// carries when its rewind falls in the class (a stable prefix of the
	// failing step's script summary; the rest of the line varies).
	ReasonContains string
	// Max is the class's own budget: once the class's uncounted rewinds
	// reach it, a matching FAIL without the owner's word is refused, so
	// the gate asks the owner first — exactly like a spent max_rewinds.
	// The loader fills DefaultUncountedRewinds when the lane declares
	// no max; 0 is a valid budget: every matching FAIL asks the owner.
	Max int
}

// Defaults carries the workflow-level fallback model candidates and the
// tier table a step's `model_tier` names (`defaults.tiers.<name>`).
type Defaults struct {
	ModelCandidates []string
	Tiers           map[string][]string
}

// Resources is a step's declared resources (`resources`): the heavy-run
// admission (AdmissionNone or AdmissionHeavy) and the locks the dispatch
// holds (LockMerge, at most once).
type Resources struct {
	Admission string
	Locks     []string
}

// Holds reports whether the resources declare lock.
func (r *Resources) Holds(lock string) bool {
	if r == nil {
		return false
	}
	return contains(r.Locks, lock)
}

// Heavy reports whether the resources ask the heavy-run admission.
func (r *Resources) Heavy() bool { return r != nil && r.Admission == AdmissionHeavy }

// Step is one loaded step entry: an agent step (workflow contract 4).
type Step struct {
	ID   string
	Gate bool
	// OnFailTarget is the gate's on_fail target ("" = none).
	OnFailTarget    string
	WallMinutes     int
	ModelCandidates []string
	Vars            map[string]string
	// Waivable is `waivable` (a gate's; default false): the gate may hand
	// off waive on the owner's word.
	Waivable bool
	// Skippable is `skippable` (default false): a project may switch the
	// step off (`steps.<project>.<step>.skip` in its workspace yaml).
	Skippable bool
	// RewindOnly is `rewind_only` (default false): the step runs only
	// after a rewind covers it — the first round records it skipped
	// without a dispatch.
	RewindOnly bool
	// HumanMergeable is `human_mergeable` (default false): a project may
	// declare the step's work done by a person
	// (`steps.<project>.<step>.by: human` in its workspace yaml,
	// polyforge#36).
	HumanMergeable bool
	// ToolMinutes is `budget.tool_minutes` (0 = none declared).
	ToolMinutes int
	// ModelTier is `model_tier` ("" = none): a tier of the lane's
	// defaults.tiers, whose list is the step's candidate list.
	ModelTier string
	// Skill is `skill` ("" = none): the skill file rendered into the
	// prompt (SkillPath).
	Skill string
	// Resources is `resources` (nil = none declared).
	Resources *Resources
	// Hub is `hub.verbs` (nil = none): the `pf step hub` verbs the step
	// may call, in declaration order; HubAttrs is `hub.attrs` (nil =
	// none): the top-level attrs keys the step may write with `pf step hub
	// attrs`.
	Hub      []string
	HubAttrs []string
	// WrapAttrs is `wrap_attrs` (nil = none): the wrap attrs the step's
	// round registers (`pf step hub wrap-attrs <name>`) before it may hand
	// off done — the daemon refuses that hand-off otherwise.
	WrapAttrs []string
	// Params is `params` (nil = none): the step's declared parameters,
	// each name to its lane-default value (a single-line string). The
	// final value is composed at every dispatch (lane default, the
	// project's steps.<project>.<step>.params, the work item's pf
	// override) and fills the definition's {{<name>}} placeholders and the
	// step's PF_PARAM_<NAME> environment variable; a parameter the
	// definition never uses is allowed (a script may read it).
	Params map[string]string

	// varLines are the workflow.yaml lines of the vars' keys (a
	// vars_files var's: its vars_files key) and path the
	// step's YAML path (a definition's var defect points back at them);
	// fileVars marks the vars read through vars_files; nodes are the
	// step's known keys' value nodes.
	varLines map[string]int
	fileVars map[string]bool
	path     string
	nodes    map[string]*yaml.Node
}

// Step answers the step with id (nil when absent).
func (l *Lane) Step(id string) *Step {
	if l == nil {
		return nil
	}
	for i := range l.Steps {
		if l.Steps[i].ID == id {
			return &l.Steps[i]
		}
	}
	return nil
}

// idPattern is the grammar of a lane name and of a step id.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// loader carries one Load's state: the lane dir and every defect found.
type loader struct {
	dir string
	// shared is the shared tree the lane's definitions and fragments fall
	// back on; user marks a workspace's user lane (LoadUser).
	shared string
	user   bool
	errs   []ContractError
}

// at records one defect in file at node n (n nil: no line), off being
// the file line the node's YAML line 1 sits on minus one.
func (l *loader) at(code, file string, off int, n *yaml.Node, path, format string, args ...any) {
	line := 0
	if n != nil && n.Line > 0 {
		line = n.Line + off
	}
	l.errs = append(l.errs, ContractError{Code: code, File: file, Line: line, Path: path, Msg: fmt.Sprintf(format, args...)})
}

// Load reads the lane at dir and judges it against the contract. It
// answers the lane as far as it could be read (nil only when the
// workflow.yaml cannot be read at all) and every defect, in
// file and line order; a lane with any defect must not run. Callers turn
// the defects into an error with AsLoadError.
func Load(dir string) (*Lane, []ContractError) {
	return load(&loader{dir: dir, shared: SiblingShared(dir)})
}

// LoadUser reads a workspace's user lane at dir (docs/reference/
// workflow-contract.md, "User lanes"): its definitions and fragments
// fall back on the built-in shared tree at shared (a
// workspace has no shared tree of its own), and it is judged by the user
// lane rules on top of every other. Whether a built-in lane carries its
// name (WFC-122) is the lane set's judgement (LaneSet.Lanes), not the
// lane's.
func LoadUser(dir, shared string) (*Lane, []ContractError) {
	return load(&loader{dir: dir, shared: shared, user: true})
}

func load(l *loader) (*Lane, []ContractError) {
	lane := l.load()
	sortErrors(l.errs)
	return lane, l.errs
}

// LoadStrict is Load answering the defects as one *LoadError.
func LoadStrict(dir string) (*Lane, error) {
	lane, errs := Load(dir)
	if err := AsLoadError(dir, errs); err != nil {
		return nil, err
	}
	return lane, nil
}

func (l *loader) load() *Lane {
	file := filepath.Join(l.dir, WorkflowFile)
	old := filepath.Join(l.dir, Contract4File)
	_, oldErr := os.Stat(old)
	raw, err := os.ReadFile(file)
	switch {
	case err != nil && oldErr == nil:
		return l.contract4(old)
	case err != nil:
		l.at(CodeLaneFile, file, 0, nil, "", "%s unreadable: %v", WorkflowFile, err)
		return nil
	case oldErr == nil:
		l.at(CodeLaneFile, old, 0, nil, "", "the lane carries %s, the lane file of the earlier releases, beside its %s — %s", Contract4File, WorkflowFile, Contract4Hint)
	}
	root := l.laneFile(file, raw)
	if root == nil {
		return nil
	}
	const off = 0 // the YAML's line 1 is the file's
	lane := &Lane{Dir: l.dir, Shared: l.shared}
	keys := l.mapping(file, off, root, "", laneKeys, removedKeys["lane"])

	if n, ok := keys["contract"]; !ok {
		l.at(CodeContractMissing, file, off, root, "contract", "the lane declares no contract version (add `contract: %d`)", ContractVersion)
	} else if v, ok := l.intValue(file, off, n, "contract"); ok {
		lane.Contract = v
		if v != ContractVersion {
			l.at(CodeContractVersion, file, off, n, "contract", "the lane targets contract %d, this daemon implements contract %d (see docs/upgrade.md, section %q)", v, ContractVersion, UpgradeSection)
		}
	}
	dirName := filepath.Base(filepath.Clean(l.dir))
	if n, ok := keys["name"]; !ok {
		l.at(CodeMissingKey, file, off, root, "name", "the lane declares no name (its directory name, %q)", dirName)
	} else if v, ok := l.stringValue(file, off, n, "name"); ok {
		lane.Name = v
		switch {
		case !idPattern.MatchString(v):
			l.at(CodeName, file, off, n, "name", "%q is not a lane name (^[a-z][a-z0-9-]{0,39}$)", v)
		case v != dirName:
			l.at(CodeName, file, off, n, "name", "%q is not the lane's directory name %q", v, dirName)
		}
	}
	if n, ok := keys["description"]; !ok {
		l.at(CodeDescription, file, off, root, "description", "the lane declares no description")
	} else if v, ok := l.stringValue(file, off, n, "description"); ok {
		lane.Description = v
		if strings.TrimSpace(v) == "" {
			l.at(CodeDescription, file, off, n, "description", "the description is empty")
		}
	}
	if n, ok := keys["defaults"]; ok {
		lane.Defaults = l.defaults(file, off, n)
	}
	maxNode, hasMax := keys["max_rewinds"]
	if hasMax {
		if v, ok := l.intValue(file, off, maxNode, "max_rewinds"); ok {
			if v < 0 {
				l.at(CodeInvalidValue, file, off, maxNode, "max_rewinds", "%d is negative (0 = every gate FAIL asks the owner)", v)
			}
			lane.MaxRewinds = v
		}
	}
	if n, ok := keys["uncounted_rewinds"]; ok {
		lane.UncountedRewinds = l.uncountedRewinds(file, off, n)
	}
	if n, ok := keys["exclusive"]; ok {
		lane.Exclusive, _ = l.boolValue(file, off, n, "exclusive")
	}
	if n, ok := keys["acceptance"]; ok {
		if v, good := l.stringValue(file, off, n, "acceptance"); good {
			switch v {
			case AcceptanceRequired, AcceptanceNone:
				lane.Acceptance = v
			default:
				l.at(CodeInvalidValue, file, off, n, "acceptance", "%q is not an acceptance (want %s or %s)", v, AcceptanceRequired, AcceptanceNone)
			}
		}
	}
	if n, ok := keys["children_branch"]; ok {
		if v, good := l.stringValue(file, off, n, "children_branch"); good {
			lane.ChildrenBranch = v
			if msg := childrenBranchDefect(v); msg != "" {
				l.at(CodeInvalidValue, file, off, n, "children_branch", "%s", msg)
			}
		}
	}
	if n, ok := keys["epic"]; ok {
		lane.Epic, _ = l.boolValue(file, off, n, "epic")
	}
	if n, ok := keys["resume_reset"]; ok {
		if v, good := l.stringValue(file, off, n, "resume_reset"); good {
			lane.ResumeReset = v
		}
	}
	stepsNode, ok := keys["steps"]
	if !ok {
		l.at(CodeMissingKey, file, off, root, "steps", "the lane declares no steps")
		return lane
	}
	lane.Steps = l.steps(file, off, stepsNode)

	// lane-wide rules over the steps
	onFail := false
	for _, s := range lane.Steps {
		if s.OnFailTarget != "" {
			onFail = true
		}
	}
	switch {
	case onFail && !hasMax:
		l.at(CodeMissingKey, file, off, root, "max_rewinds", "a step declares on_fail, so the lane must declare max_rewinds (its rewind budget)")
	case !onFail && hasMax:
		l.at(CodeInvalidValue, file, off, maxNode, "max_rewinds", "no step declares on_fail, so the lane has nothing to rewind: remove max_rewinds")
	}
	if uncountedNode, hasUncounted := keys["uncounted_rewinds"]; !onFail && hasUncounted {
		l.at(CodeInvalidValue, file, off, uncountedNode, "uncounted_rewinds", "no step declares on_fail, so the lane has nothing to rewind: remove uncounted_rewinds")
	}
	// an epic lane names the branch its own work items work on through
	// its children_branch, and the step an owner-resumed changed-children
	// marker resets it to through resume_reset: either key without epic
	// names nothing, and epic without either is refused (polyforge#117)
	if lane.Epic && lane.ChildrenBranch == "" {
		l.at(CodeInvalidValue, file, off, keys["epic"], "epic", "an epic lane's own work items work on the children branch, so the lane must declare children_branch too")
	}
	if lane.Epic && lane.ResumeReset == "" {
		l.at(CodeInvalidValue, file, off, keys["epic"], "epic", "an owner-resumed epic's changed-children marker resets the lane to a step, so the lane must declare resume_reset too")
	}
	if !lane.Epic && lane.ResumeReset != "" {
		l.at(CodeInvalidValue, file, off, keys["resume_reset"], "resume_reset", "the lane declares no epic: no changed-children marker of its work items ever resets it, so resume_reset names nothing")
	}
	if lane.ResumeReset != "" {
		found := false
		for _, s := range lane.Steps {
			if s.ID == lane.ResumeReset {
				found = true
				break
			}
		}
		if !found {
			l.at(CodeInvalidValue, file, off, keys["resume_reset"], "resume_reset", "%q is not a step of this lane", lane.ResumeReset)
		}
	}
	l.modelTiers(file, off, lane)
	l.definitions(lane)
	return lane
}

// modelTiers judges every step's model_tier against the lane's tier
// table (defaults.tiers): a tier the table does not carry is WFC-198.
func (l *loader) modelTiers(file string, off int, lane *Lane) {
	for i := range lane.Steps {
		s := &lane.Steps[i]
		if s.ModelTier == "" {
			continue
		}
		if lane.Defaults == nil || lane.Defaults.Tiers == nil {
			l.at(CodeModelCandidate, file, off, s.nodes["model_tier"], join(s.path, "model_tier"), "model_tier %q names a tier, and the lane declares no defaults.tiers", s.ModelTier)
			continue
		}
		if _, ok := lane.Defaults.Tiers[s.ModelTier]; !ok {
			l.at(CodeModelCandidate, file, off, s.nodes["model_tier"], join(s.path, "model_tier"), "model_tier %q is not a tier of defaults.tiers (%s)", s.ModelTier, strings.Join(sortedKeys(lane.Defaults.Tiers), ", "))
		}
	}
}

// TierCandidates answers the candidate list the step's model_tier names
// (nil when the step names none, or the lane has no such tier).
func (l *Lane) TierCandidates(s *Step) []string {
	if l == nil || s == nil || s.ModelTier == "" || l.Defaults == nil {
		return nil
	}
	list, ok := l.Defaults.Tiers[s.ModelTier]
	if !ok {
		return nil
	}
	return list
}

// contract4 refuses a lane whose only lane file is a workflow.md: WFC-101
// with its rewrite, the lane answered with nothing but the contract its
// frontmatter declares (0 when it declares none or does not parse), so a
// capsule snapshot of an earlier contract is kept and parks blocked (kind
// contract) like any other version.
func (l *loader) contract4(file string) *Lane {
	lane := &Lane{Dir: l.dir, Shared: l.shared}
	if raw, err := os.ReadFile(file); err == nil {
		if front, _, ok := SplitFrontmatter(raw); ok {
			var v struct {
				Contract int `yaml:"contract"`
			}
			if yaml.Unmarshal(front, &v) == nil {
				lane.Contract = v.Contract
			}
		}
	}
	l.at(CodeContractVersion, file, 0, nil, "contract", "the lane is a %s, the lane file of the earlier releases (it declares contract %d); this daemon implements contract %d (see docs/upgrade.md, section %q) — %s",
		Contract4File, lane.Contract, ContractVersion, UpgradeSection, Contract4Hint)
	return lane
}

// laneFile answers the YAML mapping of a workflow.yaml (nil after recording
// the defect when it is empty, does not parse, holds more than one YAML
// document or is not a mapping).
func (l *loader) laneFile(file string, raw []byte) *yaml.Node {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		l.at(CodeLaneFile, file, 0, nil, "", "%s is empty", WorkflowFile)
		return nil
	} else if err != nil {
		l.at(CodeLaneFile, file, 0, nil, "", "%s does not parse: %v", WorkflowFile, err)
		return nil
	}
	var more yaml.Node
	if err := dec.Decode(&more); err == nil {
		l.at(CodeLaneFile, file, 0, &more, "", "%s holds more than one YAML document: a lane is one mapping", WorkflowFile)
		return nil
	} else if !errors.Is(err, io.EOF) {
		l.at(CodeLaneFile, file, 0, nil, "", "%s does not parse: %v", WorkflowFile, err)
		return nil
	}
	if len(doc.Content) == 0 {
		l.at(CodeLaneFile, file, 0, nil, "", "%s is empty", WorkflowFile)
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		l.at(CodeLaneFile, file, 0, root, "", "%s is not a mapping of the lane keys", WorkflowFile)
		return nil
	}
	return root
}

// mapping walks one mapping node against its closed key set: a duplicate
// key is WFC-110, a removed key (with its rewrite) or any other unknown
// key WFC-111 — naming the key and asking to upgrade pf, since a newer
// release may add it without bumping the contract version (a compatible
// change, contract.go). It answers the known keys' value nodes.
func (l *loader) mapping(file string, off int, n *yaml.Node, path string, known map[string]keySpec, removed map[string]string) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n.Kind != yaml.MappingNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a mapping")
		return out
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := join(path, k.Value)
		if seen[k.Value] {
			l.at(CodeLaneFile, file, off, k, p, "duplicate key")
			continue
		}
		seen[k.Value] = true
		if hint, ok := removed[k.Value]; ok {
			l.at(CodeUnknownKey, file, off, k, p, "%s", hint)
			continue
		}
		if _, ok := known[k.Value]; !ok {
			l.at(CodeUnknownKey, file, off, k, p, "this lane uses key %q, which this pf (workflow contract %d) does not know: upgrade pf — a newer release may define it (the keys this pf takes here: %s)", k.Value, ContractVersion, strings.Join(sortedKeys(known), ", "))
			continue
		}
		out[k.Value] = v
	}
	return out
}

// join appends key to a YAML path.
func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// stringValue decodes a string scalar (WFC-112 otherwise).
func (l *loader) stringValue(file string, off int, n *yaml.Node, path string) (string, bool) {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		l.at(CodeInvalidValue, file, off, n, path, "want a string")
		return "", false
	}
	return n.Value, true
}

// intValue decodes an integer scalar (WFC-112 otherwise).
func (l *loader) intValue(file string, off int, n *yaml.Node, path string) (int, bool) {
	var v int
	if n.Kind != yaml.ScalarNode || n.Tag != "!!int" || n.Decode(&v) != nil {
		l.at(CodeInvalidValue, file, off, n, path, "want an integer")
		return 0, false
	}
	return v, true
}

// boolValue decodes a boolean scalar (WFC-112 otherwise).
func (l *loader) boolValue(file string, off int, n *yaml.Node, path string) (bool, bool) {
	var v bool
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" || n.Decode(&v) != nil {
		l.at(CodeInvalidValue, file, off, n, path, "want true or false")
		return false, false
	}
	return v, true
}

// stringMap decodes a mapping of string to string (WFC-112 otherwise).
func (l *loader) stringMap(file string, off int, n *yaml.Node, path string) map[string]string {
	if n.Kind != yaml.MappingNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a mapping of strings")
		return nil
	}
	out := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := join(path, k.Value)
		if _, dup := out[k.Value]; dup {
			l.at(CodeLaneFile, file, off, k, p, "duplicate key")
			continue
		}
		if s, ok := l.stringValue(file, off, v, p); ok {
			out[k.Value] = s
		}
	}
	return out
}

// candidates decodes a model-candidate list, each entry in canonical
// `<harness>:<model>[:<effort>]` form (WFC-198 otherwise).
func (l *loader) candidates(file string, off int, n *yaml.Node, path string) []string {
	if n.Kind != yaml.SequenceNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a list of model candidates")
		return nil
	}
	var out []string
	for i, c := range n.Content {
		p := fmt.Sprintf("%s[%d]", path, i)
		s, ok := l.stringValue(file, off, c, p)
		if !ok {
			continue
		}
		canon, err := harness.Normalize(s)
		if err != nil {
			l.at(CodeModelCandidate, file, off, c, p, "%v", err)
			continue
		}
		out = append(out, canon)
	}
	return out
}

// defaults decodes the lane's `defaults`.
func (l *loader) defaults(file string, off int, n *yaml.Node) *Defaults {
	keys := l.mapping(file, off, n, "defaults", defaultsKeys, removedKeys["defaults"])
	d := &Defaults{}
	if c, ok := keys["model_candidates"]; ok {
		d.ModelCandidates = l.candidates(file, off, c, "defaults.model_candidates")
	}
	if t, ok := keys["tiers"]; ok {
		d.Tiers = l.tiers(file, off, t, "defaults.tiers")
	}
	return d
}

// uncountedRewinds decodes the lane's `uncounted_rewinds` (the
// lane-declared uncounted rewind class): the reason substring a gate
// FAIL's one-line hand-off reason carries, and the class's own cap —
// the default (DefaultUncountedRewinds) when the lane declares none.
func (l *loader) uncountedRewinds(file string, off int, n *yaml.Node) *UncountedRewinds {
	keys := l.mapping(file, off, n, "uncounted_rewinds", uncountedRewindsKeys, removedKeys["uncounted_rewinds"])
	c := &UncountedRewinds{Max: DefaultUncountedRewinds}
	if rn, ok := keys["reason_contains"]; !ok {
		l.at(CodeMissingKey, file, off, n, "uncounted_rewinds.reason_contains", "the class declares no reason_contains (the substring a gate FAIL's one-line reason carries)")
	} else if s, good := l.stringValue(file, off, rn, "uncounted_rewinds.reason_contains"); good {
		if strings.TrimSpace(s) == "" {
			l.at(CodeInvalidValue, file, off, rn, "uncounted_rewinds.reason_contains", "the reason substring is blank")
		} else {
			c.ReasonContains = s
		}
	}
	if mn, ok := keys["max"]; ok {
		if v, good := l.intValue(file, off, mn, "uncounted_rewinds.max"); good {
			if v < 0 {
				l.at(CodeInvalidValue, file, off, mn, "uncounted_rewinds.max", "%d is negative (0 = every matching FAIL asks the owner)", v)
			} else {
				c.Max = v
			}
		}
	}
	return c
}

// tiers decodes `defaults.tiers`: tier name -> a candidate list.
func (l *loader) tiers(file string, off int, n *yaml.Node, path string) map[string][]string {
	if n.Kind != yaml.MappingNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a mapping of tier name to model candidates")
		return nil
	}
	out := map[string][]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := join(path, k.Value)
		if _, dup := out[k.Value]; dup {
			l.at(CodeLaneFile, file, off, k, p, "duplicate key")
			continue
		}
		if !idPattern.MatchString(k.Value) {
			l.at(CodeInvalidValue, file, off, k, p, "%q is not a tier name (^[a-z][a-z0-9-]{0,39}$)", k.Value)
			continue
		}
		list := l.candidates(file, off, v, p)
		if list == nil {
			list = []string{}
		}
		out[k.Value] = list
	}
	return out
}

// steps decodes and judges the step list.
func (l *loader) steps(file string, off int, n *yaml.Node) []Step {
	if n.Kind != yaml.SequenceNode {
		l.at(CodeInvalidValue, file, off, n, "steps", "want a list of steps")
		return nil
	}
	if len(n.Content) == 0 {
		l.at(CodeMissingKey, file, off, n, "steps", "the lane declares no steps")
		return nil
	}
	var out []Step
	seen := map[string]bool{}
	for i, sn := range n.Content {
		path := fmt.Sprintf("steps[%d]", i)
		s := l.step(file, off, sn, path, seen)
		if s.ID != "" {
			seen[s.ID] = true
		}
		out = append(out, s)
	}
	l.rewindOnlyCoverage(file, off, n, out)
	return out
}

// rewindOnlyCoverage judges every rewind_only step against the gates'
// rewind ranges: a step declaring the key must sit in some gate's
// [on_fail target, gate) range — the range a rewind of that gate resets
// — or it would never run (the first round skips it and no rewind can
// reach it). The judgement needs the whole step list, so it runs after
// the list is parsed; the defect is WFC-112 at the step's own path,
// naming the step.
func (l *loader) rewindOnlyCoverage(file string, off int, n *yaml.Node, steps []Step) {
	idx := map[string]int{}
	for i, s := range steps {
		idx[s.ID] = i
	}
	for i, s := range steps {
		if !s.RewindOnly {
			continue
		}
		covered := false
		for _, g := range steps {
			if !g.Gate || g.OnFailTarget == "" {
				continue
			}
			if t, ok := idx[g.OnFailTarget]; ok && t <= i && i < idx[g.ID] {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		l.at(CodeInvalidValue, file, off, mappingNode(n.Content[i], "rewind_only"),
			fmt.Sprintf("steps[%d](%s).rewind_only", i, s.ID),
			"step %q declares rewind_only, but no gate's [on_fail target, gate) range covers it: the first round skips the step and no rewind would ever reach it, so it would never run — drop the key or place the step inside a gate's rewind range", s.ID)
	}
}

// step decodes and judges one step entry; seen holds the ids declared
// before it.
func (l *loader) step(file string, off int, n *yaml.Node, path string, seen map[string]bool) Step {
	idPath := join(path, "id")
	if v := mappingNode(n, "id"); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
		path = fmt.Sprintf("%s(%s)", path, v.Value)
	}
	keys := l.mapping(file, off, n, path, stepKeys, removedKeys["step"])
	s := Step{path: path, nodes: keys}
	if v, ok := keys["id"]; !ok {
		l.at(CodeStepID, file, off, n, idPath, "the step declares no id")
	} else if id, ok := l.stringValue(file, off, v, idPath); ok {
		s.ID = id
		switch {
		case !idPattern.MatchString(id):
			l.at(CodeStepID, file, off, v, idPath, "%q is not a step id (^[a-z][a-z0-9-]{0,39}$)", id)
		case seen[id]:
			l.at(CodeStepID, file, off, v, idPath, "duplicate step id %q", id)
		}
	}
	if v, ok := keys["gate"]; ok {
		s.Gate, _ = l.boolValue(file, off, v, join(path, "gate"))
	}
	if v, ok := keys["on_fail"]; ok {
		of := l.mapping(file, off, v, join(path, "on_fail"), onFailKeys, removedKeys["on_fail"])
		t, has := of["target"]
		switch {
		case !has:
			l.at(CodeOnFail, file, off, v, join(path, "on_fail"), "on_fail declares no target")
		default:
			if target, ok := l.stringValue(file, off, t, join(path, "on_fail.target")); ok {
				s.OnFailTarget = target
				switch {
				case !s.Gate:
					l.at(CodeOnFail, file, off, v, join(path, "on_fail"), "on_fail on a step that is not a gate")
				case target == s.ID || !seen[target]:
					l.at(CodeOnFail, file, off, t, join(path, "on_fail.target"), "%q is not an earlier step of the lane", target)
				}
			}
		}
	}
	if v, ok := keys["budget"]; ok {
		b := l.mapping(file, off, v, join(path, "budget"), budgetKeys, nil)
		if w, ok := b["wall_minutes"]; ok {
			if m, ok := l.intValue(file, off, w, join(path, "budget.wall_minutes")); ok {
				if m <= 0 {
					l.at(CodeInvalidValue, file, off, w, join(path, "budget.wall_minutes"), "%d is not a positive number of minutes", m)
				}
				s.WallMinutes = m
			}
		}
		if w, ok := b["tool_minutes"]; ok {
			if m, ok := l.intValue(file, off, w, join(path, "budget.tool_minutes")); ok {
				switch {
				case m <= 0:
					l.at(CodeInvalidValue, file, off, w, join(path, "budget.tool_minutes"), "%d is not a positive number of minutes", m)
				case m > MaxToolMinutes:
					l.at(CodeInvalidValue, file, off, w, join(path, "budget.tool_minutes"), "%d exceeds the contract's bound of %d minutes", m, MaxToolMinutes)
				case s.WallMinutes > 0 && m > s.WallMinutes:
					l.at(CodeInvalidValue, file, off, w, join(path, "budget.tool_minutes"), "%d exceeds the step's wall_minutes (%d): the tool window never outlasts the wall estimate", m, s.WallMinutes)
				}
				s.ToolMinutes = m
			}
		}
	}
	if v, ok := keys["waivable"]; ok {
		if b, good := l.boolValue(file, off, v, join(path, "waivable")); good {
			if b && !s.Gate {
				l.at(CodeInvalidValue, file, off, v, join(path, "waivable"), "waivable is a gate's: the step declares no gate: true")
			} else {
				s.Waivable = b
			}
		}
	}
	if v, ok := keys["skippable"]; ok {
		s.Skippable, _ = l.boolValue(file, off, v, join(path, "skippable"))
	}
	if v, ok := keys["rewind_only"]; ok {
		s.RewindOnly, _ = l.boolValue(file, off, v, join(path, "rewind_only"))
	}
	if v, ok := keys["human_mergeable"]; ok {
		s.HumanMergeable, _ = l.boolValue(file, off, v, join(path, "human_mergeable"))
	}
	if v, ok := keys["model_candidates"]; ok {
		s.ModelCandidates = l.candidates(file, off, v, join(path, "model_candidates"))
	}
	if v, ok := keys["model_tier"]; ok {
		if tier, good := l.stringValue(file, off, v, join(path, "model_tier")); good {
			switch {
			case strings.TrimSpace(tier) == "":
				l.at(CodeInvalidValue, file, off, v, join(path, "model_tier"), "the tier name is empty")
			case keys["model_candidates"] != nil:
				l.at(CodeInvalidValue, file, off, v, join(path, "model_tier"), "a step declares model_tier or model_candidates, not both")
			default:
				s.ModelTier = tier
			}
		}
	}
	if v, ok := keys["skill"]; ok {
		if name, good := l.stringValue(file, off, v, join(path, "skill")); good {
			if !idPattern.MatchString(name) {
				l.at(CodeInvalidValue, file, off, v, join(path, "skill"), "%q is not a skill name (^[a-z][a-z0-9-]{0,39}$)", name)
			} else {
				s.Skill = name
			}
		}
	}
	if v, ok := keys["resources"]; ok {
		s.Resources = l.resources(file, off, v, join(path, "resources"))
	}
	if v, ok := keys["hub"]; ok {
		s.Hub, s.HubAttrs = l.hub(file, off, v, join(path, "hub"))
	}
	if v, ok := keys["wrap_attrs"]; ok {
		s.WrapAttrs = l.wrapAttrs(file, off, v, join(path, "wrap_attrs"))
	}
	if v, ok := keys["vars"]; ok {
		s.Vars = l.stringMap(file, off, v, join(path, "vars"))
		s.varLines = map[string]int{}
		for i := 0; i+1 < len(v.Content); i += 2 {
			s.varLines[v.Content[i].Value] = v.Content[i].Line + off
		}
	}
	if v, ok := keys["vars_files"]; ok {
		l.varsFiles(file, off, v, join(path, "vars_files"), &s)
	}
	if v, ok := keys["params"]; ok {
		s.Params = l.stepParams(file, off, v, join(path, "params"), &s)
	}
	return s
}

// MaxParamValueBytes bounds one parameter's value (a step entry's `params`
// default, and every layer that overrides it): a value is one line — no
// newline — of at most this many bytes. A value is inserted verbatim into
// the step's prompt and exported as PF_PARAM_<NAME>, so the bound keeps
// both surfaces small (owner ruling 2026-11-16).
const MaxParamValueBytes = 256

// ParamValueProblem answers why value is not a legal parameter value (""
// when it is): one line, at most MaxParamValueBytes bytes. The one rule
// every layer shares — the lane loader (WFC-112), the workspace yaml's
// parse and the pf override write — so a value cannot grow a new meaning
// on its way to the prompt or the environment.
func ParamValueProblem(value string) string {
	if strings.ContainsAny(value, "\n\r") {
		return "a param value is one line (no newline)"
	}
	if len(value) > MaxParamValueBytes {
		return fmt.Sprintf("a param value is at most %d bytes", MaxParamValueBytes)
	}
	return ""
}

// stepParams decodes a step's `params`: each name to its lane-default
// value, a single-line string of at most MaxParamValueBytes bytes. A
// name takes the param-name shape, collides with no var of the step
// (vars or vars_files) and shadows no fragment of the shared tree; a
// parameter the definition never uses is allowed (a script may read it).
func (l *loader) stepParams(file string, off int, n *yaml.Node, path string, s *Step) map[string]string {
	if n.Kind != yaml.MappingNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a mapping of param name to its default value")
		return nil
	}
	out := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := join(path, k.Value)
		if seen[k.Value] {
			l.at(CodeLaneFile, file, off, k, p, "duplicate key")
			continue
		}
		seen[k.Value] = true
		if !ParamName.MatchString(k.Value) {
			l.at(CodeInvalidValue, file, off, k, p, "%q is not a param name (%s)", k.Value, ParamName)
			continue
		}
		val, ok := l.stringValue(file, off, v, p)
		if !ok {
			continue
		}
		if why := ParamValueProblem(val); why != "" {
			l.at(CodeInvalidValue, file, off, v, p, "%s", why)
			continue
		}
		if _, varDeclared := s.varLines[k.Value]; varDeclared {
			key := "vars"
			if s.fileVars[k.Value] {
				key = "vars_files"
			}
			l.at(CodePlaceholder, file, off, k, p, "param %q collides with the var declared in %s.%s: a name is a var's or a param's, never both", k.Value, key, k.Value)
			continue
		}
		if _, err := os.Stat(sharedFragment(l.shared, k.Value)); err == nil {
			l.at(CodePlaceholder, file, off, k, p, "param %q shadows the fragment %s/%s.md", k.Value, SharedDir, k.Value)
			continue
		}
		out[k.Value] = val
	}
	return out
}

// varsFiles decodes a step's `vars_files` into its vars: each var's value
// is the text of the file of the lane its entry names (laneFileText),
// exactly as if it were written inline in `vars`. A var declared in both
// is WFC-112, as is a path that leaves the lane or names no regular file.
func (l *loader) varsFiles(file string, off int, n *yaml.Node, path string, s *Step) {
	if n.Kind != yaml.MappingNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a mapping of var name to a file of the lane")
		return
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := join(path, k.Value)
		if seen[k.Value] {
			l.at(CodeLaneFile, file, off, k, p, "duplicate key")
			continue
		}
		seen[k.Value] = true
		rel, ok := l.stringValue(file, off, v, p)
		if !ok {
			continue
		}
		if _, inline := s.varLines[k.Value]; inline {
			l.at(CodeInvalidValue, file, off, k, p, "var %q is declared in vars and in vars_files: a var takes one of the two", k.Value)
			continue
		}
		text, err := laneFileText(l.dir, rel)
		if err != nil {
			l.at(CodeInvalidValue, file, off, v, p, "%v", err)
			continue
		}
		if s.Vars == nil {
			s.Vars = map[string]string{}
		}
		if s.varLines == nil {
			s.varLines = map[string]int{}
		}
		if s.fileVars == nil {
			s.fileVars = map[string]bool{}
		}
		s.Vars[k.Value] = text
		s.varLines[k.Value] = k.Line + off
		s.fileVars[k.Value] = true
	}
}

// laneFileText reads the file rel names inside the lane at dir (a
// vars_files value): a relative, slash-separated path that stays inside
// the lane directory and names a regular file reached through no symbolic
// link — the claim's snapshot copies the lane's regular files and nothing
// else, so the snapshot reads the same text.
func laneFileText(dir, rel string) (string, error) {
	clean := path.Clean(rel)
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%q is not a file inside the lane directory (want a relative path such as fragments/<name>.md)", rel)
	}
	p := dir
	for _, part := range strings.Split(clean, "/") {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if err != nil {
			return "", fmt.Errorf("%q resolves to no file of the lane: %v", rel, err)
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%q passes through the symbolic link %s: a var file is a regular file of the lane (its snapshot copies no link)", rel, p)
		}
	}
	if st, _ := os.Lstat(p); !st.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", rel)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("%q is unreadable: %v", rel, err)
	}
	return string(raw), nil
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// resources decodes a step's `resources`: the admission and the locks.
func (l *loader) resources(file string, off int, n *yaml.Node, path string) *Resources {
	keys := l.mapping(file, off, n, path, resourcesKeys, nil)
	r := &Resources{Admission: AdmissionNone}
	if a, ok := keys["admission"]; ok {
		if v, good := l.stringValue(file, off, a, join(path, "admission")); good {
			switch v {
			case AdmissionNone, AdmissionHeavy:
				r.Admission = v
			default:
				l.at(CodeInvalidValue, file, off, a, join(path, "admission"), "%q is not an admission (want %s or %s)", v, AdmissionNone, AdmissionHeavy)
			}
		}
	}
	if lk, ok := keys["locks"]; ok {
		if lk.Kind != yaml.SequenceNode {
			l.at(CodeInvalidValue, file, off, lk, join(path, "locks"), "want a list of locks (%s)", LockMerge)
			return r
		}
		for i, c := range lk.Content {
			p := fmt.Sprintf("%s.locks[%d]", path, i)
			v, good := l.stringValue(file, off, c, p)
			if !good {
				continue
			}
			switch {
			case v == LockBar:
				l.at(CodeInvalidValue, file, off, c, p, "%s", LockBarRemovedHint)
			case v != LockMerge:
				l.at(CodeInvalidValue, file, off, c, p, "%q is not a lock (want %s)", v, LockMerge)
			case contains(r.Locks, v):
				l.at(CodeInvalidValue, file, off, c, p, "lock %q is listed twice", v)
			default:
				r.Locks = append(r.Locks, v)
			}
		}
	}
	return r
}

// hub decodes a step's `hub`: the `pf step hub` verbs it may call
// (`verbs`, each one of HubVerbs at most once) and the attrs keys it may
// write (`attrs`, each an attrs key at most once).
func (l *loader) hub(file string, off int, n *yaml.Node, path string) (verbs, attrs []string) {
	keys := l.mapping(file, off, n, path, hubKeys, nil)
	if v, ok := keys["verbs"]; ok {
		verbs = l.nameList(file, off, v, join(path, "verbs"), "hub verb", func(s string) string {
			if !contains(HubVerbs, s) {
				return fmt.Sprintf("%q is not a hub verb (want one of %s; a step writes attrs keys by naming them in hub.attrs)", s, strings.Join(HubVerbs, ", "))
			}
			return ""
		})
	}
	if v, ok := keys["attrs"]; ok {
		attrs = l.nameList(file, off, v, join(path, "attrs"), "attrs key", func(s string) string {
			if !wrapAttrName.MatchString(s) {
				return fmt.Sprintf("%q is not an attrs key (%s)", s, wrapAttrName)
			}
			return ""
		})
	}
	return verbs, attrs
}

// nameList decodes a list of names, each at most once and each passing
// bad (which answers why a name is refused, "" when it is good).
func (l *loader) nameList(file string, off int, n *yaml.Node, path, what string, bad func(string) string) []string {
	if n.Kind != yaml.SequenceNode {
		l.at(CodeInvalidValue, file, off, n, path, "want a list of %ss", what)
		return nil
	}
	out := []string{}
	for i, c := range n.Content {
		p := fmt.Sprintf("%s[%d]", path, i)
		v, good := l.stringValue(file, off, c, p)
		if !good {
			continue
		}
		if why := bad(v); why != "" {
			l.at(CodeInvalidValue, file, off, c, p, "%s", why)
			continue
		}
		if contains(out, v) {
			l.at(CodeInvalidValue, file, off, c, p, "%s %q is listed twice", what, v)
			continue
		}
		out = append(out, v)
	}
	return out
}

// wrapAttrs decodes a step's `wrap_attrs`: attr names, each at most once.
func (l *loader) wrapAttrs(file string, off int, n *yaml.Node, path string) []string {
	return l.nameList(file, off, n, path, "wrap attr name", func(s string) string {
		if !wrapAttrName.MatchString(s) {
			return fmt.Sprintf("%q is not a wrap attr name (%s)", s, wrapAttrName)
		}
		return ""
	})
}

// HubAllowed answers the `pf step hub` verbs the step may call: its
// `hub.verbs`, wrap-attrs when it declares `wrap_attrs`, and attrs when its
// `hub.attrs` names a key.
func (s *Step) HubAllowed() []string {
	if s == nil {
		return nil
	}
	out := append([]string(nil), s.Hub...)
	if len(s.WrapAttrs) > 0 && !contains(out, "wrap-attrs") {
		out = append(out, "wrap-attrs")
	}
	if len(s.HubAttrs) > 0 {
		out = append(out, "attrs")
	}
	return out
}

// definitions resolves, renders and parses every step's definition file
// and its skill.
func (l *loader) definitions(lane *Lane) {
	for i := range lane.Steps {
		s := &lane.Steps[i]
		if s.ID == "" || !idPattern.MatchString(s.ID) {
			continue
		}
		l.definition(s)
		if s.Skill != "" {
			l.skill(s)
		}
	}
}

// definition reads, renders and parses the step's definition file. A var
// defect points at the var's key in the workflow.yaml, a placeholder defect
// at the definition's line.
func (l *loader) definition(s *Step) *Definition {
	path := stepDefinitionPath(l.dir, l.shared, s.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		l.at(CodeDefinitionMissing, path, 0, nil, "", "definition %q resolves to no file (want %s under the lane, or %s): %v",
			s.ID, "steps/"+s.ID+".md", l.sharedRel("steps/"+s.ID+".md"), err)
		return nil
	}
	rendered, problems := renderDefinition(l.shared, raw, s.Vars, s.Params)
	// a placeholder problem leaves the placeholder text in place; the
	// frontmatter is still judged, so one load reports both
	for _, p := range problems {
		e := ContractError{Code: CodePlaceholder, File: path, Line: p.line, Msg: p.msg}
		if p.varName != "" {
			key := "vars."
			if s.fileVars[p.varName] {
				key = "vars_files."
			}
			e.File, e.Line, e.Path = filepath.Join(l.dir, WorkflowFile), s.varLines[p.varName], join(s.path, key+p.varName)
			e.Msg = fmt.Sprintf("%s (definition %s)", p.msg, path)
		}
		l.errs = append(l.errs, e)
	}
	d, errs := parseDefinition(path, s.ID, rendered)
	l.errs = append(l.errs, errs...)
	return d
}

// sharedRel names rel under the loader's shared tree for a defect: the
// `../_shared/<rel>` beside a built-in lane, the built-in tree's path for
// a user lane.
func (l *loader) sharedRel(rel string) string {
	if l.shared == SiblingShared(l.dir) {
		return "../" + SharedDir + "/" + rel
	}
	return filepath.Join(l.shared, filepath.FromSlash(rel))
}

// placeholderProblem is one WFC-195 defect of a definition render.
type placeholderProblem struct {
	line int
	// varName names the declared var the problem is about ("" for a
	// placeholder of the definition).
	varName, msg string
}

// renderDefinition renders one definition with vars, the step's params
// and the fragments of the shared tree at shared its other placeholders
// name, answering every placeholder problem: a placeholder no var, param
// or fragment resolves, a var nothing uses, a var shadowing a fragment.
// Fragments are inserted verbatim and never re-scanned.
func renderDefinition(shared string, raw []byte, vars, params map[string]string) ([]byte, []placeholderProblem) {
	var problems []placeholderProblem
	lineOf := func(name string) int {
		idx := strings.Index(string(raw), "{{"+name+"}}")
		if idx < 0 {
			return 0
		}
		return strings.Count(string(raw[:idx]), "\n") + 1
	}
	for _, name := range sortedKeys(vars) {
		if _, err := os.Stat(sharedFragment(shared, name)); err == nil {
			problems = append(problems, placeholderProblem{varName: name, msg: fmt.Sprintf("var %q shadows the fragment %s/%s.md", name, SharedDir, name)})
		}
	}
	used := map[string]bool{}
	values := map[string]string{}
	missing := map[string]bool{}
	out := placeholder.ReplaceAllFunc(raw, func(m []byte) []byte {
		name := string(m[2 : len(m)-2])
		if v, ok := vars[name]; ok {
			used[name] = true
			return []byte(strings.TrimRight(v, "\n"))
		}
		if v, ok := params[name]; ok {
			return []byte(v)
		}
		if v, ok := values[name]; ok {
			return []byte(v)
		}
		text, err := os.ReadFile(sharedFragment(shared, name))
		if err != nil {
			missing[name] = true
			return m
		}
		values[name] = strings.TrimSpace(string(text))
		return []byte(values[name])
	})
	for _, name := range sortedKeys(missing) {
		problems = append(problems, placeholderProblem{line: lineOf(name), msg: fmt.Sprintf("{{%s}} resolves to no var or param of the step and no fragment %s/%s.md", name, SharedDir, name)})
	}
	for _, name := range sortedKeys(vars) {
		if !used[name] {
			problems = append(problems, placeholderProblem{varName: name, msg: fmt.Sprintf("var %q is used by no placeholder of the definition", name)})
		}
	}
	return out, problems
}

// Definition is one parsed definition file: its frontmatter keys and its
// body (the prompt text).
type Definition struct {
	Path        string
	Name        string
	Description string
	Body        string
}

// decodeDefinition is the one parse of a rendered definition file: its
// frontmatter mapping (nil root when the file has none) and its body. It
// judges nothing — Load judges what it answers (parseDefinition).
func decodeDefinition(raw []byte) (root *yaml.Node, body string, hasFront bool, err error) {
	front, rest, ok := SplitFrontmatter(raw)
	if !ok {
		return nil, strings.TrimSpace(string(raw)), false, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, "", true, fmt.Errorf("frontmatter does not parse: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, strings.TrimSpace(string(rest)), true, fmt.Errorf("the frontmatter is not a mapping")
	}
	return doc.Content[0], strings.TrimSpace(string(rest)), true, nil
}

// mappingNode answers the value node of key in a mapping (nil when absent).
func mappingNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// parseDefinition decodes one rendered definition file and judges it: a
// frontmatter of the closed key set — a removed key (`output`) refused
// with its rewrite — a name equal to id and a description (WFC-191), and a
// non-empty body.
func parseDefinition(path, id string, raw []byte) (*Definition, []ContractError) {
	l := &loader{}
	root, body, hasFront, err := decodeDefinition(raw)
	switch {
	case !hasFront:
		l.at(CodeDefinitionFront, path, 0, nil, "", "no frontmatter block (a definition declares its name and description)")
		return nil, l.errs
	case err != nil:
		l.at(CodeDefinitionFront, path, 0, nil, "", "%v", err)
		return nil, l.errs
	}
	const off = 1
	d := &Definition{Path: path, Body: body}
	keys := map[string]*yaml.Node{}
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		hint, removed := removedKeys["definition"][k.Value]
		switch {
		case seen[k.Value]:
			l.at(CodeDefinitionFront, path, off, k, k.Value, "duplicate key")
		case removed:
			l.at(CodeDefinitionFront, path, off, k, k.Value, "%s", hint)
		case defKeys[k.Value].Type == "":
			l.at(CodeDefinitionFront, path, off, k, k.Value, "unknown key (a definition takes %s)", strings.Join(sortedKeys(defKeys), ", "))
		default:
			keys[k.Value] = v
		}
		seen[k.Value] = true
	}
	if n, ok := keys["name"]; !ok {
		l.at(CodeDefinitionFront, path, off, root, "name", "the definition declares no name (want %q)", id)
	} else if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value != id {
		l.at(CodeDefinitionFront, path, off, n, "name", "name %q is not the definition's file name %q", n.Value, id)
	} else {
		d.Name = n.Value
	}
	if n, ok := keys["description"]; !ok || n.Kind != yaml.ScalarNode || strings.TrimSpace(n.Value) == "" {
		at := root
		if ok {
			at = n
		}
		l.at(CodeDefinitionFront, path, off, at, "description", "the definition declares no description")
	} else {
		d.Description = n.Value
	}
	if d.Body == "" {
		l.at(CodeDefinitionFront, path, 0, nil, "", "the definition's body is empty")
	}
	return d, l.errs
}

// RenderDefinition reads the definition id of the lane at dir (its own
// steps/<id>.md, else the shared one) and renders it with vars, params and
// its fragments: the whole file, frontmatter included. A placeholder
// problem is an error; a missing file answers an error os.IsNotExist
// holds for. The params are the step's FINAL values at a dispatch (the
// loader's own read passes the lane defaults).
func RenderDefinition(dir, id string, vars, params map[string]string) ([]byte, error) {
	return renderDefinitionFile(dir, SiblingShared(dir), id, vars, params)
}

// renderDefinitionFile is RenderDefinition over an explicit shared tree.
func renderDefinitionFile(dir, shared, id string, vars, params map[string]string) ([]byte, error) {
	path := stepDefinitionPath(dir, shared, id)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rendered, problems := renderDefinition(shared, raw, vars, params)
	if len(problems) > 0 {
		msgs := make([]string, 0, len(problems))
		for _, p := range problems {
			msgs = append(msgs, p.msg)
		}
		return nil, fmt.Errorf("%s: %s: %s", path, CodePlaceholder, strings.Join(msgs, "; "))
	}
	return rendered, nil
}

// ReadDefinition renders the definition id of the lane at dir
// (RenderDefinition) and decodes it through the one definition parse —
// the dispatch-time read of what Load judged. A placeholder problem or a
// frontmatter that does not parse is an error; a missing file answers an
// error os.IsNotExist holds for.
func ReadDefinition(dir, id string, vars, params map[string]string) (*Definition, error) {
	return readDefinition(dir, SiblingShared(dir), id, vars, params)
}

// readDefinition is ReadDefinition over an explicit shared tree.
func readDefinition(dir, shared, id string, vars, params map[string]string) (*Definition, error) {
	rendered, err := renderDefinitionFile(dir, shared, id, vars, params)
	if err != nil {
		return nil, err
	}
	path := stepDefinitionPath(dir, shared, id)
	root, body, _, err := decodeDefinition(rendered)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	_ = root
	return &Definition{Path: path, Body: body}, nil
}
