package workflow

// schema.go: the lane file's JSON Schema, rendered from the registry
// (registry.go) — the lane keys and each nested mapping's closed key set,
// every key's value as its keySpec.Schema states it and its type wording
// as its description. The generated file is
// workflows/_schema/workflow-<ContractVersion>.schema.json (tools/laneschema
// writes it; a test regenerates it and fails on any difference). It is an
// editor's aid: the loader alone judges a lane, and the rules across keys
// (max_rewinds with on_fail, model_tier against defaults.tiers, a var in
// both vars and vars_files, the definitions, skills and placeholders) are
// the loader's only.

//go:generate go run ../../../tools/laneschema -workflows ../../../workflows

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/silgrid/polyforge/internal/common/harness"
)

// SchemaDir is the directory beside the lanes that holds the lane schema
// (workflows/_schema/): `_`-prefixed, so no lane.
const SchemaDir = "_schema"

// SchemaFile is the lane schema's file name for contract version n.
func SchemaFile(n int) string { return fmt.Sprintf("workflow-%d.schema.json", n) }

// jsonSchema is one JSON Schema object.
type jsonSchema = map[string]any

// pattern is a string matching re (an ECMA-262 pattern the Go one shares).
func pattern(re string) jsonSchema { return jsonSchema{"type": "string", "pattern": re} }

// candidates is a model-candidate list: each entry `<harness>:<model>[:<effort>]`
// with a harness of the closed set (the loader judges the rest).
func candidates() jsonSchema {
	return jsonSchema{"type": "array", "items": pattern(`^\s*(` + harness.Claude + "|" + harness.Pi + `)\s*:\s*\S`)}
}

// object is the closed mapping of keys: each key's schema with its type
// wording as the description, the required keys, nothing else allowed.
func object(keys map[string]keySpec) jsonSchema {
	props := jsonSchema{}
	var required []string
	for _, k := range sortedKeys(keys) {
		spec := keys[k]
		p := jsonSchema{"description": spec.Type}
		for kk, v := range spec.Schema {
			p[kk] = v
		}
		props[k] = p
		if spec.Required {
			required = append(required, k)
		}
	}
	o := jsonSchema{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

// LaneSchema renders the JSON Schema (draft-07) of a workflow.yaml under
// ContractVersion: the bytes of workflows/_schema/SchemaFile(ContractVersion).
func LaneSchema() ([]byte, error) {
	s := object(laneKeys)
	s["$schema"] = "http://json-schema.org/draft-07/schema#"
	s["title"] = fmt.Sprintf("workflow.yaml (workflow contract %d)", ContractVersion)
	s["description"] = fmt.Sprintf("A lane's workflow.yaml under workflow contract %d, generated from the contract registry: each key's own value. The rules across keys, the step definitions, skills and placeholders are the lane loader's (`pf workflows validate`); docs/reference/workflow-contract.md.", ContractVersion)
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// laneFileKeySets are the key sets of the lane file, each of whose keys
// carries a Schema (the schema test holds every one to it).
func laneFileKeySets() map[string]map[string]keySpec {
	return map[string]map[string]keySpec{
		"lane": laneKeys, "defaults": defaultsKeys, "step": stepKeys,
		"on_fail": onFailKeys, "budget": budgetKeys, "resources": resourcesKeys,
		"hub": hubKeys,
	}
}
