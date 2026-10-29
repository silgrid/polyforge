// outputschema.go: the declared step output schema (polyforge#164 H3).
//
// Every step definition (workflows/<bundle>/steps/<id>.md) declares the
// shape of its result `output` in its frontmatter `output:` block — a
// JSON Schema written in YAML. Two duties follow from that declaration
// and this file owns both:
//
//   - the prompt shows it: the schema block is injected into the step's
//     prompt ahead of the step body (the body says "per the schema
//     above"), instead of being stripped with the rest of the
//     frontmatter;
//   - the driver validates it: a completed result's output is checked
//     against the compiled schema inside ValidateStepResult. On the
//     release bundle's `release` and `deploy` steps a violation is an
//     ordinary envelope rejection (the in-process retry lane, bounded
//     into blocked result_invalid); on every other step it is recorded
//     as the durable output_schema_violation event and the result is
//     still accepted (outputSchemaEnforced). The plan step's schema
//     stays record-only too — enforcing it whole would refuse real plans
//     for unrelated violations; its one enforced contract, a write
//     without a coupled-obligation entry, is the dedicated
//     checkPlanObligations rejection (planobligations.go).
//
// A step definition without an `output:` block declares nothing and is
// not validated. Schemas are draft-07.
package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// outputSchemaKey is the step-definition frontmatter key of the schema.
const outputSchemaKey = "output"

// outputSchemaEnforced answers whether stepID's declared output schema
// is enforced: true for the release bundle's `release` and `deploy`
// steps (their schemas are the release verdict contract), false for
// every other step, whose violations are recorded, never rejected —
// replayed real outputs violate those schemas too often to enforce.
func outputSchemaEnforced(stepID string) bool {
	return stepID == releaseStepID || stepID == deployStepID
}

// EvOutputSchemaViolation is the durable event kind recording a
// completed output that violates its step's declared schema on a step
// whose schema is not enforced. EvOutputSchemaUnloadable records a
// declared schema that does not load (so nothing is validated for that
// step).
const (
	EvOutputSchemaViolation  = "output_schema_violation"
	EvOutputSchemaUnloadable = "output_schema_unloadable"
)

// stepOutputSchema is one step's declared output schema: the YAML text
// the prompt shows and the compiled form the validator runs.
type stepOutputSchema struct {
	Text   string
	Schema *jsonschema.Schema
	// node is the schema's YAML node: the pruned prompt text
	// (PromptText) is marshalled from a pruned copy of it.
	node *yaml.Node
	// marked answers whether any properties entry carries an
	// x-prompt-when marker (an unmarked schema needs no pruning pass).
	marked bool
}

// promptWhenKey is the draft-07 annotation a step's declared output
// schema may put on a `properties` entry: `x-prompt-when: <condition>`,
//. The schema keeps compiling the FULL node (unknown
// keywords are annotations); only the text the prompt shows is pruned:
// entries whose condition does not hold are dropped and the marker
// keys themselves never appear in it. Validation never sees a pruned
// schema.
const promptWhenKey = "x-prompt-when"

// promptWhenConds is the closed condition vocabulary of x-prompt-when:
// scoped (the dispatch's ScopeTier is scoped: verification's recorded
// tier, review's work-item opt-in), armed (the
// lane's verification entry declares mechanical_fix), authored (the
// lane has an e2e-author step), reentry (a review re-entry dispatch).
// An unknown value is a schema load error — fail-closed, so a typo can
// never silently hide a schema fragment.
var promptWhenConds = map[string]bool{
	"scoped": true, "armed": true, "authored": true, "reentry": true,
}

// loadStepOutputSchema reads workflows/<bundle>/steps/<id>.md (or the
// shared workflows/_shared/steps/<id>.md it falls back to), rendered
// with the step entry's vars, and answers its declared output schema;
// (nil, nil) when the definition declares none (or there is no
// definition file). A declared schema
// that does not parse or compile is an error — a bundle whose contract
// cannot be checked must say so, never validate silently against
// nothing.
func loadStepOutputSchema(bundleDir, stepID string, vars map[string]string) (*stepOutputSchema, error) {
	if bundleDir == "" {
		return nil, nil
	}
	raw, err := workflow.ReadStepDefinition(bundleDir, stepID, vars)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	front, _, ok := workflow.SplitFrontmatter(raw)
	if !ok {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, fmt.Errorf("driver: step %q frontmatter unparsable: %w", stepID, err)
	}
	node := mappingValue(&doc, outputSchemaKey)
	if node == nil {
		return nil, nil
	}
	marked, err := checkPromptWhen(node)
	if err != nil {
		return nil, fmt.Errorf("driver: step %q output schema: %w", stepID, err)
	}
	s, err := compileStepOutputSchema(stepID, node)
	if err != nil {
		return nil, err
	}
	s.marked = marked
	return s, nil
}

// loadLightOutputSchema reads the shared fast-lane material
// (workflows/_shared/mechanical-fix.md, beside the bundle) and answers
// its declared LIGHT output schema — the light recheck's result contract
// (verdict + checks, no full-bar fields; polyforge#260); (nil, nil) when
// the material is absent or declares none. A declared schema that does
// not parse or compile is an error, like any step's.
func loadLightOutputSchema(bundleDir string) (*stepOutputSchema, error) {
	raw, err := os.ReadFile(mechanicalFixFile(bundleDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	front, _, ok := workflow.SplitFrontmatter(raw)
	if !ok {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return nil, fmt.Errorf("driver: mechanical-fix.md frontmatter unparsable: %w", err)
	}
	node := mappingValue(&doc, outputSchemaKey)
	if node == nil {
		return nil, nil
	}
	marked, err := checkPromptWhen(node)
	if err != nil {
		return nil, fmt.Errorf("driver: mechanical-fix.md output schema: %w", err)
	}
	s, err := compileStepOutputSchema("verification-light", node)
	if err != nil {
		return nil, err
	}
	s.marked = marked
	return s, nil
}

// compileStepOutputSchema answers one frontmatter output block's
// compiled schema (the YAML text the prompt shows, the compiled form
// the validator runs).
func compileStepOutputSchema(stepID string, node *yaml.Node) (*stepOutputSchema, error) {
	text, err := yaml.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("driver: step %q output schema: %w", stepID, err)
	}
	var tree any
	if err := node.Decode(&tree); err != nil {
		return nil, fmt.Errorf("driver: step %q output schema: %w", stepID, err)
	}
	js, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("driver: step %q output schema is not JSON-representable: %w", stepID, err)
	}
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft7
	url := "step://" + stepID + "/output.json"
	if err := c.AddResource(url, bytes.NewReader(js)); err != nil {
		return nil, fmt.Errorf("driver: step %q output schema: %w", stepID, err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("driver: step %q output schema does not compile: %w", stepID, err)
	}
	return &stepOutputSchema{Text: strings.TrimSpace(string(text)), Schema: schema, node: node}, nil
}

// PromptText answers the schema text the step prompt shows: the full
// declared schema with every `properties` entry whose x-prompt-when
// condition does not hold dropped and the marker keys themselves never
// shown. cond is the dispatch's condition set (a marker
// whose condition is absent or false is pruned). Validation is never
// affected: it always runs against the full compiled schema. A nil
// schema answers ""; a schema with no marker answers its full text.
func (s *stepOutputSchema) PromptText(cond map[string]bool) string {
	if s == nil {
		return ""
	}
	if !s.marked || s.node == nil {
		return s.Text
	}
	pruned := prunePromptNode(s.node, cond)
	text, err := yaml.Marshal(pruned)
	if err != nil {
		return s.Text
	}
	return strings.TrimSpace(string(text))
}

// checkPromptWhen validates the closed condition vocabulary across one
// schema node (every mapping entry of promptWhenKey must name a known
// condition) and answers whether any marker is present at all.
func checkPromptWhen(n *yaml.Node) (bool, error) {
	marked := false
	var walk func(n *yaml.Node) error
	walk = func(n *yaml.Node) error {
		if n == nil {
			return nil
		}
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				if err := walk(c); err != nil {
					return err
				}
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if k.Value == promptWhenKey {
					marked = true
					if v.Kind != yaml.ScalarNode || !promptWhenConds[v.Value] {
						return fmt.Errorf("%s: %q is not in the closed condition set (scoped, armed, authored, reentry)", promptWhenKey, v.Value)
					}
				}
				if err := walk(v); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return marked, walk(n)
}

// prunePromptNode answers a deep copy of one schema node with every
// `properties` entry whose x-prompt-when condition does not hold
// dropped and every x-prompt-when key removed; the full schema the
// validator runs is never passed here.
func prunePromptNode(n *yaml.Node, cond map[string]bool) *yaml.Node {
	if n == nil {
		return nil
	}
	out := &yaml.Node{Kind: n.Kind, Style: n.Style, Tag: n.Tag, Value: n.Value}
	out.Content = make([]*yaml.Node, 0, len(n.Content))
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Value == promptWhenKey {
				continue // the marker never reaches the prompt
			}
			if k.Value == "properties" && v.Kind == yaml.MappingNode {
				props := pruneProperties(v, cond)
				if len(props.Content) == 0 {
					continue // every entry was pruned: drop the empty properties key
				}
				out.Content = append(out.Content, k, props)
				continue
			}
			out.Content = append(out.Content, k, prunePromptNode(v, cond))
		}
		return out
	}
	for _, c := range n.Content {
		out.Content = append(out.Content, prunePromptNode(c, cond))
	}
	return out
}

// pruneProperties answers a copy of one `properties` mapping with the
// entries whose x-prompt-when condition does not hold dropped.
func pruneProperties(props *yaml.Node, cond map[string]bool) *yaml.Node {
	out := &yaml.Node{Kind: props.Kind, Style: props.Style, Tag: props.Tag, Value: props.Value}
	for i := 0; i+1 < len(props.Content); i += 2 {
		k, v := props.Content[i], props.Content[i+1]
		if v.Kind == yaml.MappingNode {
			if c := promptWhenOf(v); c != "" && !cond[c] {
				continue // the condition does not hold: the entry leaves the prompt
			}
		}
		out.Content = append(out.Content, k, prunePromptNode(v, cond))
	}
	return out
}

// promptWhenOf answers a properties entry's x-prompt-when condition
// ("" when it carries none).
func promptWhenOf(entry *yaml.Node) string {
	if entry == nil || entry.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(entry.Content); i += 2 {
		if entry.Content[i].Value == promptWhenKey && entry.Content[i+1].Kind == yaml.ScalarNode {
			return entry.Content[i+1].Value
		}
	}
	return ""
}

// mappingValue answers the value node of key in a YAML document's
// top-level mapping (nil when absent).
func mappingValue(doc *yaml.Node, key string) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// dropNulls removes every object property whose value is null, at any
// depth: an agent writing `"fail_reason": null` on a pass means the
// optional property is absent, and is validated as such.
func dropNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if e == nil {
				delete(t, k)
				continue
			}
			t[k] = dropNulls(e)
		}
	case []any:
		for i, e := range t {
			t[i] = dropNulls(e)
		}
	}
	return v
}

// maxSchemaErrorLen bounds the validation detail carried by the
// output_schema_violation event or, on an enforced step, by the
// rejection fed back into the retry prompt.
const maxSchemaErrorLen = 2000

// checkOutputSchema validates one output payload against the declared
// schema. An absent payload validates as the empty object.
func checkOutputSchema(s *jsonschema.Schema, output json.RawMessage) error {
	raw := bytes.TrimSpace(output)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("output is not valid JSON: %w", err)
	}
	v = dropNulls(v)
	if err := s.Validate(v); err != nil {
		detail := err.Error()
		if ve, ok := err.(*jsonschema.ValidationError); ok {
			detail = fmt.Sprintf("%#v", ve)
		}
		detail = strings.TrimSpace(detail)
		if len(detail) > maxSchemaErrorLen {
			cut := maxSchemaErrorLen
			for cut > 0 && !utf8.RuneStart(detail[cut]) {
				cut-- // never split a UTF-8 rune
			}
			detail = detail[:cut] + " …"
		}
		return fmt.Errorf("output does not match the step's declared output schema (shown in your prompt under \"Output schema\"): %s", detail)
	}
	return nil
}
