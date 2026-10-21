// Package sessionjsonl is THE single copier and reader of the harness
// session-jsonl storage format (the live transcript shape: one JSON
// record per line, appended to concurrently by the harness process):
// CopyReadonly is the one live-transcript copier, consumed by the
// observe module (the commit-boundary sync) and by the runtime's
// transcript sync face; Tail is the one bounded-tail reader, consumed
// by the summarize lane's prompt (polyforge#224) — and the package stays
// a leaf (stdlib plus the atomicfile leaf) so both consumers can import
// it without cycles.
//
// The observe stdout-wire parser (internal/polyforge/observe/stream.go) is
// a DIFFERENT vocabulary (the daemon↔agent RPC record stream) and
// deliberately does not use this package (grill ruling 5): the two formats
// must never merge, and the single-source property is asserted by test.
//
// Deliberate posture: parsing NEVER fails per record. A live transcript is
// appended to concurrently, so its last line may be torn mid-write, and a
// malformed line is data the consumers may still want to see. Every line —
// JSON object, JSON scalar, garbage, empty — becomes a Record carrying the
// raw bytes verbatim plus a best-effort classification. Only I/O fails.
// The copy is byte-verbatim (a torn last line is preserved exactly).
package sessionjsonl

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// CopyReadonly copies the session-jsonl file src onto dst without ever
// opening the source for writing (the source is the harness's own
// session store). The bytes are copied verbatim; the destination is
// written tmp+rename, 0644. It is the one live-transcript copier: the
// observe monitor's commit-boundary sync and the runtime's
// seal/pause/bounded-fail sync both call it.
func CopyReadonly(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return atomicfile.Write(dst, raw, 0o644)
}

// Record is one line of a session-jsonl file, raw bytes preserved.
type Record struct {
	// Line is the 1-based ordinal of the record in the file.
	Line int
	// Raw is the line verbatim, WITHOUT its trailing newline.
	Raw string
	// Torn marks the final line of a file that does not end with a
	// newline (the live transcript is appended to concurrently; the
	// last write may still be in flight). A torn record is still data.
	Torn bool
	// Type is the minimal classification: for a JSON object carrying a
	// string-typed "type", "kind" or "t" field, that value; for any
	// other JSON value, "json"; for an empty line, "blank"; for an
	// unparsable line, "raw".
	Type string
	// At is the record's timestamp when it carries one: the first
	// string-typed value among the keys "timestamp", "ts", "at", "time".
	// "" when none. Never parsed — consumers compare or format it.
	At string
}

// Tail returns the last n records of the session-jsonl file at path.
// n <= 0 means the whole file. The raw bytes of every record are
// preserved verbatim; torn trailing lines and malformed lines are
// records, never errors. Only a read failure is an error.
func Tail(path string, n int) ([]Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	all := split(data)
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// split cuts the file bytes into records: one per line, the torn flag on
// a final unterminated line, each line classified.
func split(data []byte) []Record {
	text := string(data)
	if text == "" {
		return nil
	}
	torn := !strings.HasSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	recs := make([]Record, 0, len(lines))
	for i, raw := range lines {
		last := i == len(lines)-1
		if last && raw == "" {
			// The newline after the final complete line; not a record.
			break
		}
		recs = append(recs, Record{
			Line: i + 1,
			Raw:  raw,
			Torn: torn && last,
			Type: classify(raw),
			At:   timestampOf(raw),
		})
	}
	return recs
}

// classify best-efforts one line. Never fails: an unparsable line is
// "raw", an empty line "blank".
func classify(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "blank"
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "raw"
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "json"
	}
	for _, key := range []string{"type", "kind", "t"} {
		if s, ok := obj[key].(string); ok && s != "" {
			return s
		}
	}
	return "object"
}

// timestampOf extracts the first string-typed timestamp field of a JSON
// object line; "" otherwise (malformed, non-object, or absent).
func timestampOf(raw string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return ""
	}
	for _, key := range []string{"timestamp", "ts", "at", "time"} {
		if s, ok := obj[key].(string); ok {
			return s
		}
	}
	return ""
}
