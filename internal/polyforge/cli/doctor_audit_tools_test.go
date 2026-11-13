package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorAuditToolsCheck: both tools present pass; each absent tool is
// named with its install command, warn at most.
func TestDoctorAuditToolsCheck(t *testing.T) {
	look := func(present ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, p := range present {
				if p == name {
					return "/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	if c := auditToolsCheck(look("npm", "govulncheck")); c.Status != "pass" || c.Name != "audit tools" {
		t.Fatalf("both tools present: %+v", c)
	}
	c := auditToolsCheck(look("npm"))
	if c.Status != "warn" || !strings.Contains(c.Detail, "govulncheck — go install golang.org/x/vuln/cmd/govulncheck@v1.9.0") || strings.Contains(c.Detail, "npm —") {
		t.Fatalf("govulncheck absent: %+v", c)
	}
	c = auditToolsCheck(look())
	if c.Status != "warn" || !strings.Contains(c.Detail, "npm — install Node.js with npm") || !strings.Contains(c.Detail, "govulncheck —") {
		t.Fatalf("both absent: %+v", c)
	}
}

// TestDoctorAuditToolsRow: the full doctor reports the row, and a missing
// audit tool never moves `ok` or the exit code.
func TestDoctorAuditToolsRow(t *testing.T) {
	env := doctorServerConfig(t, "http://aihub.invalid:1", "k", "")
	withPath := func(tools ...string) map[string]string {
		dir := t.TempDir()
		for _, tool := range tools {
			if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		out := map[string]string{}
		for k, v := range env {
			out[k] = v
		}
		out["PATH"] = dir
		return out
	}
	base := []string{"gh", "curl", "python3", "tmux", "git"}
	codeAll, _, docAll := doctorStatus(t, withPath(append(base, "npm", "govulncheck")...), "--json", "doctor")
	if st, detail := doctorFind(t, docAll, "audit tools"); st != "pass" {
		t.Fatalf("audit tools with both on PATH: %q (%q)", st, detail)
	}
	codeNone, _, docNone := doctorStatus(t, withPath(base...), "--json", "doctor")
	st, detail := doctorFind(t, docNone, "audit tools")
	if st != "warn" || !strings.Contains(detail, "npm —") || !strings.Contains(detail, "govulncheck —") {
		t.Fatalf("audit tools with neither on PATH: %q (%q)", st, detail)
	}
	if codeNone != codeAll || docNone.OK != docAll.OK {
		t.Fatalf("a missing audit tool must not move the exit code or ok: exit %d→%d ok %t→%t", codeAll, codeNone, docAll.OK, docNone.OK)
	}
}
