package cli

// config_cas.go: the expected-file-hash (CAS) precondition every
// `pf config set|unset` write may carry (workbench#22). The comparison runs
// inside the SAME writer flock as the read-modify-write, immediately before
// the atomic write, so two callers that read one hash cannot both win. The
// refusal is the typed CORE-409 envelope with a key-level diff; api_key is
// reported as changed/unchanged only, never with a value.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

type configExpectation struct {
	hash    string
	hashSet bool
	state   *configExpectState
}

type configExpectState struct {
	FileSHA256 json.RawMessage            `json:"file_sha256"`
	Values     map[string]json.RawMessage `json:"values"`
}

type casDiffEntry struct {
	Key      string `json:"key"`
	Expected any    `json:"expected,omitempty"`
	Current  any    `json:"current,omitempty"`
	Changed  *bool  `json:"changed,omitempty"`
}

// parseConfigWriteArgs separates a set/unset tail's manual options from its
// positionals. cmdConfig stays flag-less so a value such as `-1m` remains a
// positional; only the three explicit long options are consumed.
func parseConfigWriteArgs(args []string) (pos []string, exp configExpectation, workspacePath, cwd string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--expect-hash":
			if i+1 >= len(args) {
				return nil, exp, "", "", fmt.Errorf("--expect-hash needs <sha256|absent>")
			}
			exp.hash, exp.hashSet = args[i+1], true
			i++
		case "--expect-state":
			if i+1 >= len(args) {
				return nil, exp, "", "", fmt.Errorf("--expect-state needs a JSON object")
			}
			var st configExpectState
			if err := json.Unmarshal([]byte(args[i+1]), &st); err != nil {
				return nil, exp, "", "", fmt.Errorf("--expect-state: %v", err)
			}
			exp.state = &st
			i++
		case "--workspace":
			if i+1 >= len(args) {
				return nil, exp, "", "", fmt.Errorf("--workspace needs a path")
			}
			workspacePath = args[i+1]
			i++
		case "--cwd":
			if i+1 >= len(args) {
				return nil, exp, "", "", fmt.Errorf("--cwd needs a directory")
			}
			cwd = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				return nil, exp, "", "", fmt.Errorf("config: unknown option %q (only --expect-hash, --expect-state, --workspace and --cwd)", args[i])
			}
			pos = append(pos, args[i])
		}
	}
	return pos, exp, workspacePath, cwd, nil
}

func hashOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// checkFileExpectation refuses a cli.toml write whose expected hash/state does
// not match the current bytes. It is called under the writer flock.
func checkFileExpectation(exp configExpectation, curRaw []byte, exists bool) error {
	curHash := ""
	if exists {
		curHash = hashOf(curRaw)
	}
	return checkExpectation(exp, curHash, exists, configSnapshotValues(curRaw, exists))
}

// stateFileHash reads the expectation's file_sha256 as a tri-state. ok=false
// means the field was absent from the wire (no hash expectation). ok=true with
// want "" means an explicit JSON null, which on the wire says the target file
// is expected absent. This is why the field is json.RawMessage and not
// *string: encoding/json decodes both an omitted field and a null to a nil
// pointer, so a null would silently disable the CAS and defeat the documented
// `file_sha256: null` creation path.
func stateFileHash(raw json.RawMessage) (want string, ok bool) {
	if raw == nil {
		return "", false
	}
	var s *string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	if s == nil {
		return "", true
	}
	return *s, true
}

// hashValueSaysAbsent reports whether an expected file_sha256 value means the
// file is expected absent: an empty string, the JSON null spelling or the
// literal `absent`.
func hashValueSaysAbsent(want string) bool {
	switch strings.TrimSpace(want) {
	case "", "null", "absent":
		return true
	}
	return false
}

// expectsAbsent reports whether the expectation says the target file is absent,
// the one precondition a summarize write may create it under.
func expectsAbsent(exp configExpectation) bool {
	if exp.hashSet && strings.TrimSpace(exp.hash) == "absent" {
		return true
	}
	if exp.state != nil {
		if want, ok := stateFileHash(exp.state.FileSHA256); ok && hashValueSaysAbsent(want) {
			return true
		}
	}
	return false
}

func expectationHashChanged(want, curHash string, exists bool) bool {
	if want == "absent" {
		return exists
	}
	return !exists || !strings.EqualFold(want, curHash)
}

// checkExpectation is THE expected-hash/state comparison both config files
// (cli.toml and the workspace yaml) share: the caller supplies the current
// hash, existence and tracked key values. With a per-key snapshot the diff is
// key-level and an unchanged key is never reported; a hash-only expectation
// lists the current tracked values (the caller could not say which key moved).
func checkExpectation(exp configExpectation, curHash string, exists bool, curValues map[string]any) error {
	if !exp.hashSet && exp.state == nil {
		return nil
	}
	hashChanged := false
	if exp.hashSet {
		hashChanged = expectationHashChanged(strings.TrimSpace(exp.hash), curHash, exists)
	}
	var diff []casDiffEntry
	if exp.state != nil {
		if want, ok := stateFileHash(exp.state.FileSHA256); ok {
			want = strings.TrimSpace(want)
			if hashValueSaysAbsent(want) {
				if exists {
					hashChanged = true
				}
			} else if expectationHashChanged(want, curHash, exists) {
				hashChanged = true
			}
		}
		keys := make([]string, 0, len(exp.state.Values))
		for k := range exp.state.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			entry := compareExpectedValue(key, exp.state.Values[key], curValues, hashChanged)
			if entry != nil {
				diff = append(diff, *entry)
			}
		}
	}
	if hashChanged && len(diff) == 0 && (exp.state == nil || len(exp.state.Values) == 0) {
		// Hash-only expectation: name the current tracked values so the
		// operator can see the file the caller pinned.
		keys := make([]string, 0, len(curValues))
		for k := range curValues {
			if k != "api_key" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			diff = append(diff, casDiffEntry{Key: key, Current: curValues[key]})
		}
		if _, ok := curValues["api_key"]; ok {
			changed := true
			diff = append(diff, casDiffEntry{Key: "api_key", Changed: &changed})
		}
	}
	if !hashChanged && len(diff) == 0 {
		return nil
	}
	detail := "the file changed since it was read"
	if len(diff) > 0 {
		parts := make([]string, 0, len(diff))
		for _, d := range diff {
			if d.Key == "api_key" {
				if d.Changed != nil && *d.Changed {
					parts = append(parts, "api_key: changed (value redacted)")
				} else {
					parts = append(parts, "api_key: unchanged (value redacted)")
				}
				continue
			}
			switch {
			case d.Expected != nil && d.Current != nil:
				parts = append(parts, fmt.Sprintf("%s: expected %v, current %v", d.Key, d.Expected, d.Current))
			case d.Current != nil:
				parts = append(parts, fmt.Sprintf("%s: current %v", d.Key, d.Current))
			default:
				parts = append(parts, d.Key+": changed")
			}
		}
		detail += ": " + strings.Join(parts, "; ")
	}
	extra, _ := json.Marshal(map[string]any{"diff": diff})
	return &api.Error{Code: api.ErrCoreConflict, Detail: detail, Extra: extra}
}

func compareExpectedValue(key string, raw json.RawMessage, current map[string]any, hashChanged bool) *casDiffEntry {
	if key == "api_key" {
		var obj struct {
			Set *bool `json:"set"`
		}
		wantSet := false
		if json.Unmarshal(raw, &obj) == nil && obj.Set != nil {
			wantSet = *obj.Set
		} else {
			var scalar bool
			if json.Unmarshal(raw, &scalar) == nil {
				wantSet = scalar
			}
		}
		cur, ok := current["api_key"]
		curSet := false
		if ok {
			if m, ok := cur.(map[string]any); ok {
				curSet, _ = m["set"].(bool)
			}
		}
		// The set/unset bit is the only diff we can name; an unchanged bit with
		// an unchanged file is not a difference and must never refuse. When the
		// whole-file hash moved we cannot know whether the value rotated, so the
		// key is reported changed (value redacted).
		changed := wantSet != curSet || hashChanged
		if !changed {
			return nil
		}
		return &casDiffEntry{Key: key, Changed: &changed}
	}
	var want any
	if json.Unmarshal(raw, &want) != nil {
		return nil
	}
	cur, ok := current[key]
	if !ok {
		return &casDiffEntry{Key: key, Expected: want, Current: nil}
	}
	if !jsonEqual(want, cur) {
		return &casDiffEntry{Key: key, Expected: want, Current: cur}
	}
	return nil
}

// jsonEqual compares two decoded JSON values by their canonical JSON, so a
// string, number or array compares by content rather than Go's fmt %v shape.
func jsonEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return fmt.Sprint(a) == fmt.Sprint(b)
	}
	return string(ab) == string(bb)
}

// configSnapshotValues renders the current file's key→value map for a CAS
// comparison. api_key carries only {set: bool}.
func configSnapshotValues(raw []byte, exists bool) map[string]any {
	out := map[string]any{}
	if !exists {
		return out
	}
	cfg, err := ParseConfigBytes("", raw)
	if err != nil {
		return out
	}
	flat, _, dkvs, err := parseConfigTOML("", string(raw))
	if err != nil {
		return out
	}
	for _, key := range knownFlatKeyNames() {
		if key == "api_key" {
			out[key] = map[string]any{"set": flat[key] != ""}
			continue
		}
		if v, ok := flat[key]; ok {
			out[key] = v
		}
	}
	seenDaemon := map[string]bool{}
	for _, kv := range dkvs {
		seenDaemon[kv.key] = true
	}
	for _, key := range knownDaemonKeyNames() {
		val := daemonValue(cfg.Daemon, key)
		if val == "" && !seenDaemon[key] {
			continue
		}
		out[key] = val
	}
	return out
}
