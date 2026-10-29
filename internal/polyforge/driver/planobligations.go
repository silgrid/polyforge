// planobligations.go: the plan step's coupled-obligation contract
//.
//
// A plan names, for every file it declares with `write` intent, the
// documents, tables and projection rules that must change with it —
// `coupled_obligations` on the touched_files entry: a non-empty list of
// `{path, note?}` entries, or exactly one `{none: true, reason}` entry.
// A completed plan result that declares a write without a well-formed
// obligation entry is an envelope rejection (the in-process retry lane),
// so the build step never runs on it. The rest of the plan's declared
// output schema stays record-only like every non-release step; this
// check is the one plan contract that is enforced. Whether the listed
// obligations are the right ones stays review's judgment.
package driver

import (
	"fmt"
	"strings"
)

// planStepID is the step that declares the touched files and their
// coupled obligations (the chore and feature-full lanes).
const planStepID = "plan"

// checkPlanObligations answers an error naming the first declared write
// whose coupled_obligations entry is missing or malformed; nil when every
// write carries one. Read entries owe nothing, and an output without a
// touched_files list is left to the recorded schema check.
func checkPlanObligations(out map[string]any) error {
	files, _ := out["touched_files"].([]any)
	for i, it := range files {
		f, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if intent, _ := f["intent"].(string); intent != "write" {
			continue
		}
		path, _ := f["path"].(string)
		if err := checkObligationList(f["coupled_obligations"]); err != nil {
			return fmt.Errorf("touched_files[%d] declares the write %q %v — every write names the documents, tables and projection rules that must change with it as coupled_obligations [{\"path\": ..., \"note\": ...}], or exactly one {\"none\": true, \"reason\": ...}",
				i, path, err)
		}
	}
	return nil
}

// checkObligationList judges one write's coupled_obligations value.
func checkObligationList(v any) error {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return fmt.Errorf("without a coupled_obligations entry")
	}
	for k, it := range list {
		o, ok := it.(map[string]any)
		if !ok {
			return fmt.Errorf("with coupled_obligations[%d] that is not an object", k)
		}
		if none, _ := o["none"].(bool); none {
			if reason, _ := o["reason"].(string); strings.TrimSpace(reason) == "" {
				return fmt.Errorf("with a none coupled obligation that gives no reason")
			}
			if len(list) != 1 {
				return fmt.Errorf("with a none coupled obligation beside other entries")
			}
			continue
		}
		if p, _ := o["path"].(string); strings.TrimSpace(p) == "" {
			return fmt.Errorf("with coupled_obligations[%d] carrying neither a path nor none with a reason", k)
		}
	}
	return nil
}
