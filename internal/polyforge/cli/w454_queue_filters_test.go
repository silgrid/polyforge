package cli

// w454_queue_filters_test.go (u1): `pf queue --attr
// <path>=<value>` (repeatable) and `--bundle L` are a generic pass-through
// of aihub's existing list filters — `?attr=<path>:<value>` and
// `?bundle=L`. The CLI splits --attr on its first `=` and changes nothing
// else: no default path, bundle or value, no special case for any name.
// Every value here is deliberately arbitrary.

import (
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// workItemListQueries answers the query of every recorded list request
// (GET /api/v1/work-items), in order.
func workItemListQueries(t *testing.T, rec *reqRecorder) []url.Values {
	t.Helper()
	var out []url.Values
	for _, r := range rec.all() {
		u, err := url.ParseRequestURI(r.Path)
		if err != nil {
			t.Fatalf("recorded request %q: %v", r.Path, err)
		}
		if r.Method == http.MethodGet && u.Path == "/api/v1/work-items" {
			out = append(out, u.Query())
		}
	}
	return out
}

func TestW454QueuePassesBundleAndAttrFilters(t *testing.T) {
	var rec reqRecorder
	srv := newFakeAihub(t, &rec, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"items":[`+cannedWI+`],"total":1}`)
	})
	env := map[string]string{"PF_SERVER": srv.URL}

	// The plan's pinned case: `:` and `=` inside a value pass through
	// untouched; only the first `=` splits path from value; attr order is
	// kept.
	t.Run("first equals splits, rest verbatim", func(t *testing.T) {
		before := len(workItemListQueries(t, &rec))
		code, _, stderr := runPf(t, env, "--json", "queue", "--project", "p",
			"--attr", "q7.k2=v:w=x", "--attr", "a.b=c", "--bundle", "lane_x")
		if code != 0 {
			t.Fatalf("queue with filters: code=%d stderr=%s", code, stderr)
		}
		qs := workItemListQueries(t, &rec)[before:]
		if len(qs) != 1 {
			t.Fatalf("want exactly one list request, got %d: %v", len(qs), qs)
		}
		want := url.Values{
			"project": {"p"},
			"attr":    {"q7.k2:v:w=x", "a.b:c"},
			"bundle":  {"lane_x"},
		}
		if !reflect.DeepEqual(qs[0], want) {
			t.Errorf("list query = %v, want exactly %v", qs[0], want)
		}
	})

	// No special case: arbitrary paths, values and bundle names map
	// straight onto the query, nothing added, nothing rewritten.
	cases := []struct {
		name string
		args []string
		want url.Values
	}{
		{"single attr", []string{"--attr", "zz.top=1"},
			url.Values{"attr": {"zz.top:1"}}},
		{"bundle only", []string{"--bundle", "bundle9"},
			url.Values{"bundle": {"bundle9"}}},
		{"empty value", []string{"--attr", "m.n="},
			url.Values{"attr": {"m.n:"}}},
		{"spaces and unicode", []string{"--attr", "r_s.t-u=hello world", "--attr", "k=值"},
			url.Values{"attr": {"r_s.t-u:hello world", "k:值"}}},
		{"with status and limit", []string{"--status", "wrapped", "--limit", "7", "--bundle", "b-2", "--attr", "x.y=z"},
			url.Values{"status": {"wrapped"}, "limit": {"7"}, "bundle": {"b-2"}, "attr": {"x.y:z"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := len(workItemListQueries(t, &rec))
			args := append([]string{"--json", "queue", "--project", "proj_q"}, c.args...)
			code, _, stderr := runPf(t, env, args...)
			if code != 0 {
				t.Fatalf("queue %v: code=%d stderr=%s", c.args, code, stderr)
			}
			qs := workItemListQueries(t, &rec)[before:]
			if len(qs) != 1 {
				t.Fatalf("want exactly one list request, got %d: %v", len(qs), qs)
			}
			want := url.Values{"project": {"proj_q"}}
			for k, v := range c.want {
				want[k] = v
			}
			if !reflect.DeepEqual(qs[0], want) {
				t.Errorf("queue %v: list query = %v, want exactly %v", c.args, qs[0], want)
			}
		})
	}

	// Merged mode (no --project): every mapped project's query carries the
	// same filters.
	t.Run("merged mode filters every mapped project", func(t *testing.T) {
		menv := withTwoProjectMapping(t)
		menv["PF_SERVER"] = srv.URL
		before := len(workItemListQueries(t, &rec))
		code, _, stderr := runPf(t, menv, "--json", "queue", "--attr", "a.b=c", "--bundle", "lane_x")
		if code != 0 {
			t.Fatalf("merged queue with filters: code=%d stderr=%s", code, stderr)
		}
		got := workItemListQueries(t, &rec)[before:]
		want := []url.Values{
			{"project": {"alpha"}, "attr": {"a.b:c"}, "bundle": {"lane_x"}},
			{"project": {"beta"}, "attr": {"a.b:c"}, "bundle": {"lane_x"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("merged list queries = %v, want exactly %v", got, want)
		}
	})

	// Refused locally, before any request.
	refusals := []struct {
		name string
		args []string
		msg  string
	}{
		{"attr without equals", []string{"--project", "p", "--attr", "foo"}, "--attr"},
		{"attr with empty path", []string{"--project", "p", "--attr", "=v"}, "--attr"},
		{"ready with attr", []string{"--project", "p", "--ready", "--attr", "a.b=c"}, "--ready"},
		{"ready with bundle", []string{"--project", "p", "--ready", "--bundle", "lane_x"}, "--ready"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			before := len(rec.all())
			code, _, stderr := runPf(t, env, append([]string{"queue"}, c.args...)...)
			if code == 0 {
				t.Fatalf("queue %v: exit 0, want a refusal", c.args)
			}
			if !strings.Contains(stderr, c.msg) {
				t.Errorf("queue %v: stderr %q does not name %s", c.args, stderr, c.msg)
			}
			if n := len(rec.all()); n != before {
				t.Errorf("queue %v reached aihub (%d new requests)", c.args, n-before)
			}
		})
	}
}
