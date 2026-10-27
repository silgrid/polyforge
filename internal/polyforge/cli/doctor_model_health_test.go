package cli

// doctor_model_health_test.go: workbench#14 additive `model health` row —
// warn at most, skip when the daemon is down or reports no summary, and
// out of the harness-only report.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
)

func doctorModelHealthRow(r *doctorRun) doctorCheck {
	st, detail := modelHealthCheck(r)
	return doctorCheck{Name: docChModelHealth, Status: st, Detail: detail}
}

func TestDoctorModelHealthRow(t *testing.T) {
	down := doctorModelHealthRow(&doctorRun{sockOK: false})
	if down.Status != "skip" || !strings.Contains(down.Detail, "pf daemon ensure") {
		t.Fatalf("daemon down must be skip with the ensure remediation, got %+v", down)
	}

	older := doctorModelHealthRow(&doctorRun{sockOK: true})
	if older.Status != "skip" || !strings.Contains(older.Detail, "older daemon") {
		t.Fatalf("a daemon without the summary must be skip, never a faked pass, got %+v", older)
	}

	clean := doctorModelHealthRow(&doctorRun{sockOK: true, status: StatusResponse{
		ModelHealth: &modelhealth.Summary{Warnings: []modelhealth.Warning{}},
	}})
	if clean.Status != "pass" {
		t.Fatalf("no warnings must pass, got %+v", clean)
	}

	warn := doctorModelHealthRow(&doctorRun{sockOK: true, status: StatusResponse{
		ModelHealth: &modelhealth.Summary{Warnings: []modelhealth.Warning{
			{Model: "glm-5.3-flash", Rule: modelhealth.RuleDeaths24h, ModelDeaths: 3},
		}},
	}})
	if warn.Status != "warn" || !strings.Contains(warn.Detail, "glm-5.3-flash") {
		t.Fatalf("a warning must name the model, got %+v", warn)
	}
	if strings.Contains(warn.Status, "fail") {
		t.Fatalf("the row must never fail")
	}
	// Warn does not move ok: only fail counts.
	if n := countFails(doctorReport{Checks: []doctorCheck{{Name: docChModelHealth, Status: warn.Status}}}); n != 0 {
		t.Fatalf("a warn must not count as a failing check: %d", n)
	}
}

func TestDoctorModelHealthRowIsRegisteredAndOutOfHarness(t *testing.T) {
	found := false
	for _, d := range doctorChecks {
		if d.name == docChModelHealth {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the model health row must be registered in doctorChecks: %+v", doctorChecks)
	}
	// --harness runs harnessDisplayChecks only; none of them is the row.
	for _, c := range harnessDisplayChecks() {
		if strings.Contains(strings.ToLower(c.Name), "model health") {
			t.Fatalf("--harness must not run the model health row: %+v", c)
		}
	}
}
