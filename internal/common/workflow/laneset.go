package workflow

// laneset.go: the lane set a work item's lane is resolved in
// (docs/reference/workflow-contract.md, "User lanes"). A daemon runs the
// built-in lanes its release ships (its workflows dir, which also holds
// the shared tree every lane falls back on) and, for each workspace it
// serves, that workspace's user lanes under
// <workspace>/.polyforge/workflows/<lane>/. A work item's lane is
// resolved per work item: its project's workspace names the set, the set
// answers the lane by name. A user lane never shadows a built-in one: the
// built-in lane of a name always wins, and the user lane that carries the
// name is refused (WFC-122). A workspace has no shared tree of its own: a
// user lane's definitions and fragments fall back on the built-in one.

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// UserLanesDir is a workspace's user lane root, relative to the
// workspace root.
const UserLanesDir = ".polyforge/workflows"

// SourceWorkspace is the snapshot source of a workspace's user lane.
const SourceWorkspace = "workspace"

// UserLanesRoot answers the user lane root of the workspace at ws ("" for
// no workspace).
func UserLanesRoot(ws string) string {
	if ws == "" {
		return ""
	}
	return filepath.Join(ws, filepath.FromSlash(UserLanesDir))
}

// LaneSet is the lanes one workspace runs work items on: the built-in
// lanes under Builtin (the daemon's workflows dir; "" = none installed)
// and the user lanes under User (UserLanesRoot of the workspace; "" =
// none).
type LaneSet struct {
	Builtin string
	User    string
}

// ForWorkspace answers the lane set of the workspace at ws (""
// = no workspace: the built-in lanes alone) over the built-in lanes
// under builtin.
func ForWorkspace(builtin, ws string) LaneSet {
	return LaneSet{Builtin: builtin, User: UserLanesRoot(ws)}
}

// shared is the built-in shared tree every lane of the set falls back on.
func (s LaneSet) shared() string {
	return filepath.Join(s.Builtin, SharedDir)
}

// Resolve answers the directory and source of the lane name in the set:
// the built-in lane when Builtin carries it (a user lane of that name
// never shadows it), else the workspace's user lane; ok false when the
// set carries no lane of that name (a claim's WFC-123). A set with no
// built-in lanes carries none: a user lane needs the built-in shared tree.
func (s LaneSet) Resolve(name string) (dir, source string, ok bool) {
	if s.Builtin == "" {
		return "", "", false
	}
	if LanePresent(s.Builtin, name) {
		return filepath.Join(s.Builtin, name), SourceBuiltin, true
	}
	if s.User != "" && LanePresent(s.User, name) {
		return filepath.Join(s.User, name), SourceWorkspace, true
	}
	return "", "", false
}

// LoadLane reads the lane at dir of the given source through the one
// lane loader: a built-in lane (and a capsule snapshot of either source,
// whose shared tree the claim laid out beside it) by Load, a user lane in
// place by LoadUser over shared.
func LoadLane(dir, source, shared string) (*Lane, []ContractError) {
	if source == SourceWorkspace {
		return LoadUser(dir, shared)
	}
	return Load(dir)
}

// Load reads the lane name the set resolves through the one lane loader:
// (nil, "", nil) when the set does not carry it, else the lane, its
// source and — when the loader refuses it — the *LoadError naming every
// defect (the lane nil).
func (s LaneSet) Load(name string) (*Lane, string, error) {
	dir, source, ok := s.Resolve(name)
	if !ok {
		return nil, "", nil
	}
	lane, errs := LoadLane(dir, source, s.shared())
	if err := AsLoadError(dir, errs); err != nil {
		return nil, source, err
	}
	return lane, source, nil
}

// SetLane is one lane directory of a set and the loader's verdict on it.
type SetLane struct {
	Name   string
	Dir    string
	Source string
	// Lane is the lane as far as the loader could read it (nil when its
	// workflow.yaml cannot be read at all).
	Lane   *Lane
	Errors []ContractError
	// Shadowed marks a user lane whose name a built-in lane carries: it
	// never runs (WFC-122 among its Errors).
	Shadowed bool
}

// Lanes loads every lane of the set: each built-in lane, then each user
// lane, in name order. A user lane whose name a built-in lane carries is
// listed, refused with WFC-122 on top of whatever else the loader finds;
// Resolve never answers it. A missing lanes root holds no lane; an
// unreadable one is an error.
func (s LaneSet) Lanes() ([]SetLane, error) {
	out, err := s.builtinLanes()
	if err != nil {
		return nil, err
	}
	user, err := s.UserLanes()
	if err != nil {
		return nil, err
	}
	return append(out, user...), nil
}

// builtinLanes loads every built-in lane of the set.
func (s LaneSet) builtinLanes() ([]SetLane, error) {
	dirs, err := laneDirsIfAny(s.Builtin)
	if err != nil {
		return nil, err
	}
	out := make([]SetLane, 0, len(dirs))
	for _, d := range dirs {
		lane, errs := Load(d)
		out = append(out, SetLane{Name: filepath.Base(d), Dir: d, Source: SourceBuiltin, Lane: lane, Errors: errs})
	}
	return out, nil
}

// UserLanes loads every user lane of the set, in name order, each judged
// by the user lane rules over the built-in shared tree; a user lane whose
// name a built-in lane carries is refused with WFC-122 (Shadowed).
func (s LaneSet) UserLanes() ([]SetLane, error) {
	if s.Builtin == "" {
		return nil, nil
	}
	dirs, err := laneDirsIfAny(s.User)
	if err != nil {
		return nil, err
	}
	out := make([]SetLane, 0, len(dirs))
	for _, d := range dirs {
		name := filepath.Base(d)
		lane, errs := LoadUser(d, s.shared())
		sl := SetLane{Name: name, Dir: d, Source: SourceWorkspace, Lane: lane, Errors: errs}
		if LanePresent(s.Builtin, name) {
			sl.Shadowed = true
			file := filepath.Join(d, WorkflowFile)
			sl.Errors = append([]ContractError{{Code: CodeShadowsBuiltin, File: file, Line: nameLine(file), Path: "name",
				Msg: "a built-in lane is named " + name + ": a user lane never shadows it, so every work item bound to " + name + " runs the built-in lane and this one never runs — rename it (its directory and name)"}}, sl.Errors...)
			sortErrors(sl.Errors)
		}
		out = append(out, sl)
	}
	return out, nil
}

// laneDirsIfAny is LaneDirs answering nothing for a missing (or unset)
// root.
func laneDirsIfAny(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	dirs, err := LaneDirs(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return dirs, err
}

// nameLine answers the line of the `name:` key of the workflow.yaml at file
// (0 when it has none).
func nameLine(file string) int {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for n := 1; sc.Scan(); n++ {
		if strings.HasPrefix(sc.Text(), "name:") {
			return n
		}
	}
	return 0
}

// UserLanesError is the answer of a lane set whose workspace's user lane
// root is unreadable (a file in its place, a permission): the built-in
// lanes still answer, and only that workspace's user lanes are unknown —
// a broken user lane root disables only the user lanes.
type UserLanesError struct {
	Root string
	Err  error
}

func (e *UserLanesError) Error() string {
	return "the user lanes " + e.Root + " are unreadable: " + e.Err.Error()
}

func (e *UserLanesError) Unwrap() error { return e.Err }

// runnable answers the lanes of the set a work item can be bound to as
// far as the loader could read them: every built-in lane and every user
// lane no built-in lane shadows (a lane with defects included: its
// defect is the lane's, reported by its claim and by validate). An
// unreadable built-in lanes root is an error and answers nothing; an
// unreadable user lane root answers the built-in lanes and a
// *UserLanesError.
func (s LaneSet) runnable() ([]*Lane, error) {
	builtin, err := s.builtinLanes()
	if err != nil {
		return nil, err
	}
	all := builtin
	user, uerr := s.UserLanes()
	if uerr != nil {
		uerr = &UserLanesError{Root: s.User, Err: uerr}
	}
	all = append(all, user...)
	var out []*Lane
	for _, sl := range all {
		if sl.Lane != nil && !sl.Shadowed {
			out = append(out, sl.Lane)
		}
	}
	return out, uerr
}

// StepIDs answers the set of step ids the lanes of the set declare — the
// legal per-step keys of the workspace yaml's model lists. A lane with
// defects still contributes the ids the loader could read: the defect is
// the lane's (its claim and `pf workflows validate` report it), never the
// workspace yaml's. An unreadable built-in lanes root is an error (nil
// ids); an unreadable user lane root answers the built-in lanes' ids and a
// *UserLanesError (errors.As).
func (s LaneSet) StepIDs() (map[string]bool, error) {
	return s.stepIDs(func(Step) bool { return true })
}

// SkippableSteps answers the set of step ids some lane of the set declares
// `skippable: true` the steps whose workspace yaml
// `skip` a write may set. Its errors are StepIDs'.
func (s LaneSet) SkippableSteps() (map[string]bool, error) {
	return s.stepIDs(func(st Step) bool { return st.Skippable })
}

// stepIDs answers the ids of the steps of the set's lanes that keep says.
func (s LaneSet) stepIDs(keep func(Step) bool) (map[string]bool, error) {
	if _, err := LaneDirs(s.Builtin); err != nil {
		return nil, err
	}
	lanes, uerr := s.runnable()
	if lanes == nil && uerr != nil && !isUserLanesError(uerr) {
		return nil, uerr
	}
	ids := map[string]bool{}
	for _, lane := range lanes {
		for _, st := range lane.Steps {
			if st.ID != "" && keep(st) {
				ids[st.ID] = true
			}
		}
	}
	return ids, uerr
}

// isUserLanesError reports whether err is a lane set's unreadable user
// lane root (the built-in lanes still answered).
func isUserLanesError(err error) bool {
	var ue *UserLanesError
	return errors.As(err, &ue)
}
