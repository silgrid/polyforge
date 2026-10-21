package runtime

// manualmodels.go: the daemon-local record of `pf submit --models` lists.
//
// The list is a local operator choice, so it lives under the daemon's
// data dir — never in the capsule (the agent writes there, and the
// capsule travels to other machines with the WI). One file maps work
// item id → list; it survives a restart and a blocked end (the re-claim
// reads it back) and is removed when the WI ends terminally here. A
// re-claim on another machine finds no entry and uses that machine's
// workspace yaml.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

func (r *Runtime) manualModelsPath() string {
	return filepath.Join(r.cfg.DataDir, "manual-models.json")
}

func (r *Runtime) readManualModelsFile() map[string][]string {
	m := map[string][]string{}
	raw, err := os.ReadFile(r.manualModelsPath())
	if err != nil {
		return m
	}
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return map[string][]string{}
	}
	return m
}

func (r *Runtime) writeManualModelsFile(m map[string][]string) {
	var err error
	if len(m) == 0 {
		if err = os.Remove(r.manualModelsPath()); os.IsNotExist(err) {
			err = nil
		}
	} else {
		var blob []byte
		if blob, err = json.MarshalIndent(m, "", "  "); err == nil {
			err = atomicfile.Write(r.manualModelsPath(), blob, stateFilePerm)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: manual models record write failed: %v\n", err)
	}
}

func (r *Runtime) saveManualModels(wi string, models []string) {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()
	m := r.readManualModelsFile()
	m[wi] = models
	r.writeManualModelsFile(m)
}

func (r *Runtime) loadManualModels(wi string) []string {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()
	return r.readManualModelsFile()[wi]
}

func (r *Runtime) clearManualModels(wi string) {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()
	m := r.readManualModelsFile()
	if _, ok := m[wi]; !ok {
		return
	}
	delete(m, wi)
	r.writeManualModelsFile(m)
}
