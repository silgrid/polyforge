package cli

// doctor_model_health.go: the additive `model health` doctor row
// (workbench#14). It follows the scoped-gates readiness precedent: warn at
// most, never `fail`, never moves `ok` or the exit code, `skip` when its
// input cannot be read, and absent from `--harness` (which skips
// doctorChecks wholesale). It reads only the GET /status summary the
// socket probe already made.

import (
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
)

// modelHealthCheck judges the daemon's additive model_health summary.
func modelHealthCheck(r *doctorRun) (string, string) {
	if !r.sockOK {
		return "skip", "not run: the daemon does not answer — start it: pf daemon ensure"
	}
	s := r.status.ModelHealth
	if s == nil {
		return "skip", "not run: the daemon reported no model-health summary (an older daemon?)"
	}
	if len(s.Warnings) == 0 {
		return "pass", "no model-health warnings over the events-retention window"
	}
	var rows []string
	for _, w := range s.Warnings {
		step := w.Step
		if step == "" {
			step = "-"
		}
		numbers := fmt.Sprintf("%d model death(s)", w.ModelDeaths)
		if w.Dispatches > 0 {
			numbers += fmt.Sprintf(" / %d dispatch(es), %.1f%%", w.Dispatches, w.Rate*100)
		}
		rows = append(rows, fmt.Sprintf("%s step %s: %s (%s)",
			modelhealth.Key(w.Model, w.Effort), step, numbers, w.Rule))
	}
	return "warn", "unhealthy model(s): " + strings.Join(rows, "; ") +
		" — advisory only (no dispatch is reordered or blocked); see `pf ops models`"
}
