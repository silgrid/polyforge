package stepcall

// ops.go: the hub writes a step records with `pf step hub` (design #483
// §5.2). A write is never made when it is called: it is recorded pending
// on the ledger, and the pending-writes executor makes the dispatch's
// writes in declaration order once the dispatch hands off done. The
// checks here are shape only — what is written is the step's.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// The hub verbs (`pf step hub <verb>`; a call line's verb is VerbHubPrefix
// + the verb).
const (
	HubGet       = "get"
	HubList      = "list"
	HubFile      = "file"
	HubAttrs     = "attrs"
	HubWrapAttrs = "wrap-attrs"
	HubBatch     = "batch"
)

// HubVerbs is every hub verb.
var HubVerbs = []string{HubGet, HubList, HubFile, HubAttrs, HubWrapAttrs, HubBatch}

// IsHubWrite reports whether verb records a pending write (batch
// included).
func IsHubWrite(verb string) bool {
	switch verb {
	case HubFile, HubAttrs, HubWrapAttrs, HubBatch:
		return true
	}
	return false
}

// KeyRefPrefix prefixes a reference to a work item a pending `file` of the
// same dispatch files: `key:<K>` resolves to the filed id when the write
// is made.
const KeyRefPrefix = "key:"

// Op is one pending hub write: its id on the ledger, its verb and the
// verb's fields.
type Op struct {
	// ID is "<call seq>.<n>": the call that recorded it and its place in
	// that call (set by the daemon).
	ID   string `json:"id,omitempty"`
	Verb string `json:"verb"`
	// Key is a file's identity (required): written into the filed work
	// item's body as a marker line, it is what the write looks up before
	// filing, so a replay never files twice.
	Key string `json:"key,omitempty"`
	// file: the work item to create (Project is the calling step's
	// project, set by the daemon).
	Project  string   `json:"project,omitempty"`
	Lane     string   `json:"lane,omitempty"`
	Goal     string   `json:"goal,omitempty"`
	Content  string   `json:"content,omitempty"`
	Priority string   `json:"priority,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	// attrs: the work item written (an id, a slug or key:<K>); the
	// top-level attrs key it writes whole is Name and its value Value;
	// If fences the write (each attrs path must hold its JSON value, an
	// absent path reading as null, or aihub refuses it CORE-409).
	Target string         `json:"target,omitempty"`
	If     map[string]any `json:"if,omitempty"`
	// attrs: PerOwner is the stamp (at) of the owner message the write
	// carries out (optional) — an owner message to the calling step in
	// its current round, checked as a hand-off's --per-owner is;
	// OwnerMessage is that message's words verbatim, writer and stamp,
	// set by the daemon from its own record (never by the caller) and
	// recorded with the write at aihub.
	PerOwner     string            `json:"per_owner,omitempty"`
	OwnerMessage *api.OwnerMessage `json:"owner_message,omitempty"`
	// attrs: the key written and its value; wrap-attrs: the attr the work
	// item's wrap merges and its value.
	Name  string          `json:"name,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// attrNameRe is the shape of an attrs key (attrs) and of a wrap attr's
// name; attrPathRe the shape of an attrs path an attrs write is fenced
// on (aihub's own shapes).
var (
	attrNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	attrPathRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
)

// CheckOp checks one write's shape: a known write verb and the verb's
// fields. A file needs its key, lane, goal and content; an attrs write
// its target, a key, a JSON value and attrs paths in its if; a wrap attr
// a name and a JSON value.
func CheckOp(op Op) error {
	switch op.Verb {
	case HubFile:
		switch {
		case strings.TrimSpace(op.Key) == "":
			return errors.New("file: --key is required (the ticket's identity: a replay finds the ticket filed under it)")
		case strings.ContainsAny(op.Key, "\r\n"):
			return errors.New("file: --key is one line")
		case strings.TrimSpace(op.Lane) == "":
			return errors.New("file: --lane is required")
		case strings.TrimSpace(op.Goal) == "":
			return errors.New("file: --goal is required")
		case strings.TrimSpace(op.Content) == "":
			return errors.New("file: --content is required")
		}
	case HubAttrs:
		switch {
		case strings.TrimSpace(op.Target) == "":
			return errors.New("attrs: the work item is required")
		case !attrNameRe.MatchString(op.Name):
			return fmt.Errorf("attrs: --set names the attrs key %q, not of the shape %s", op.Name, attrNameRe)
		case len(op.Value) == 0 || !json.Valid(op.Value):
			return errors.New("attrs: the value is one JSON document")
		}
		for path := range op.If {
			if !attrPathRe.MatchString(path) {
				return fmt.Errorf("attrs: --if %q is not an attrs path (%s)", path, attrPathRe)
			}
		}
	case HubWrapAttrs:
		switch {
		case !attrNameRe.MatchString(op.Name):
			return fmt.Errorf("wrap-attrs: the attr name %q is not of the shape %s", op.Name, attrNameRe)
		case len(op.Value) == 0 || !json.Valid(op.Value):
			return errors.New("wrap-attrs: the value is one JSON document")
		}
	default:
		return fmt.Errorf("no hub write %q (the writes: file, attrs, wrap-attrs)", op.Verb)
	}
	return nil
}

// KeyRefs answers the keys op references (key:<K> as its target or, an
// attrs write's, as any string inside its value), in order of
// appearance, each once.
func KeyRefs(op Op) []string {
	var out []string
	add := func(s string) {
		if k, ok := strings.CutPrefix(s, KeyRefPrefix); ok && k != "" && !contains(out, k) {
			out = append(out, k)
		}
	}
	add(op.Target)
	if op.Verb == HubAttrs {
		var v any
		if json.Unmarshal(op.Value, &v) == nil {
			walkStrings(v, func(s string) string { add(s); return s })
		}
	}
	return out
}

// ResolveRefs answers op with every key:<K> reference replaced by the
// reference refs maps K to; a key refs lacks is an error.
func ResolveRefs(op Op, refs map[string]string) (Op, error) {
	var missing []string
	resolve := func(s string) string {
		k, ok := strings.CutPrefix(s, KeyRefPrefix)
		if !ok || k == "" {
			return s
		}
		if ref, ok := refs[k]; ok && ref != "" {
			return ref
		}
		if !contains(missing, k) {
			missing = append(missing, k)
		}
		return s
	}
	op.Target = resolve(op.Target)
	if op.Verb == HubAttrs {
		var v any
		if err := json.Unmarshal(op.Value, &v); err != nil {
			return op, fmt.Errorf("the attrs value is unreadable: %w", err)
		}
		raw, err := json.Marshal(walkStrings(v, resolve))
		if err != nil {
			return op, err
		}
		op.Value = raw
	}
	if len(missing) > 0 {
		return op, fmt.Errorf("the write references %s, filed by no write made before it", strings.Join(missing, ", "))
	}
	return op, nil
}

// walkStrings answers v (a decoded JSON value) with fn applied to every
// string inside it, copying the containers it walks.
func walkStrings(v any, fn func(string) string) any {
	switch x := v.(type) {
	case string:
		return fn(x)
	case map[string]any:
		if x == nil {
			return x
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = walkStrings(e, fn)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = walkStrings(e, fn)
		}
		return out
	}
	return v
}
