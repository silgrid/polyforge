package workflow

// skill.go: a step's skill (workflow contract v2, design §3.1; contract 2).
// A skill is a markdown file of the lane snapshot — `<lane>/skills/<name>.md`,
// else the shared `_shared/skills/<name>.md` — whose body the driver renders
// into the step's prompt as the `# Skill` section: the pre-written commands
// the step runs mechanically, the upstream it verifies first, the routes
// on each exit code and the things it must not do. No harness-native
// skill is involved (a claude step runs under --safe-mode, which turns
// those off; pi has its own mechanism), so one file serves both harnesses
// and freezes with the capsule. Its frontmatter carries `name` (the file
// name), `description` and `commands` (the bare names of the scripts it
// runs, e.g. `verify.py`); a file that resolves nowhere, a frontmatter
// outside that closed set or an empty body is WFC-200.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill is one parsed skill file.
type Skill struct {
	Path        string
	Name        string
	Description string
	// Commands are the bare names of the scripts the skill runs.
	Commands []string
	// Body is the prompt text (the `# Skill` section's content).
	Body string
}

// SkillPath answers the skill file of name for the lane at dir: the
// lane's own skills/<name>.md when it exists, else the shared
// ../_shared/skills/<name>.md when that exists, else the lane's own path
// (so the caller's read error names the expected location).
func SkillPath(dir, name string) string {
	return skillPath(dir, SiblingShared(dir), name)
}

// skillPath is SkillPath over an explicit shared tree.
func skillPath(dir, shared, name string) string {
	own := filepath.Join(dir, SkillsDir, name+".md")
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		return own
	}
	sharedSkill := filepath.Join(shared, SkillsDir, name+".md")
	if _, err := os.Stat(sharedSkill); err == nil {
		return sharedSkill
	}
	return own
}

// ReadSkill reads and judges the skill name of the lane at dir — the
// dispatch-time read of what Load judged. A missing file answers an error
// os.IsNotExist holds for; a defect of the file is an error naming it.
func ReadSkill(dir, name string) (*Skill, error) {
	return readSkill(dir, SiblingShared(dir), name)
}

// readSkill is ReadSkill over an explicit shared tree.
func readSkill(dir, shared, name string) (*Skill, error) {
	path := skillPath(dir, shared, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sk, errs := parseSkill(path, name, raw)
	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Msg)
		}
		return nil, fmt.Errorf("%s: %s: %s", path, CodeSkill, strings.Join(msgs, "; "))
	}
	return sk, nil
}

// skill judges the step's skill file at load (WFC-200): it resolves, its
// frontmatter is the closed set with the file's name, a description and
// the commands list, and its body is not empty.
func (l *loader) skill(s *Step) {
	path := skillPath(l.dir, l.shared, s.Skill)
	raw, err := os.ReadFile(path)
	if err != nil {
		l.at(CodeSkill, path, 0, nil, "", "skill %q resolves to no file (want %s under the lane, or %s): %v",
			s.Skill, SkillsDir+"/"+s.Skill+".md", l.sharedRel(SkillsDir+"/"+s.Skill+".md"), err)
		return
	}
	_, errs := parseSkill(path, s.Skill, raw)
	l.errs = append(l.errs, errs...)
}

// parseSkill decodes one skill file and judges it: a frontmatter of the
// closed key set (skillKeys), name equal to the file's name, a non-empty
// description, a commands list of non-empty strings, and a non-empty
// body.
func parseSkill(path, name string, raw []byte) (*Skill, []ContractError) {
	l := &loader{}
	root, body, hasFront, err := decodeDefinition(raw)
	switch {
	case !hasFront:
		l.at(CodeSkill, path, 0, nil, "", "no frontmatter block (a skill declares name, description and commands)")
		return nil, l.errs
	case err != nil:
		l.at(CodeSkill, path, 0, nil, "", "%v", err)
		return nil, l.errs
	}
	const off = 1
	sk := &Skill{Path: path, Body: body}
	keys := map[string]*yaml.Node{}
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		switch {
		case seen[k.Value]:
			l.at(CodeSkill, path, off, k, k.Value, "duplicate key")
		case skillKeys[k.Value].Type == "":
			l.at(CodeSkill, path, off, k, k.Value, "unknown key (a skill takes %s)", strings.Join(sortedKeys(skillKeys), ", "))
		default:
			keys[k.Value] = v
		}
		seen[k.Value] = true
	}
	if n, ok := keys["name"]; !ok {
		l.at(CodeSkill, path, off, root, "name", "the skill declares no name (want %q)", name)
	} else if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value != name {
		l.at(CodeSkill, path, off, n, "name", "name %q is not the skill's file name %q", n.Value, name)
	} else {
		sk.Name = n.Value
	}
	if n, ok := keys["description"]; !ok || n.Kind != yaml.ScalarNode || strings.TrimSpace(n.Value) == "" {
		at := root
		if ok {
			at = n
		}
		l.at(CodeSkill, path, off, at, "description", "the skill declares no description")
	} else {
		sk.Description = n.Value
	}
	if n, ok := keys["commands"]; !ok {
		l.at(CodeSkill, path, off, root, "commands", "the skill declares no commands list (the pre-written commands it runs; [] when none)")
	} else if n.Kind != yaml.SequenceNode {
		l.at(CodeSkill, path, off, n, "commands", "want a list of command names")
	} else {
		sk.Commands = []string{}
		for i, c := range n.Content {
			if c.Kind != yaml.ScalarNode || c.Tag != "!!str" || strings.TrimSpace(c.Value) == "" || strings.ContainsAny(c.Value, " /\\") {
				l.at(CodeSkill, path, off, c, fmt.Sprintf("commands[%d]", i), "want a command name (one word, no path)")
				continue
			}
			sk.Commands = append(sk.Commands, c.Value)
		}
	}
	if strings.TrimSpace(sk.Body) == "" {
		l.at(CodeSkill, path, 0, nil, "", "the skill's body is empty")
	}
	return sk, l.errs
}
