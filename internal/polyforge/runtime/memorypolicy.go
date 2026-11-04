package runtime

// memorypolicy.go: the claimed project's effective memory policy, written
// into the capsule at claim (capsule.MemoryPolicyJSON) for the driver's
// wrap prompt. The source is the daemon's loaded cli.toml snapshot — the
// [memory_policy."<path>".<entry>] block of the project's workspace path
//; a path without a block is the built-in default. A job
// that already claimed keeps the capsule its claim wrote.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func (r *Runtime) writeMemoryPolicy(j *job) {
	var entries map[string]workspace.PolicyEntry
	if ws, err := r.plan().WorkspaceFor(j.project); err == nil {
		entries = r.cfg.Live.MemoryPolicyOf(ws)
	}
	blob, _ := json.Marshal(workspace.MemoryPolicyFor(entries, j.project))
	if err := atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.MemoryPolicyJSON)), blob, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: memory policy write failed: %v\n", j.wi, err)
	}
}
