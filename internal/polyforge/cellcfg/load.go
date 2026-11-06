package cellcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// CellFile is one named Cell's configuration file found next to the Host
// file. NameErr is set when the file's profile is not a valid named
// profile (cli-default.toml, a bad name): that Cell is invalid, visibly.
type CellFile struct {
	Profile string
	Path    string
	NameErr error
}

// Discover lists the named Cell files in the Host file's directory, sorted
// by profile. A retired instances/ directory there is refused by name: it
// is not a configuration form.
func Discover(dir string) ([]CellFile, error) {
	if fi, err := os.Stat(filepath.Join(dir, InstancesDirName)); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("%s: the %s/ directory is not a configuration form — move each instance into %s as cli-<profile>.toml and remove the directory",
			filepath.Join(dir, InstancesDirName), InstancesDirName, dir)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []CellFile
	for _, e := range ents {
		if e.IsDir() || e.Name() == HostFileName {
			continue
		}
		p, ok := ProfileOfFile(e.Name())
		if !ok {
			continue
		}
		out = append(out, CellFile{Profile: p, Path: filepath.Join(dir, e.Name()), NameErr: ValidNamedProfile(p)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Profile < out[j].Profile })
	return out, nil
}

// IndexEntry is one row of the effective workspace index: a mapped
// workspace and the Cell that owns it.
type IndexEntry struct {
	Profile   string `json:"profile"`
	Workspace string `json:"workspace"`
}

// MatchCwd answers the one Cell whose mapped workspace is cwd or one of its
// ancestors (both sides symlink-resolved). Zero or several matching Cells
// answer ok=false: the resolver then falls through to default.
func MatchCwd(index []IndexEntry, cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	c := ResolvePath(cwd)
	match := ""
	for _, e := range index {
		w := ResolvePath(e.Workspace)
		if c != w && !isUnder(c, w) {
			continue
		}
		if match != "" && match != e.Profile {
			return "", false
		}
		match = e.Profile
	}
	return match, match != ""
}

func isUnder(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !filepath.IsAbs(rel) && !startsDotDot(rel)
}

func startsDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(os.PathSeparator)
}
