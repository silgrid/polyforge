package task

// candidates.go: the task lane's model-candidate resolution. It is NOT
// part of a step's precedence chain: the task list wholly governs when
// declared, else the project default does, else the spawn refuses naming
// the missing key. Candidates rotate in order inside one shared budget;
// there is no `--models` override.

import (
	"fmt"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ResolveCandidates answers the effective candidate list of project from
// a live workspace config (nil = no yaml): `steps.<project>.task.models`
// when declared, else `steps.<project>.default.models`. A declared-but-
// empty list is a present level — it governs and is refused as empty
// (nothing may run), never silently falling through. When neither level
// is declared the error is the named refusal.
//
// Every candidate is validated as a `<harness>:<model>[:<effort>]`
// string (a yaml validated at load cannot reach here broken; the check
// keeps the resolver's own contract).
func ResolveCandidates(cfg *workspace.Config, project string) ([]string, string, error) {
	list, from := workspace.TaskModels(cfg, project)
	if list == nil {
		return nil, "", workspace.MissingTaskModelsError(project)
	}
	if len(list) == 0 {
		return nil, from, fmt.Errorf("%s declares no model candidate, so a task cannot start", from)
	}
	out := make([]string, 0, len(list))
	for i, m := range list {
		c, err := harness.ParseCandidate(m)
		if err != nil {
			return nil, from, fmt.Errorf("%s candidate %d: %w", from, i, err)
		}
		out = append(out, c.String())
	}
	return out, from, nil
}
