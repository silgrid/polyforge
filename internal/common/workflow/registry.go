package workflow

// registry.go: the contract registry — every key a lane, a step and a
// definition file may carry, the keys the contract removed (each with its
// rewrite), the lane properties and the error codes (contract.go). The loader (load.go)
// judges a lane against exactly this; RegistryDump renders it for the
// golden test that pins it (contract.v<N>.golden, regenerated in place
// for a compatible change; an incompatible one against the baseline is red
// until contract-pending.md registers it — compat.go), and LaneSchema
// (schema.go) renders the lane file's keys as the JSON Schema
// workflows/_schema/workflow-<N>.schema.json.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// keySpec is one key of a mapping the contract defines: its type (the
// dump's wording), whether the key is required (the dump's `[required]`;
// the lane schema's required list for a key of the lane file) and, for a
// key of the lane file, its value's JSON Schema (LaneSchema, whose enums
// are the dump's `[values]`; nil for a key of a definition or a skill,
// which the schema does not cover).
type keySpec struct {
	Type     string
	Required bool
	Schema   jsonSchema
}

// laneKeys is the closed key set of a workflow.yaml.
var laneKeys = map[string]keySpec{
	"contract":          {Type: "int, required, == ContractVersion", Required: true, Schema: jsonSchema{"const": ContractVersion}},
	"name":              {Type: "string, required, ^[a-z][a-z0-9-]{0,39}$, == directory name", Required: true, Schema: pattern(idPattern.String())},
	"description":       {Type: "string, required, non-empty", Required: true, Schema: pattern(`\S`)},
	"max_rewinds":       {Type: "int >= 0; required when any step declares on_fail, refused otherwise", Schema: jsonSchema{"type": "integer", "minimum": 0}},
	"uncounted_rewinds": {Type: "map {reason_contains, max}: the lane-declared class of gate-FAIL rewinds charged to no budget — a gate FAIL whose one-line hand-off reason carries reason_contains rewinds without spending max_rewinds, counted on its own toward max (the default 2 when the lane declares none; 0 = every matching FAIL asks the owner first, exactly like a spent max_rewinds); allowed only when a step declares on_fail, refused otherwise", Schema: object(uncountedRewindsKeys)},
	"acceptance":        {Type: "required|none (default required): whether a work item on the lane must list an acceptance criterion before it runs", Schema: jsonSchema{"enum": []string{AcceptanceNone, AcceptanceRequired}}},
	"children_branch":   {Type: "string: the branch-name template the daemon checks a sub-ticket of this lane's work items out on — it must carry {seq} (the parent's seq), may carry {project} (the parent's project name), and its substituted name must be a legal git branch ref (WFC-112 otherwise)", Schema: pattern(`\{seq\}`)},
	"epic":              {Type: "bool (default false): the lane's own work items are epics — the lane must also declare children_branch and resume_reset (WFC-112 otherwise), and a first claim checks such a work item out on the children branch substituted with its own seq, records no fixed target (it merges into the origin's default branch) and snapshots its children", Schema: jsonSchema{"type": "boolean"}},
	"resume_reset":      {Type: "string: the step id an owner-resumed epic's changed-children marker resets the lane to — required when the lane declares epic, refused without it, and it must name a step of this lane (WFC-112 otherwise)", Schema: pattern(idPattern.String())},
	"exclusive":         {Type: "bool (default false): one unfinished work item per project on the lane — a create on it reaches aihub exclusive, refused while another is not wrapped, failed or canceled", Schema: jsonSchema{"type": "boolean"}},
	"defaults":          {Type: "map {model_candidates, tiers}", Schema: object(defaultsKeys)},
	"steps":             {Type: "[]step, required, non-empty", Required: true, Schema: jsonSchema{"type": "array", "minItems": 1, "items": object(stepKeys)}},
}

// uncountedRewindsKeys is the closed key set of `uncounted_rewinds`.
var uncountedRewindsKeys = map[string]keySpec{
	"reason_contains": {Type: "string, required, non-empty: the substring a gate FAIL's one-line hand-off reason carries when its rewind falls in the class (the stable prefix of the failing step's script summary; the rest of the line varies)", Required: true, Schema: pattern(`\S`)},
	"max":             {Type: "int >= 0 (the default 2 when the lane declares none): the class's own rewind budget, held at `pf step handoff` exactly like max_rewinds", Schema: jsonSchema{"type": "integer", "minimum": 0}},
}

// defaultsKeys is the closed key set of `defaults`.
var defaultsKeys = map[string]keySpec{
	"model_candidates": {Type: "[]<harness>:<model>[:<effort>]", Schema: candidates()},
	"tiers":            {Type: "map <tier> -> []<harness>:<model>[:<effort>]; the table a step's model_tier names", Schema: jsonSchema{"type": "object", "propertyNames": pattern(idPattern.String()), "additionalProperties": candidates()}},
}

// stepKeys is the closed key set of one `steps` entry. Every step is an
// agent step that ends its dispatch with `pf step handoff` (workflow
// contract 4): what it reads, checks and hands off is its definition's
// and its skill's text, not a key.
var stepKeys = map[string]keySpec{
	"id":               {Type: "string, required, ^[a-z][a-z0-9-]{0,39}$, unique", Required: true, Schema: pattern(idPattern.String())},
	"gate":             {Type: "bool", Schema: jsonSchema{"type": "boolean"}},
	"on_fail":          {Type: "map {target}, gate only", Schema: object(onFailKeys)},
	"waivable":         {Type: "bool (default false), gate only: the gate may hand off waive on the owner's word", Schema: jsonSchema{"type": "boolean"}},
	"skippable":        {Type: "bool (default false): a project may switch the step off with `steps.<project>.<step>.skip: true` in its workspace yaml, fixed when the work item is first claimed; a switched-off step is never dispatched (its execution status is skipped), and the wrap_attrs of skipped steps at the lane's end pass to the last step that runs", Schema: jsonSchema{"type": "boolean"}},
	"rewind_only":      {Type: "bool (default false): the step runs only after a rewind covers it — the first round records it skipped without a dispatch, and it runs once any rewind that covers it (a gate FAIL's or an owner's) resets it to pending; a project skip of a `skippable` it declares wins over it, and the loader refuses the declaration when no gate's [on_fail target, gate) range covers the step (WFC-112)", Schema: jsonSchema{"type": "boolean"}},
	"human_mergeable":  {Type: "bool (default false): a project may declare the step's work done by a person with `steps.<project>.<step>.by: human` in its workspace yaml, fixed when the work item is first claimed; the step then asks the owner to do its work instead of running it itself, and a claim of a lane that does not declare the step human_mergeable is refused naming it", Schema: jsonSchema{"type": "boolean"}},
	"budget":           {Type: "map {wall_minutes, tool_minutes}", Schema: object(budgetKeys)},
	"model_candidates": {Type: "[]<harness>:<model>[:<effort>]; exclusive with model_tier", Schema: candidates()},
	"model_tier":       {Type: "a tier of defaults.tiers; exclusive with model_candidates", Schema: pattern(`\S`)},
	"params":           {Type: "map <name> -> a single-line string (the lane default): the step's declared parameters. <name> is ^[a-z][a-z0-9_]{0,63}$, collides with no var or fragment (WFC-195), and a parameter the definition's placeholders never use is allowed (a script reads it); a value is one line of at most 256 bytes (WFC-112). The final value is composed at EVERY dispatch — lane default < the project's steps.<project>.<step-id>.params.<name> in its workspace yaml < the work item's pf override — fills the definition's {{<name>}} and reaches the step's scripts as PF_PARAM_<NAME>", Schema: jsonSchema{"type": "object", "propertyNames": pattern(wrapAttrName.String()), "additionalProperties": jsonSchema{"type": "string"}}},
	"vars":             {Type: "map[string]string", Schema: jsonSchema{"type": "object", "additionalProperties": jsonSchema{"type": "string"}}},
	"vars_files":       {Type: "map <var> -> a file of the lane (fragments/<name>.md), relative to the lane directory and inside it: the var's value is the file's text, exactly as if written in vars; a var is declared in vars or in vars_files, not both", Schema: jsonSchema{"type": "object", "additionalProperties": jsonSchema{"type": "string", "minLength": 1}}},
	"skill":            {Type: "skill name: <lane>/skills/<name>.md, else _shared/skills/<name>.md, rendered into the prompt", Schema: pattern(idPattern.String())},
	"resources":        {Type: "map {admission, locks}: what the daemon takes for the step's dispatch", Schema: object(resourcesKeys)},
	"hub":              {Type: "map {verbs, attrs} (default none): what the step may call through `pf step hub`", Schema: object(hubKeys)},
	"wrap_attrs":       {Type: "[]attr name (^[a-z][a-z0-9_]{0,63}$), each at most once (default none): the wrap attrs the step registers (`pf step hub wrap-attrs <name>`) before its round may hand off done; listing one lets the step call wrap-attrs. When the steps at the lane's end are skipped (a skippable step a project switched off), their wrap_attrs are the last running step's to register", Schema: jsonSchema{"type": "array", "items": pattern(wrapAttrName.String()), "uniqueItems": true}},
	"fast_check_proof": {Type: "map {result, command} (default none): the fast-check proof this step's done needs for each task repo it changed — a `pf-step-result/1` file with verdict pass whose heads carry the repo at its current HEAD; a repo the step did not change and a fast-forward need none", Schema: object(fastCheckProofKeys)},
	"facts_proof":      {Type: "map {result, command} (default none): the mechanical facts summary this step's done needs for each task repo it changed (polyforge#241) — a `pf-step-result/1` file `facts.py handoff` writes, verdict pass whose heads carry the repo at its current HEAD; a repo the step did not change and a fast-forward need none; its refusal names every hit beside the result file", Schema: object(fastCheckProofKeys)},
}

// fastCheckProofKeys is the closed key set of `fast_check_proof`: the
// result file's path template and the command that writes a fresh one, each
// required and each carrying {repo} (the task repo's name).
var fastCheckProofKeys = map[string]keySpec{
	"result":  {Type: "string, required: the result file's path relative to the capsule, carrying {repo} (WFC-112 otherwise)", Required: true, Schema: pattern(`\{repo\}`)},
	"command": {Type: "string, required: one line carrying {repo} (WFC-112 otherwise): the command a refusal quotes to write a fresh proof", Required: true, Schema: pattern(`\{repo\}`)},
}

// onFailKeys, budgetKeys, resourcesKeys and hubKeys are the closed key
// sets of the step-level maps.
var (
	hubKeys = map[string]keySpec{
		"verbs": {Type: "[]get|list|file|wrap-attrs|batch, each at most once (default none): the `pf step hub` verbs the step may call", Schema: jsonSchema{"type": "array", "items": jsonSchema{"enum": HubVerbs}, "uniqueItems": true}},
		"attrs": {Type: "[]attrs key (^[a-z][a-z0-9_]{0,63}$), each at most once (default none): the top-level attrs keys the step may write (`pf step hub attrs <wi> --set <key>`); listing one lets the step call attrs, and the daemon refuses a write of any other key", Schema: jsonSchema{"type": "array", "items": pattern(wrapAttrName.String()), "uniqueItems": true}},
	}
	onFailKeys = map[string]keySpec{
		"target": {Type: "an earlier step id, required", Required: true, Schema: pattern(idPattern.String())},
	}
	budgetKeys = map[string]keySpec{
		"wall_minutes": {Type: "int > 0", Schema: jsonSchema{"type": "integer", "minimum": 1}},
		"tool_minutes": {Type: "int > 0, <= 180, <= wall_minutes when both are declared: the step's own tool window", Schema: jsonSchema{"type": "integer", "minimum": 1, "maximum": MaxToolMinutes}},
	}
	resourcesKeys = map[string]keySpec{
		"admission": {Type: "none|heavy (default none): a heavy-run admission slot for the dispatch", Schema: jsonSchema{"enum": []string{AdmissionNone, AdmissionHeavy}}},
		"locks":     {Type: "[]merge: the task repos' per-repo merge locks", Schema: jsonSchema{"type": "array", "items": jsonSchema{"enum": []string{LockMerge}}, "uniqueItems": true}},
	}
)

// The resources values. LockBar is no value of contract 4: the loader
// refuses it with its rewrite, LockBarRemovedHint.
const (
	AdmissionNone  = "none"
	AdmissionHeavy = "heavy"
	LockBar        = "bar"
	LockMerge      = "merge"
)

// LockBarRemovedHint is the rewrite of a refused `locks: [bar]`.
const LockBarRemovedHint = contract4 + "the bar lock is gone; delete `bar` from resources.locks (a step that runs heavy commands declares `resources: {admission: heavy}`)"

// MaxToolMinutes bounds a step's budget.tool_minutes.
const MaxToolMinutes = 180

// DefaultUncountedRewinds is `uncounted_rewinds.max`'s default: the
// lane-declared uncounted class's own budget when the lane declares the
// class without a max. The class's rewinds are cheap (each is another
// lane's landing), but an ever-moving default branch would loop a work
// item for ever, so the default asks the owner after two.
const DefaultUncountedRewinds = 2

// wrapAttrName is the shape of a wrap attr's name (the `pf step hub
// wrap-attrs` name the daemon's step-call check takes) and of an attrs key
// a step may write (`hub.attrs`, aihub's own key shape).
var wrapAttrName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ParamName is the shape of a step parameter's name (the `params` key of
// a step entry): the wrap-attr shape, so a name maps onto a legal
// PF_PARAM_<NAME> environment variable.
var ParamName = wrapAttrName

// HubVerbs are the `pf step hub` verbs a step's `hub.verbs` may name
// (design #483 §5.2; workflow contract 1): two reads and the writes made
// only when the dispatch hands off done. The attrs write is no entry: a
// step may call it when its `hub.attrs` names a key (HubAllowed).
var HubVerbs = []string{"get", "list", "file", "wrap-attrs", "batch"}

// skillKeys is the closed key set of a skill file's frontmatter
// (<lane>/skills/<name>.md, else _shared/skills/<name>.md).
var skillKeys = map[string]keySpec{
	"name":        {Type: "string, required, == file name", Required: true},
	"description": {Type: "string, required, non-empty", Required: true},
	"commands":    {Type: "[]string, required (may be empty): the pre-written commands the skill runs by name", Required: true},
}

// SkillsDir is the directory of a lane's (or the shared tree's) skills.
const SkillsDir = "skills"

// defKeys is the closed key set of a step definition file's frontmatter
// (steps/<id>.md).
var defKeys = map[string]keySpec{
	"name":        {Type: "string, required, == file name", Required: true},
	"description": {Type: "string, required, non-empty", Required: true},
}

// contract4 prefixes the rewrite of every key removed before workflow
// contract 1 (the step I/O, effects and hand-off keys: one entry, `pf step`,
// and one hand-off document, artifacts/handoff.md).
const contract4 = "removed before workflow contract 1: "

// removedKeys are the keys removed before contract 1, keyed by the mapping they
// sat in ("step", "on_fail", "definition"), each with its rewrite. A
// removed key is WFC-111 like any unknown key (a definition's is WFC-191);
// the hint says what to write instead.
// Any other key an earlier release had is an unknown key: a lane declaring
// it targets an earlier contract, refused for its version (WFC-101).
var removedKeys = map[string]map[string]string{
	"step": {
		"inputs":   contract4 + "delete the key; every step reads artifacts/handoff.md first, and its skill (or definition) names the files it reads before it starts",
		"verifies": contract4 + "delete the key; the step's skill says what it reads and checks of its upstream before it starts",
		"outputs":  contract4 + "delete the key; the step hands off with `pf step handoff` (a body, the --evidence files), and its skill says what the hand-off carries",
		"verdicts": contract4 + "delete the key; a gate hands off done (its pass) or fail, and a verdict a later command needs is in that command's result file",
		"effects":  contract4 + "delete the key; the step registers its aihub writes with `pf step hub <verb>`, and its `hub:` names the verbs it may call and the attrs keys it may write",
		"handoff":  contract4 + "delete the key; the step registers the record its wrap carries with `pf step hub wrap-attrs <name> @<file>` and declares `wrap_attrs: [<name>]` (its done is refused until the record is registered)",
		"attrs":    contract4 + "delete the key, its material (attrs/<name>.md or _shared/attr_<name>.md) and the workspace yaml's steps.<project>.<step>.attrs overrides",
	},
	"on_fail": {
		"charge": contract4 + "delete the key; a FAIL is charged to max_rewinds unless the gate hands it off with --per-owner (the owner's word)",
	},
	"definition": {
		"output": contract4 + "delete the output schema; the step hands off with `pf step handoff`, and its skill (or this definition) says what the hand-off carries",
	},
}

// Properties is the lane-property registry: each property and who reads
// it under this contract.
var Properties = map[string]string{
	"acceptance":      "whether a work item on the lane must list an acceptance criterion (`## Acceptance` or `## 验收`) before it runs (absent = required; none = the lane has no criterion to list): pf submit refuses an item without one only on a lane that requires it, the daemon's auto claim skips such an item only there, pf queue (and the Pi Web list, which reads its acceptance_missing) marks it only there, and pf file create prints the carried-by hint only there; the pre-submit analysis check (a `## 分析` / `## Analysis` section with content) applies on the same lanes — refused by pf submit, skipped by the auto claim, marked 需要交互：未分析 by pf queue; a lane the reader cannot load counts as required; a queued or blocked execution ticket's analysis can be re-scheduled with `pf file analysis redo`, the old `## 分析` text kept verbatim in `## 历次分析` and the new draft unconfirmed again, with the strong guarantee local to that daemon",
	"children_branch": "the branch-name template the daemon's claim of a sub-ticket reads from the PARENT's lane (the lane of the work item its `parent_id` names — never the sub-ticket's own): {seq} is the parent's seq and {project} its project name; the sub-ticket's first claim fixes the substituted branch into runtime-state/target-branch.json before the task repos are cloned, every (re-)claim checks the fixed value out (the carrier restore included) and the daemon creates the branch on each origin from the default branch tip with a non-forced push when the remote does not carry it; a claim that cannot read the parent or its lane is refused naming why; a parent whose lane declares none leaves the sub-ticket untouched",
	"epic":            "the lane's own work items are epics: the lane must also declare children_branch and resume_reset (the loader refuses the key without them, WFC-112); a first claim fixes the children branch substituted with the work item's own seq into runtime-state/target-branch.json as the record's checkout key — no target key, so every reader keeps its default-branch logic and the epic merges into the origin's default branch — and snapshots the item's children into runtime-state/children.json, which the driver re-checks against aihub before every dispatch; a sub-ticket's claim reads the parent's lane exactly as before",
	"resume_reset":    "the step id an owner-resumed epic's changed-children marker (runtime-state/epic-resume.json) resets the lane to — required when the lane declares epic and refused without it (WFC-112), and it must name a step of this lane: the runtime writes it into the marker and the driver lands it as one owner-class rewind, charged to no budget; the Go code names no step id (workflow contract ⑥)",
	"exclusive":       "one unfinished work item per project on the lane: the daemon's data-plane proxy reads it in the lane set of the project's workspace and forwards a work-item create on the lane (`pf file create --bundle <lane>`) to aihub exclusive, refused CORE-409 OPEN_ON_BUNDLE while another work item of the project on the lane is not wrapped, failed or canceled; nothing else reads it",
}

// sortedKeys answers m's keys, sorted.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RegistryDump renders the whole registry as stable text: the golden
// file contract.v<ContractVersion>.golden pins it, and its last two
// sections show mechanically what an incompatible change moves — every
// required key (`[required]`, `<set>.<key>`) and every closed value
// (`[values]`, `<set>.<key>: <value>`) — so the pin tells a compatible
// change (regenerated in place, the version stays) from an incompatible
// one (registered in contract-pending.md; compat.go).
func RegistryDump() string {
	var b strings.Builder
	fmt.Fprintf(&b, "contract %d\n", ContractVersion)
	section := func(title string, keys map[string]keySpec) {
		fmt.Fprintf(&b, "\n[%s]\n", title)
		for _, k := range sortedKeys(keys) {
			fmt.Fprintf(&b, "%s: %s\n", k, keys[k].Type)
		}
	}
	for _, ks := range keySets() {
		section(ks.name+" keys", ks.keys)
	}
	b.WriteString("\n[removed keys]\n")
	for _, where := range sortedKeys(removedKeys) {
		for _, k := range sortedKeys(removedKeys[where]) {
			fmt.Fprintf(&b, "%s.%s: %s\n", where, k, removedKeys[where][k])
		}
	}
	b.WriteString("\n[properties]\n")
	for _, k := range sortedKeys(Properties) {
		fmt.Fprintf(&b, "%s: %s\n", k, Properties[k])
	}
	b.WriteString("\n[placeholders]\n")
	b.WriteString("{{<var>}}: the step's vars (vars, vars_files)\n")
	b.WriteString("{{<param>}}: the step's params (params), at the dispatch's final value\n")
	b.WriteString("{{<fragment>}}: " + SharedDir + "/<fragment>.md, inserted verbatim\n")
	b.WriteString("\n[error codes]\n")
	for _, c := range sortedKeys(Codes) {
		fmt.Fprintf(&b, "%s: %s\n", c, Codes[c])
	}
	b.WriteString("\n[required]\n")
	for _, ks := range keySets() {
		for _, k := range sortedKeys(ks.keys) {
			if ks.keys[k].Required {
				fmt.Fprintf(&b, "%s.%s\n", ks.name, k)
			}
		}
	}
	b.WriteString("\n[values]\n")
	for _, ks := range keySets() {
		for _, k := range sortedKeys(ks.keys) {
			for _, v := range enumValues(ks.keys[k].Schema) {
				fmt.Fprintf(&b, "%s.%s: %s\n", ks.name, k, v)
			}
		}
	}
	return b.String()
}

// keySet is one closed key set of the contract and its name in the dump.
type keySet struct {
	name string
	keys map[string]keySpec
}

// keySets answers every closed key set, in the dump's order.
func keySets() []keySet {
	return []keySet{
		{"lane", laneKeys}, {"defaults", defaultsKeys}, {"step", stepKeys},
		{"fast_check_proof", fastCheckProofKeys},
		{"facts_proof", fastCheckProofKeys},
		{"on_fail", onFailKeys}, {"resources", resourcesKeys}, {"hub", hubKeys},
		{"budget", budgetKeys}, {"definition", defKeys}, {"skill", skillKeys},
	}
}

// enumValues answers the closed values a key's schema allows: its enum,
// or its array items' enum (none when its values are open).
func enumValues(s jsonSchema) []string {
	if items, ok := s["items"].(jsonSchema); ok {
		s = items
	}
	vs, _ := s["enum"].([]string)
	return vs
}
