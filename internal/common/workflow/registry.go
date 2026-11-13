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
	"contract":    {Type: "int, required, == ContractVersion", Required: true, Schema: jsonSchema{"const": ContractVersion}},
	"name":        {Type: "string, required, ^[a-z][a-z0-9-]{0,39}$, == directory name", Required: true, Schema: pattern(idPattern.String())},
	"description": {Type: "string, required, non-empty", Required: true, Schema: pattern(`\S`)},
	"max_rewinds": {Type: "int >= 0; required when any step declares on_fail, refused otherwise", Schema: jsonSchema{"type": "integer", "minimum": 0}},
	"acceptance":  {Type: "required|none (default required): whether a work item on the lane must list an acceptance criterion before it runs", Schema: jsonSchema{"enum": []string{AcceptanceNone, AcceptanceRequired}}},
	"exclusive":   {Type: "bool (default false): one unfinished work item per project on the lane — a create on it reaches aihub exclusive, refused while another is not wrapped, failed or canceled", Schema: jsonSchema{"type": "boolean"}},
	"defaults":    {Type: "map {model_candidates, tiers}", Schema: object(defaultsKeys)},
	"steps":       {Type: "[]step, required, non-empty", Required: true, Schema: jsonSchema{"type": "array", "minItems": 1, "items": object(stepKeys)}},
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
	"budget":           {Type: "map {wall_minutes, tool_minutes}", Schema: object(budgetKeys)},
	"model_candidates": {Type: "[]<harness>:<model>[:<effort>]; exclusive with model_tier", Schema: candidates()},
	"model_tier":       {Type: "a tier of defaults.tiers; exclusive with model_candidates", Schema: pattern(`\S`)},
	"vars":             {Type: "map[string]string", Schema: jsonSchema{"type": "object", "additionalProperties": jsonSchema{"type": "string"}}},
	"vars_files":       {Type: "map <var> -> a file of the lane (fragments/<name>.md), relative to the lane directory and inside it: the var's value is the file's text, exactly as if written in vars; a var is declared in vars or in vars_files, not both", Schema: jsonSchema{"type": "object", "additionalProperties": jsonSchema{"type": "string", "minLength": 1}}},
	"skill":            {Type: "skill name: <lane>/skills/<name>.md, else _shared/skills/<name>.md, rendered into the prompt", Schema: pattern(idPattern.String())},
	"resources":        {Type: "map {admission, locks}: what the daemon takes for the step's dispatch", Schema: object(resourcesKeys)},
	"hub":              {Type: "map {verbs, attrs} (default none): what the step may call through `pf step hub`", Schema: object(hubKeys)},
	"wrap_attrs":       {Type: "[]attr name (^[a-z][a-z0-9_]{0,63}$), each at most once (default none): the wrap attrs the step registers (`pf step hub wrap-attrs <name>`) before its round may hand off done; listing one lets the step call wrap-attrs. When the steps at the lane's end are skipped (a skippable step a project switched off), their wrap_attrs are the last running step's to register", Schema: jsonSchema{"type": "array", "items": pattern(wrapAttrName.String()), "uniqueItems": true}},
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

// wrapAttrName is the shape of a wrap attr's name (the `pf step hub
// wrap-attrs` name the daemon's step-call check takes) and of an attrs key
// a step may write (`hub.attrs`, aihub's own key shape).
var wrapAttrName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// HubVerbs are the `pf step hub` verbs a step's `hub.verbs` may name
// (design #483 §5.2; workflow contract 5): two reads and the writes made
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

// contract4 prefixes the rewrite of every key workflow contract 4 removed
// (the step I/O, effects and hand-off keys: one entry, `pf step`, and one
// hand-off document, artifacts/handoff.md; docs/upgrade.md).
const contract4 = "removed in workflow contract 4: "

// removedKeys are the keys contract 4 removed, keyed by the mapping they
// sat in ("step", "on_fail", "definition"), each with its rewrite. A
// removed key is WFC-111 like any unknown key (a definition's is WFC-191);
// the hint says what to write instead.
// A key an earlier contract removed is an unknown key: a lane declaring
// it targets an earlier contract, refused for its version (WFC-101).
var removedKeys = map[string]map[string]string{
	"step": {
		"inputs":   contract4 + "delete the key; every step reads artifacts/handoff.md first, and its skill (or definition) names the files it reads before it starts",
		"verifies": contract4 + "delete the key; the step's skill says what it reads and checks of its upstream before it starts",
		"outputs":  contract4 + "delete the key; the step hands off with `pf step handoff` (a body, the --evidence files), and its skill says what the hand-off carries",
		"verdicts": contract4 + "delete the key; a gate hands off done (its pass) or fail, and a verdict a later command needs is in that command's result file",
		"effects":  contract4 + "delete the key; the step registers its aihub writes with `pf step hub <verb>`, and its `hub:` names the verbs it may call and the attrs keys it may write",
		"handoff":  contract4 + "delete the key; the step registers the record its wrap carries with `pf step hub wrap-attrs <name> @<file>` and declares `wrap_attrs: [<name>]` (its done is refused until the record is registered)",
		"attrs":    contract4 + "delete the key, its material (attrs/<name>.md or _shared/attr_<name>.md) and the workspace yaml's steps.<project>.<step>.attrs overrides (docs/upgrade.md)",
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
	"acceptance": "whether a work item on the lane must list an acceptance criterion (`## Acceptance` or `## 验收`) before it runs (absent = required; none = the lane has no criterion to list): pf submit refuses an item without one only on a lane that requires it, the daemon's auto claim skips such an item only there, pf queue (and the Pi Web list, which reads its acceptance_missing) marks it only there, and pf file create prints the carried-by hint only there; a lane the reader cannot load counts as required",
	"exclusive":  "one unfinished work item per project on the lane: the daemon's data-plane proxy reads it in the lane set of the project's workspace and forwards a work-item create on the lane (`pf file create --bundle <lane>`) to aihub exclusive, refused CORE-409 OPEN_ON_BUNDLE while another work item of the project on the lane is not wrapped, failed or canceled; nothing else reads it",
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
