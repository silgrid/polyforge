package cellcfg

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NormalizeServer answers the one comparable spelling of an AIHub base URL:
// scheme://host:port, lowercase, the default port made explicit, no path
// and no trailing slash. Two Cells whose normalized servers are equal share
// an identity domain, which is refused.
func NormalizeServer(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("server is empty")
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("server %q is not an absolute http(s) URL", raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("server %q is not an http(s) URL", raw)
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	return scheme + "://" + host + ":" + port, nil
}

// Party is one Cell's identity-domain facts the conflict rules compare.
type Party struct {
	Profile    string
	Server     string   // raw server (normalized here)
	Workspaces []string // mapped workspace paths
}

// ResolvePath answers a workspace path's physical absolute spelling
// (symlinks resolved when the path exists, cleaned otherwise), so two
// spellings of one directory compare equal.
func ResolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// Nested reports whether a and b (physical paths) are equal or one is the
// other's ancestor.
func Nested(a, b string) bool {
	if a == b {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(a, strings.TrimSuffix(b, sep)+sep) || strings.HasPrefix(b, strings.TrimSuffix(a, sep)+sep)
}

// Conflicts applies the conflict rules to the default Cell (the baseline)
// and the named Cells. It answers profile → reason for every invalid named
// Cell; the default Cell is never invalidated here (it is the baseline).
// The answer does not depend on the order of named: every party of a
// conflict between named Cells is invalid, and a named Cell conflicting
// with the default is invalid.
func Conflicts(def *Party, named []Party) map[string]string {
	out := map[string]string{}
	type norm struct {
		server string
		ws     []string
	}
	n := func(p Party) norm {
		s, err := NormalizeServer(p.Server)
		if err != nil {
			s = ""
		}
		var ws []string
		for _, w := range p.Workspaces {
			ws = append(ws, ResolvePath(w))
		}
		return norm{s, ws}
	}
	add := func(profile, reason string) {
		if prev, ok := out[profile]; ok {
			if strings.Contains(prev, reason) {
				return
			}
			out[profile] = prev + "; " + reason
			return
		}
		out[profile] = reason
	}
	conflict := func(a, b norm) string {
		if a.server != "" && a.server == b.server {
			return "same AIHub server " + a.server
		}
		for _, x := range a.ws {
			for _, y := range b.ws {
				if Nested(x, y) {
					return fmt.Sprintf("workspace %s overlaps %s", x, y)
				}
			}
		}
		return ""
	}
	sorted := append([]Party(nil), named...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Profile < sorted[j].Profile })
	norms := make([]norm, len(sorted))
	for i, p := range sorted {
		norms[i] = n(p)
	}
	if def != nil {
		dn := n(*def)
		for i, p := range sorted {
			if why := conflict(norms[i], dn); why != "" {
				add(p.Profile, "conflicts with the default Cell: "+why)
			}
		}
	}
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if why := conflict(norms[i], norms[j]); why != "" {
				add(sorted[i].Profile, fmt.Sprintf("conflicts with Cell %s: %s", sorted[j].Profile, why))
				add(sorted[j].Profile, fmt.Sprintf("conflicts with Cell %s: %s", sorted[i].Profile, why))
			}
		}
	}
	return out
}
