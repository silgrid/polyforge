package cli

import (
	"strings"
	"testing"
)

func TestConfigEditBytePreserving(t *testing.T) {
	orig := "# top comment\n\nserver = \"http://old\"\n# keep me\nuser_id = \"u1\"\n\n" +
		"[workspace.\"/srv/ws\"]\nprojects = [\"a\"]\n"
	c := newConfigLines(orig)
	c.setFlat("server", "http://new")
	got := c.String()
	if n := len(strings.Split(orig, "\n")); n != len(strings.Split(got, "\n")) {
		t.Fatalf("a flat set changed the line count: %d -> %d", n, len(strings.Split(got, "\n")))
	}
	for _, keep := range []string{"# top comment", "# keep me", `user_id = "u1"`, `[workspace."/srv/ws"]`} {
		if !strings.Contains(got, keep) {
			t.Fatalf("a set dropped %q:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, `server = "http://new"`) {
		t.Fatalf("server not updated:\n%s", got)
	}
}

func TestConfigEditWholeSectionAndDaemon(t *testing.T) {
	orig := "server = \"http://x\"\n\n[workspace.\"/srv/a\"]\nprojects = [\"p\"]\n\n[workspace.\"/srv/b\"]\nprojects = [\"q\"]\n"
	c := newConfigLines(orig)
	c.setWorkspace("/srv/a", []string{"p", "r"})
	c.unsetWorkspace("/srv/b")
	got := c.String()
	if !strings.Contains(got, `projects = ["p", "r"]`) {
		t.Fatalf("workspace section not replaced as a unit:\n%s", got)
	}
	if strings.Contains(got, "/srv/b") {
		t.Fatalf("unset workspace left the section behind:\n%s", got)
	}
	// a daemon table is created at the end and then edited in place
	c.setDaemon("max_concurrent_jobs", "6")
	c.setDaemon("step_timeout", `"45m"`)
	got = c.String()
	if !strings.Contains(got, "[daemon]\nmax_concurrent_jobs = 6\nstep_timeout = \"45m\"") {
		t.Fatalf("daemon table shape:\n%s", got)
	}
	c.setDaemon("max_concurrent_jobs", "2")
	c.unsetDaemon("step_timeout")
	got = c.String()
	if !strings.Contains(got, "max_concurrent_jobs = 2") || strings.Contains(got, "step_timeout") {
		t.Fatalf("daemon set/unset:\n%s", got)
	}
	// the result still parses through the one parser
	if _, err := ParseConfigBytes("/tmp/cli.toml", []byte(got)); err != nil {
		t.Fatalf("edited text does not parse: %v\n%s", err, got)
	}
}

func TestParseProjectsValue(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"projects=a,b", []string{"a", "b"}},
		{"a, b ,c", []string{"a", "b", "c"}},
		{"projects=[]", []string{}},
		{"projects=", []string{}},
	} {
		got, err := parseProjectsValue(c.in)
		if err != nil {
			t.Fatalf("parseProjectsValue(%q): %v", c.in, err)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Fatalf("parseProjectsValue(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := parseProjectsValue("goal=x"); err == nil {
		t.Fatal("a non-projects section key must refuse")
	}
}

// tableEditBase is a cli.toml whose tables the summarize/memory_policy
// editor must keep byte-identical outside the addressed statement.
const tableEditBase = `# keep this comment
server = "http://x"

[workspace."/ws/a"]
projects = ["alpha"]

[summarize]
models = [
  "pi:m-a",
  "claude:m-b",
]
# summarize comment

[memory_policy."/ws/a".default]
record = ["base"]
skip = []
`

func TestConfigEditSummarizeTable(t *testing.T) {
	c := newConfigLines(tableEditBase)
	c.setSummarize("language", `"Japanese"`)
	got := c.String()
	if !strings.Contains(got, "# summarize comment\nlanguage = \"Japanese\"\n\n[memory_policy") {
		t.Fatalf("language not inserted at the end of the table body:\n%s", got)
	}
	if !strings.HasPrefix(got, tableEditBase[:strings.Index(tableEditBase, "# summarize comment")]) {
		t.Fatalf("an insert rewrote earlier bytes:\n%s", got)
	}
	c.setSummarize("models", `["pi:m-c"]`)
	got = c.String()
	if strings.Contains(got, "claude:m-b") || !strings.Contains(got, "[summarize]\nmodels = [\"pi:m-c\"]\n# summarize comment") {
		t.Fatalf("a multi-line models statement was not replaced whole:\n%s", got)
	}
	if _, err := ParseConfigBytes("/tmp/cli.toml", []byte(got)); err != nil {
		t.Fatalf("the edited file does not parse: %v\n%s", err, got)
	}
	c.unsetSummarize("language")
	c.unsetSummarize("models")
	got = c.String()
	if strings.Contains(got, "[summarize]") || !strings.Contains(got, "[memory_policy.\"/ws/a\".default]") || !strings.Contains(got, "# keep this comment") {
		t.Fatalf("an emptied table must go with its header, nothing else:\n%s", got)
	}
	fresh := newConfigLines("server = \"http://x\"\n")
	fresh.setSummarize("models", `["pi:a"]`)
	if fresh.String() != "server = \"http://x\"\n\n[summarize]\nmodels = [\"pi:a\"]\n" {
		t.Fatalf("an absent table must be appended, got %q", fresh.String())
	}
}

func TestConfigEditPolicyEntry(t *testing.T) {
	c := newConfigLines(tableEditBase)
	c.setPolicyList("/ws/a", "alpha", "record", []string{"alpha note, with comma"})
	got := c.String()
	if !strings.HasPrefix(got, tableEditBase) || !strings.HasSuffix(got, "\n[memory_policy.\"/ws/a\".alpha]\nrecord = [\"alpha note, with comma\"]\n") {
		t.Fatalf("a new entry must be appended keeping every byte:\n%s", got)
	}
	cfg, err := ParseConfigBytes("/tmp/cli.toml", []byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if r := cfg.MemoryPolicy["/ws/a"]["alpha"].Record; len(r) != 1 || r[0] != "alpha note, with comma" {
		t.Fatalf("round trip = %#v", r)
	}
	c.setPolicyList("/ws/a", "default", "skip", []string{"secret"})
	if got := c.String(); !strings.Contains(got, "record = [\"base\"]\nskip = [\"secret\"]\n") {
		t.Fatalf("a list set must replace exactly its line:\n%s", got)
	}
	c.unsetPolicyList("/ws/a", "alpha", "record")
	if got := c.String(); strings.Contains(got, ".alpha]") || strings.Contains(got, "alpha note") {
		t.Fatalf("an emptied entry must go with its header:\n%s", got)
	}
	if policyHeader("/ws/a", "odd.name") != `[memory_policy."/ws/a"."odd.name"]` {
		t.Fatalf("a non-bare entry name must be quoted: %s", policyHeader("/ws/a", "odd.name"))
	}
}
