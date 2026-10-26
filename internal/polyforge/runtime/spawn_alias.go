package runtime

// spawn_alias.go: the shared spawn contract lives in internal/common/spawn;
// these aliases keep the runtime's existing names working.

import (
	"github.com/silgrid/polyforge/internal/common/spawn"
)

type SpawnParams = spawn.Params
type Spawner = spawn.Spawner
type AgentHandle = spawn.Handle
