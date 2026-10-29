// Package secretpat is the one secret-shape scanner aihub enforces before
// a message row is stored (CORE-422). The patterns are the
// plan's list: polyforge key prefixes, 64-hex runs, PEM private-key
// blocks, and common cloud/LLM key shapes.
package secretpat

import "regexp"

// Pattern is one named secret shape.
type Pattern struct {
	Name string
	Re   *regexp.Regexp
}

// Patterns in scan order.
var Patterns = []Pattern{
	{"polyforge_key", regexp.MustCompile(`pf_k1_[A-Za-z0-9]{16,}`)},
	{"polyforge_short_key", regexp.MustCompile(`\bkey_[A-Za-z0-9]{12}`)},
	{"pem_private_key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"openai_key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)},
	{"anthropic_key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"aws_access_key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github_token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{"slack_token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"google_api_key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`)},
	{"hex64", regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)},
}

// Scan answers the first matching pattern name, or ("", false) for clean
// text. It runs before any conversation or message write.
func Scan(text string) (string, bool) {
	for _, p := range Patterns {
		if p.Re.MatchString(text) {
			return p.Name, true
		}
	}
	return "", false
}
