// Package claudeinit reads, compares and writes the `system/init` records
// Claude Code emits at the start of a stream-json session, recorded from
// real probes of the processes the daemon spawns (the step;
// the task lane and the summarize lane).
//
// `system/init.tools` names the tools a session really has, which is not
// always what `--tools` names: Claude Code 2.1.319 under --bare offers
// Bash, Edit and Read whatever --tools names. A recording carries the argv
// and the adapter's env switches it ran with, so a replay test can take the
// recording of exactly the profile the code builds and compare its init
// with what the lane declares. Only tests import this package; the live
// probes that (re)write the recordings spend model tokens and are opt-in.
package claudeinit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Recording is one recorded probe: what ran and the init it got.
type Recording struct {
	ClaudeCodeVersion string   `json:"claude_code_version"`
	Recorded          string   `json:"recorded"`
	Argv              []string `json:"argv"`
	Env               []string `json:"env"`
	// ObservedWith are flags the probe appended to Argv only to read the
	// init (a lane that runs in text mode has no stream to read it from).
	ObservedWith []string `json:"observed_with,omitempty"`
	// Note states what the recording does not show, e.g. a run that never
	// reached the model (the init precedes authentication).
	Note string          `json:"note,omitempty"`
	Init json.RawMessage `json:"init"`
	File string          `json:"-"`
}

// Init is the part of a `system/init` record the checks read.
type Init struct {
	Tools   []string `json:"tools"`
	Plugins []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"plugins"`
	Skills              []string          `json:"skills"`
	SlashCommands       []string          `json:"slash_commands"`
	MCPServers          []json.RawMessage `json:"mcp_servers"`
	Agents              []string          `json:"agents"`
	MemoryPaths         json.RawMessage   `json:"memory_paths"`
	MessagingSocketPath string            `json:"messaging_socket_path"`
	Version             string            `json:"claude_code_version"`
}

// LegacyNames maps the names Claude Code lists in `system/init` to the
// names --tools takes. 2.1.319 lists the Agent tool there under its legacy
// SDK name: the init builder maps each tool through
// `jgn(e){return e===ft?Sc:e}` beside the alias table `{Task:"Agent",…}`,
// while --tools, the request's tool list and the stream's tool_use records
// all name it Agent.
var LegacyNames = map[string]string{"Task": "Agent"}

// AgentsMDPlugin is Claude Code's built-in plugin that loads AGENTS.md as
// project instructions ("by default loaded where the project has no
// CLAUDE.md"), up every ancestor directory and beside every file the
// session Reads.
const AgentsMDPlugin = "cc-plugin-agents-md"

// Load reads every recording under dir (*.json); none is an error.
func Load(dir string) ([]Recording, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) == 0 {
		return nil, fmt.Errorf("no recorded system/init under %s (%v)", dir, err)
	}
	var out []Recording
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r Recording
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		r.File = p
		out = append(out, r)
	}
	return out, nil
}

// Matching answers the recordings made with exactly argv and env.
func Matching(recs []Recording, argv, env []string) []Recording {
	var out []Recording
	for _, r := range recs {
		if slices.Equal(r.Argv, argv) && slices.Equal(r.Env, env) {
			out = append(out, r)
		}
	}
	return out
}

// Decode reads a `system/init` record; one without a `tools` key is an
// error (an empty list is a tool surface, an absent one is not an init).
func Decode(raw json.RawMessage) (Init, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return Init{}, err
	}
	if _, ok := keys["tools"]; !ok {
		return Init{}, fmt.Errorf("no tools key: not a system/init record")
	}
	var in Init
	err := json.Unmarshal(raw, &in)
	return in, err
}

// Profile answers the parts of a built command that shape what Claude
// Code offers: the argv (the binary as "claude", a --session-id/--resume
// value as "<session>") and the adapter's switches in its env — every
// CLAUDE_CODE_* entry and DISABLE_AUTOUPDATER — sorted.
func Profile(cmd *exec.Cmd) (argv, env []string) {
	argv = append([]string{"claude"}, cmd.Args[1:]...)
	for i := 1; i < len(argv)-1; i++ {
		if argv[i] == "--session-id" || argv[i] == "--resume" {
			argv[i+1] = "<session>"
		}
	}
	for _, kv := range cmd.Env {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "CLAUDE_CODE_") || name == "DISABLE_AUTOUPDATER" {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	return argv, env
}

// ToolSurfaceDiff answers what a session's init tools miss and add against
// the declared --tools list, in --tools names; a tool listed twice is
// extra. A declared "" is the empty surface.
func ToolSurfaceDiff(initTools []string, declared string) (missing, extra []string) {
	got := map[string]int{}
	for _, n := range initTools {
		if c, ok := LegacyNames[n]; ok {
			n = c
		}
		got[n]++
	}
	if declared != "" {
		for _, n := range strings.Split(declared, ",") {
			switch got[n] {
			case 0:
				missing = append(missing, n)
			case 1:
			default:
				extra = append(extra, n)
			}
			delete(got, n)
		}
	}
	for n := range got {
		extra = append(extra, n)
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// InboxFinding answers a session's cross-session inbox, "" without one:
// Claude Code opens a socket other local processes can send the session
// user messages through (auth is optional on Linux) and exports its path
// and a token to every Bash child.
func InboxFinding(in Init) string {
	if in.MessagingSocketPath == "" {
		return ""
	}
	return "a cross-session messaging inbox at " + in.MessagingSocketPath
}

// ContextFindings answers what of a clean session's empty context a
// `system/init` record shows: a plugin that loads AGENTS.md, a skill or
// command, an MCP server, the custom agent a probe planted (customAgent,
// "" for none), an auto-memory path, a cross-session inbox.
func ContextFindings(in Init, customAgent string) []string {
	var out []string
	for _, p := range in.Plugins {
		if p.Name == AgentsMDPlugin || strings.HasPrefix(p.Source, AgentsMDPlugin+"@") {
			out = append(out, "plugin "+p.Source+" (loads AGENTS.md as project instructions)")
		}
	}
	if len(in.Skills) > 0 {
		out = append(out, fmt.Sprintf("skills %q", in.Skills))
	}
	if len(in.SlashCommands) > 0 {
		out = append(out, fmt.Sprintf("slash commands %q", in.SlashCommands))
	}
	if len(in.MCPServers) > 0 {
		out = append(out, fmt.Sprintf("%d MCP servers", len(in.MCPServers)))
	}
	if customAgent != "" && slices.Contains(in.Agents, customAgent) {
		out = append(out, "the planted custom agent "+customAgent)
	}
	if m := strings.TrimSpace(string(in.MemoryPaths)); m != "" && m != "null" && m != "{}" {
		out = append(out, "auto-memory paths "+m)
	}
	if f := InboxFinding(in); f != "" {
		out = append(out, f)
	}
	return out
}

// RecordFixedSession is the session id every recording carries in place
// of the probe's own.
const RecordFixedSession = "0b9f6a52-3c1d-4e8f-9a7b-1c2d3e4f5a6b"

var socketPath = regexp.MustCompile(`"[^"]*/cc-socks[^"/]*/[0-9]+\.sock"`)

// Write (re)writes dir/name: the recording of argv/env (observedWith
// appended only to read the init), with the probe's paths replaced by the
// stand-ins of redact (longest first), its session id by
// RecordFixedSession and a default inbox socket path by a stand-in.
func Write(dir, name string, argv, env, observedWith []string, initRaw json.RawMessage, version, note string, redact map[string]string) error {
	keys := make([]string, 0, len(redact))
	for k := range redact {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	s := string(initRaw)
	for _, k := range keys {
		s = strings.ReplaceAll(s, k, redact[k])
	}
	s = socketPath.ReplaceAllString(s, `"<runtime-dir>/cc-socks/<pid>.sock"`)
	var initObj map[string]any
	if err := json.Unmarshal([]byte(s), &initObj); err != nil {
		return err
	}
	delete(initObj, "uuid")
	if _, ok := initObj["session_id"]; ok {
		initObj["session_id"] = RecordFixedSession
	}
	rec := map[string]any{"claude_code_version": version, "recorded": time.Now().UTC().Format("2006-01-02"),
		"argv": argv, "env": env, "init": initObj}
	if len(observedWith) > 0 {
		rec["observed_with"] = observedWith
	}
	if note != "" {
		rec["note"] = note
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), out.Bytes(), 0o644)
}

// FirstInit answers the first `system/init` record of a stream-json
// output, nil without one.
func FirstInit(stream []byte) json.RawMessage {
	for _, line := range bytes.Split(stream, []byte("\n")) {
		var rec struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Type == "system" && rec.Subtype == "init" {
			return append(json.RawMessage(nil), line...)
		}
	}
	return nil
}

// Result answers the stream's `result` record: whether it is an error and
// its text ("" and false without one).
func Result(stream []byte) (isError bool, text string, ok bool) {
	for _, line := range bytes.Split(stream, []byte("\n")) {
		var rec struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Type == "result" {
			return rec.IsError, rec.Result, true
		}
	}
	return false, "", false
}
