package cli

// status_retry_docs_test.go: the retry pacing's doc faces stay in step
// with the code. The GET /status row of the UDS route reference names the
// StatusJob fields that carry a pending retry backoff (and the step-wait
// counters, where StatusJob carries them), and the counted-
// failure item of the workflow design keeps "the latter" next to the
// upstream_transient clause it refers to (the pacing note once landed
// between them and made it read as ended_without_result).

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStatusRetryFieldsDocumented(t *testing.T) {
	root := docsSetRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs/reference/uds-routes.md"))
	if err != nil {
		t.Fatal(err)
	}
	var row string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "| GET | `/status` |") {
			row = line
		}
	}
	if row == "" {
		t.Fatalf("docs/reference/uds-routes.md has no GET /status row")
	}
	tags := map[string]bool{}
	typ := reflect.TypeOf(StatusJob{})
	for i := 0; i < typ.NumField(); i++ {
		tags[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	// the retry fields are this change's own; the step-wait counters
	// (wait_calls / wait_seconds) are pinned wherever StatusJob carries
	// them (they land with the wait-call change)
	for _, f := range []struct {
		tag      string
		required bool
	}{{"retry_in_sec", true}, {"retry_cause", true}, {"wait_calls", false}, {"wait_seconds", false}} {
		if !tags[f.tag] {
			if f.required {
				t.Errorf("StatusJob has no `%s` field", f.tag)
			}
			continue
		}
		if !strings.Contains(row, "`"+f.tag+"`") {
			t.Errorf("the GET /status row must name StatusJob's `%s` field:\n%s", f.tag, row)
		}
	}
}

func TestCountedFailureItemKeepsTheLatterNextToItsSubject(t *testing.T) {
	root := docsSetRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs/design/polyforge-vnext/01-workflow-skills.md"))
	if err != nil {
		t.Fatal(err)
	}
	const clause = "`rate_limited` and `upstream_transient` within its bound are not counted (for the latter, "
	if !strings.Contains(string(raw), clause) {
		t.Fatalf("01-workflow-skills.md: \"(for the latter, …)\" must directly follow the upstream_transient clause it refers to")
	}
}
