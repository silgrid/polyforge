package capsule

import (
	"os"
	"path/filepath"
	"strings"
)

// DirSubagents is the directory, beside a capsule session transcript,
// that holds the transcripts of the subagents that conversation ran.
const DirSubagents = "subagents"

// SubagentDir answers where the subagent transcripts of the capsule
// session transcript `transcript` live: the directory named like the
// transcript without ".jsonl", under subagents/ — Claude Code's own
// layout (<session>.jsonl + <session>/subagents/), used for both
// harnesses. A conversation's subagents are kept beside the capsule
// transcript that FIRST holds that conversation: a pi dispatch's own
// file (a resumed pi dispatch appends to its predecessor's), and for
// claude the first dispatch of the native session (a resumed claude
// dispatch's capsule copy is a superset of the earlier one, so its
// subagents stay beside the earlier copy and are never copied twice).
func SubagentDir(transcript string) string {
	return filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), DirSubagents)
}

// EnsureSubagentDir validates and creates the subagent directory of a
// capsule session transcript before anything is written there. The
// capsule is agent-writable and a transcript path may come from the
// agent-writable spawn journal, so the transcript must be an absolute
// <name>.jsonl directly inside <capsuleDir>/session/ (the session
// directory symlink-resolved on both sides), and neither the <name>
// directory nor its subagents/ directory may be a symlink, before or
// after creation. It answers the directory, or false when any check
// fails (the caller then writes nothing).
func EnsureSubagentDir(capsuleDir, transcript string) (string, bool) {
	if !filepath.IsAbs(transcript) || !strings.HasSuffix(transcript, ".jsonl") {
		return "", false
	}
	stem := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	if stem == "" || stem == "." || stem == ".." {
		return "", false
	}
	sess, err := filepath.EvalSymlinks(filepath.Join(capsuleDir, DirSession))
	if err != nil {
		return "", false
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(transcript))
	if err != nil || parent != sess {
		return "", false
	}
	stemDir := filepath.Join(filepath.Dir(transcript), stem)
	dir := filepath.Join(stemDir, DirSubagents)
	if !plainDirOrAbsent(stemDir) || !plainDirOrAbsent(dir) {
		return "", false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	if !plainDir(stemDir) || !plainDir(dir) {
		return "", false
	}
	return dir, true
}

func plainDirOrAbsent(p string) bool {
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return true
	}
	return err == nil && st.IsDir()
}

func plainDir(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.IsDir()
}
