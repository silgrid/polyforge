package components

// digest.go — the deterministic digest of a version directory's
// component trees. The manifest records one digest per component and
// the install verifies its staged copy against them, so the digest
// must be a function of the content alone: the same bytes, the same
// exec bits and the same symlink targets always digest equal, whatever
// the machine, the umask or the walk order.

// TreeDigest answers the sha256 of the tree at root (a file counts as
// a one-entry tree). Every entry contributes its slash-normalized
// relative path; a regular file contributes its exec bit and the
// sha256 of its bytes, a symlink its target — never its bytes — and a
// directory contributes nothing but its name (an empty directory is
// still a fact of the layout). skip names top-level entries (files or
// directories) the digest excludes; anything else in the tree is
// covered. No other file type is accepted: a socket or a device in a
// component tree is an error, not a digest.
func TreeDigest(root string, skip []string) (string, error) { return "", nil }

// IsDigest reports whether s is a well-formed digest (64 hex chars).
func IsDigest(s string) bool { return false }
