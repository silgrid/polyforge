package components

// digest.go — the deterministic digest of a version directory's
// component trees. The manifest records one digest per component and
// the install verifies its staged copy against them, so the digest
// must be a function of the content alone: the same bytes, the same
// exec bits and the same symlink targets always digest equal, whatever
// the machine, the umask or the walk order.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TreeDigest answers the sha256 of the tree at root (a file counts as
// a one-entry tree). Every entry contributes its slash-normalized
// relative path; a regular file contributes its exec bit and the
// sha256 of its bytes, a symlink its target — never its bytes — and a
// directory contributes nothing but its name (an empty directory is
// still a fact of the layout). skip names top-level entries (files or
// directories) the digest excludes; anything else in the tree is
// covered. No other file type is accepted: a socket or a device in a
// component tree is an error, not a digest.
func TreeDigest(root string, skip []string) (string, error) {
	skips := make(map[string]bool, len(skip))
	for _, s := range skip {
		skips[s] = true
	}
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		first := rel
		if i := strings.IndexByte(rel, '/'); i >= 0 {
			first = rel[:i]
		}
		if skips[first] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsDir():
			fmt.Fprintf(h, "d %s\n", rel)
		case info.Mode().IsRegular():
			kind := "f"
			if info.Mode().Perm()&0o111 != 0 {
				kind = "x"
			}
			sum, err := fileSHA256(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%s %s %s\n", kind, rel, sum)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "l %s -> %s\n", rel, target)
		default:
			return fmt.Errorf("%s: a %s entry is not part of a component tree", p, info.Mode())
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("digest %s: %w", root, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IsDigest reports whether s is a well-formed digest (64 hex chars).
func IsDigest(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// fileSHA256 answers the sha256 of one file's bytes.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
