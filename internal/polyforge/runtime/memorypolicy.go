package runtime

// memorypolicy.go: the claimed project's effective memory policy, written
// into the capsule at claim (capsule.MemoryPolicyJSON) for the driver's
// wrap prompt. An enrichment, never a gate: the yaml was validated before
// the claim, and a re-read that fails falls back to the built-in default.

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
	var cfg *workspace.Config
	if ws, err := r.cfg.Mapping.WorkspaceFor(j.project); err == nil {
		cfg, _ = workspace.Load(ws, "", r.cfg.Mapping.ProjectsOf(ws))
	}
	blob, _ := json.Marshal(cfg.MemoryPolicyFor(j.project))
	if err := atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.MemoryPolicyJSON)), blob, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: memory policy write failed: %v\n", j.wi, err)
	}
}
