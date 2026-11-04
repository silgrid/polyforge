package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

const tablesWS = "/ws/alpha"

func tablesConfig(extra string) string {
	return "server = \"http://127.0.0.1:1\"\n\n[workspace.\"" + tablesWS + "\"]\nprojects = [\"alpha\", \"beta\"]\n" + extra
}

func parseTables(t *testing.T, extra string) (Config, error) {
	t.Helper()
	return ParseConfigBytes("/tmp/cli.toml", []byte(tablesConfig(extra)))
}

func TestSummarizeTableParses(t *testing.T) {
	cfg, err := parseTables(t, "\n[summarize]\nmodels = [\"pi:m-a\", \"claude:m-b\"]\nlanguage = \"  Chinese \"\ninterval_sec = 120\n")
	if err != nil {
		t.Fatal(err)
	}
	want := &workspace.SummarizeSettings{Models: []string{"pi:m-a", "claude:m-b"}, Language: "Chinese", IntervalSec: 120}
	if !reflect.DeepEqual(cfg.Summarize, want) {
		t.Fatalf("summarize = %#v, want %#v", cfg.Summarize, want)
	}
	models, lang, iv := cfg.Summarize.Lane()
	if len(models) != 2 || lang != "Chinese" || iv != 120 {
		t.Fatalf("lane = %v %q %d", models, lang, iv)
	}
}

func TestSummarizeOptionalDefaultsAndAbsentTable(t *testing.T) {
	cfg, err := parseTables(t, "\n[summarize]\nmodels = [\"pi:m-a\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, lang, iv := cfg.Summarize.Lane(); lang != workspace.DefaultSummarizeLanguage || iv != workspace.DefaultSummarizeIntervalSec {
		t.Fatalf("defaults = %q %d", lang, iv)
	}
	cfg, err = parseTables(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Summarize != nil {
		t.Fatalf("an absent table must be nil, got %#v", cfg.Summarize)
	}
	if models, _, _ := cfg.Summarize.Lane(); models != nil {
		t.Fatalf("an absent table must turn the lane off, got %v", models)
	}
}

func TestSummarizeTableRefusals(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"no models", "\n[summarize]\nlanguage = \"English\"\n", "line 6: [summarize]: `models` is mandatory"},
		{"empty models", "\n[summarize]\nmodels = []\n", "line 7: [summarize] models: is empty"},
		{"unprefixed", "\n[summarize]\nmodels = [\"m-a\"]\n", "line 7: [summarize] models: item 0"},
		{"repeat", "\n[summarize]\nmodels = [\"pi:a\", \"pi:a\"]\n", "repeats"},
		{"empty language", "\n[summarize]\nmodels = [\"pi:a\"]\nlanguage = \"  \"\n", "line 8: [summarize] language: is empty"},
		{"unquoted language", "\n[summarize]\nmodels = [\"pi:a\"]\nlanguage = English\n", "language: must be a quoted"},
		{"interval zero", "\n[summarize]\nmodels = [\"pi:a\"]\ninterval_sec = 0\n", "interval_sec: must be an unquoted integer in 10-3600"},
		{"interval high", "\n[summarize]\nmodels = [\"pi:a\"]\ninterval_sec = 3601\n", "10-3600"},
		{"interval quoted", "\n[summarize]\nmodels = [\"pi:a\"]\ninterval_sec = \"60\"\n", "10-3600"},
		{"unknown key", "\n[summarize]\nmodels = [\"pi:a\"]\nharness = \"pi\"\n", "only models, language, interval_sec"},
		{"duplicate key", "\n[summarize]\nmodels = [\"pi:a\"]\nmodels = [\"pi:b\"]\n", "duplicate key"},
		{"duplicate table", "\n[summarize]\nmodels = [\"pi:a\"]\n[summarize]\nmodels = [\"pi:b\"]\n", "duplicate [summarize] table"},
	}
	for _, c := range cases {
		_, err := parseTables(t, c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "/tmp/cli.toml") {
			t.Errorf("%s: err = %v, want the file and %q", c.name, err, c.want)
		}
	}
}

func TestMemoryPolicyFamilyParses(t *testing.T) {
	cfg, err := parseTables(t, `
[memory_policy."`+tablesWS+`".default]
record = ["a", "b"]
skip = []

[memory_policy."`+tablesWS+`"."alpha"]
record = ["c"]
`)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.MemoryPolicy[tablesWS]
	if !reflect.DeepEqual(got["default"], workspace.PolicyEntry{Record: []string{"a", "b"}, Skip: []string{}}) || !reflect.DeepEqual(got["alpha"], workspace.PolicyEntry{Record: []string{"c"}}) {
		t.Fatalf("policy = %#v", got)
	}
	eff := workspace.MemoryPolicyFor(got, "alpha")
	if !reflect.DeepEqual(eff, workspace.MemoryPolicy{Record: []string{"a", "b", "c"}, Skip: []string{}}) {
		t.Fatalf("merge = %#v", eff)
	}
	if eff := workspace.MemoryPolicyFor(got, "beta"); !reflect.DeepEqual(eff.Record, []string{"a", "b"}) {
		t.Fatalf("beta merge = %#v", eff)
	}
}

func TestMemoryPolicyFamilyRefusals(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"unmapped path", "\n[memory_policy.\"/ws/other\".default]\nrecord = [\"a\"]\n", "line 6: [memory_policy.\"/ws/other\".default]: /ws/other is not a mapped workspace"},
		{"foreign project", "\n[memory_policy.\"" + tablesWS + "\".gamma]\nrecord = [\"a\"]\n", "entry \"gamma\" is neither"},
		{"no list", "\n[memory_policy.\"" + tablesWS + "\".default]\n", "declares no list"},
		{"other key", "\n[memory_policy.\"" + tablesWS + "\".default]\nmodels = [\"a\"]\n", "line 7: [memory_policy.\"/ws/alpha\".default] models: unknown key (an entry carries only record, skip)"},
		{"empty item", "\n[memory_policy.\"" + tablesWS + "\".default]\nrecord = [\" \"]\n", "item 0 is empty"},
		{"duplicate item", "\n[memory_policy.\"" + tablesWS + "\".default]\nrecord = [\"a\", \"a\"]\n", "item 1 repeats"},
		{"non-string item", "\n[memory_policy.\"" + tablesWS + "\".default]\nrecord = [1]\n", "items must be quoted strings"},
		{"duplicate table", "\n[memory_policy.\"" + tablesWS + "\".default]\nrecord = [\"a\"]\n[memory_policy.\"" + tablesWS + "\".default]\nskip = [\"a\"]\n", "duplicate [memory_policy"},
		{"unquoted path", "\n[memory_policy.ws.default]\nrecord = [\"a\"]\n", "QUOTED path"},
		{"no entry", "\n[memory_policy.\"" + tablesWS + "\"]\nrecord = [\"a\"]\n", "names the workspace path and the entry"},
	}
	for _, c := range cases {
		_, err := parseTables(t, c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

func TestMultilineArrayEqualsOneLine(t *testing.T) {
	multi, err := parseTables(t, `
[summarize]
models = [
  "pi:m-a",  # a comment
  "claude:m-b",
]

[memory_policy."`+tablesWS+`".default]
record = [
  "Process logs, step outputs, full spec/plan, diffs",
  "Owner decisions # not a comment",
]
`)
	if err != nil {
		t.Fatal(err)
	}
	one, err := parseTables(t, `
[summarize]
models = ["pi:m-a", "claude:m-b"]

[memory_policy."`+tablesWS+`".default]
record = ["Process logs, step outputs, full spec/plan, diffs", "Owner decisions # not a comment"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(multi.Summarize, one.Summarize) || !reflect.DeepEqual(multi.MemoryPolicy, one.MemoryPolicy) {
		t.Fatalf("multi-line %#v %#v != one-line %#v %#v", multi.Summarize, multi.MemoryPolicy, one.Summarize, one.MemoryPolicy)
	}
	if got := multi.MemoryPolicy[tablesWS]["default"].Record; len(got) != 2 {
		t.Fatalf("a quoted comma split an item: %#v", got)
	}
}

func TestGrammarStaysStrict(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"escape", "\n[summarize]\nmodels = [\"pi:a\\\\b\"]\n", "escapes"},
		{"inline table", "\n[summarize]\nmodels = { a = 1 }\n", "inline tables are not supported"},
		{"unknown table", "\n[other]\nx = \"y\"\n", "unknown section"},
		{"unterminated", "\n[summarize]\nmodels = [\n  \"pi:a\",\n", "unterminated array"},
		{"missing comma", "\n[summarize]\nmodels = [\"pi:a\" \"pi:b\"]\n", "one quoted string expected"},
	}
	for _, c := range cases {
		_, err := parseTables(t, c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
