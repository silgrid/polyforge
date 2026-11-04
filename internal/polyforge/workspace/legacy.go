package workspace

// legacy.go: the two root sections `.polyforge.yaml` no longer carries —
// `summarize:` and `memory_policy:` moved to cli.toml. A
// file still carrying either is refused whole (no dual read); this file is
// the one reader of those sections, shared by the loader's refusal, `pf
// doctor` and `pf config migrate` (the only consumer of their values).
//
// Deletion condition (AGENTS.md, "no backward-compatibility code"): the
// refusal, LegacySections and `pf config migrate` are the transition a
// safe rollback needs; they are deleted together once every mapped
// workspace on the supported install base has migrated.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// LegacyKeys are the retired root sections, in the order the refusal
// names them.
var LegacyKeys = []string{SummarizeKey, MemoryPolicyKey}

// MigrateHint is the instruction every legacy refusal carries.
const MigrateHint = "run `pf config migrate` (it moves the values into cli.toml and deletes the section)"

// LegacySection is one retired root section of a workspace yaml: its key,
// the 1-based line of the key, the value node, and the line span the
// section occupies — [Start, End), 0-based line indexes — including the
// `pf init` comment block written above a memory_policy section and
// excluding the comment/blank lines that precede the next root key.
type LegacySection struct {
	Key        string
	Line       int
	Value      *yaml.Node
	Start, End int
}

// LegacySections answers the retired root sections raw carries (file
// order). A document that is not a YAML mapping answers none (the strict
// loader names that defect); a decode error is returned as-is.
func LegacySections(raw []byte) ([]LegacySection, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(raw)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	root := deref(doc.Content[0])
	if root.Kind != yaml.MappingNode {
		return nil, nil
	}
	lines := strings.Split(string(raw), "\n")
	var out []LegacySection
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Kind != yaml.ScalarNode || !contains(LegacyKeys, k.Value) {
			continue
		}
		end := len(lines)
		if i+2 < len(root.Content) {
			end = root.Content[i+2].Line - 1
		}
		start := k.Line - 1
		// the comment/blank run right before the next root key belongs to it
		for end > start+1 && rootCommentOrBlank(lines[end-1]) {
			end--
		}
		if k.Value == MemoryPolicyKey {
			// the comment block `pf init` wrote above the section goes with it
			c := start
			for c > 0 && strings.HasPrefix(lines[c-1], "#") {
				c--
			}
			if c < start && strings.HasPrefix(lines[c], "# memory_policy:") {
				start = c
			}
		}
		out = append(out, LegacySection{Key: k.Value, Line: k.Line, Value: deref(root.Content[i+1]), Start: start, End: end})
	}
	return out, nil
}

func rootCommentOrBlank(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || (strings.HasPrefix(line, "#"))
}

// LegacyReason is the refusal of a yaml carrying a retired section: it
// names the key, its line and the migrate command ("" = none carried).
func LegacyReason(sections []LegacySection) string {
	if len(sections) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		parts = append(parts, fmt.Sprintf("line %d: `%s:`", s.Line, s.Key))
	}
	return strings.Join(parts, ", ") + " moved to cli.toml and is no longer read from the workspace yaml — " + MigrateHint
}

// RemoveLegacySections answers raw without the given sections' line
// spans (every other byte kept); a blank line left doubled (or trailing)
// by a removal is dropped once.
func RemoveLegacySections(raw []byte, sections []LegacySection) []byte {
	lines := strings.Split(string(raw), "\n")
	drop := make([]bool, len(lines))
	for _, s := range sections {
		for i := s.Start; i < s.End && i < len(lines); i++ {
			drop[i] = true
		}
		before, after := s.Start-1, s.End
		if before >= 0 && strings.TrimSpace(lines[before]) == "" && !drop[before] &&
			(after >= len(lines) || strings.TrimSpace(lines[after]) == "") {
			drop[before] = true
		}
	}
	kept := make([]string, 0, len(lines))
	for i, l := range lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	return []byte(strings.Join(kept, "\n"))
}
