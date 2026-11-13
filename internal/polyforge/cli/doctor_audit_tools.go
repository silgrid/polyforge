package cli

import "strings"

// doctor_audit_tools.go — the warn-only `audit tools` row: whether the tools
// the dependency-audit gate (workflows/_shared/scripts/audit-deps, appended by the verification
// engine to every full-tier bar) runs are on PATH. The gate itself is
// fail-closed — a missing tool turns the full bar red — so this row only
// gives the advance notice: it names each missing tool with its install
// command and never fails, never moves `ok` or the exit code, and the doctor
// installs nothing.

// docChAuditTools is the row name.
const docChAuditTools = "audit tools"

// auditToolOrder keeps the report's tool list stable.
var auditToolOrder = []string{"npm", "govulncheck"}

// auditToolHints is the one place stating how a missing audit tool is
// installed (the govulncheck pin matches docs/install.md).
var auditToolHints = map[string]string{
	"npm":         "install Node.js with npm (macOS: brew install node  |  Linux: apt install nodejs npm)",
	"govulncheck": "go install golang.org/x/vuln/cmd/govulncheck@v1.9.0 (then put $(go env GOPATH)/bin on PATH)",
}

// auditToolsCheck reports the audit tools' presence on PATH, warn at most.
func auditToolsCheck(lookPath func(string) (string, error)) doctorCheck {
	var missing []string
	for _, tool := range auditToolOrder {
		if _, err := lookPath(tool); err != nil {
			missing = append(missing, tool+" — "+auditToolHints[tool])
		}
	}
	if len(missing) == 0 {
		return doctorCheck{Name: docChAuditTools, Status: "pass",
			Detail: "the dependency-audit gate's tools are on PATH: " + strings.Join(auditToolOrder, ", ")}
	}
	return doctorCheck{Name: docChAuditTools, Status: "warn",
		Detail: "missing (the full-tier bar's dependency audit fails closed without it; the doctor never installs anything): " + strings.Join(missing, "; ")}
}
