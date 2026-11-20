package runtime

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

func TestBuiltInSubagentExtensionIsSequential(t *testing.T) {
	data, err := stepagentassets.FS.ReadFile("extensions/subagent/index.ts")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(data), `executionMode: "sequential"`) {
		t.Fatal("the built-in subagent tool must serialize concurrent calls")
	}
}
