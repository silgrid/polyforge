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
