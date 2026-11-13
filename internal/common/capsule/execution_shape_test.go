package capsule

// execution_shape_test.go (workflow contract 4 C3):
// execution.json is the daemon's progress record alone — its schema
// carries no step content and no hand-off: none of contract 3's output,
// pending_rewind_input, envelope_rejects, owner_rulings, effects,
// integration or integration_batch (nor the records, the delivery
// waivers, review_rounds and termination_reason that rode with them), at
// any depth of the record.

import (
	"reflect"
	"strings"
	"testing"
)

// goneExecutionKeys are the keys workflow contract 4 removed from
// execution.json.
var goneExecutionKeys = []string{
	"output", "pending_rewind_input", "envelope_rejects", "envelope_reject_spawn_n", "owner_rulings",
	"effects", "integration", "integration_batch", "record", "delivery", "review_rounds", "termination_reason",
}

// jsonKeys answers every JSON key of t's fields, at any depth (struct,
// pointer, slice and map element types), each once.
func jsonKeys(t reflect.Type, seen map[reflect.Type]bool, out map[string]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" {
			name = f.Name
		}
		if name != "-" {
			out[name] = t.Name() + "." + f.Name
		}
		jsonKeys(f.Type, seen, out)
	}
}

func TestExecutionRecordCarriesNoStepContent(t *testing.T) {
	keys := map[string]string{}
	jsonKeys(reflect.TypeOf(Execution{}), map[reflect.Type]bool{}, keys)
	if keys["workflow_status"] == "" || keys["rewind_stats"] == "" || keys["start_heads"] == "" {
		t.Fatalf("the walk reads the record's keys: %v", keys)
	}
	for _, gone := range goneExecutionKeys {
		if field, ok := keys[gone]; ok {
			t.Errorf("execution.json carries %q again (%s): it is the daemon's progress record, never a step's content or hand-off", gone, field)
		}
	}
}
