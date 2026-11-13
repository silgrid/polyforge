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

// masks are the credential shapes Mask replaces beside Patterns: the
// userinfo of a URL (`scheme://user:pass@host`, `scheme://token@host`),
// an assignment whose name says it holds a credential (`*API_KEY=`,
// `*TOKEN=`, `*SECRET=`, `*PASSWORD=`, `*PASSWD=`) and the `--password
// <value>` flag form — the shapes Pi Web masks in a command
// (pi-web/lib/polyforge-status.ts). Over-masking costs only fidelity;
// under-masking leaks, so the values are greedy.
var masks = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]+@`), "${1}***@"},
	{regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:api_key|token|secret|password|passwd)=)\S+`), "${1}***"},
	{regexp.MustCompile(`(?i)(--password\s+)\S+`), "${1}***"},
}

// Mask answers text with every credential-shaped substring replaced: each
// secret shape Patterns names by `[masked <name>]`, and the masks' shapes
// (URL userinfo, credential assignments, the password flag) by `***`. A
// text the daemon keeps or shows from a command's output (a run step's
// output tail) passes through it.
func Mask(text string) string {
	for _, m := range masks {
		text = m.re.ReplaceAllString(text, m.repl)
	}
	for _, p := range Patterns {
		text = p.Re.ReplaceAllString(text, "[masked "+p.Name+"]")
	}
	return text
}
