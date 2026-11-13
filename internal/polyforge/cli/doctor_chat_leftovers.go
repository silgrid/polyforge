package cli

// doctor_chat_leftovers.go: the `retired chat state` row of `pf doctor`
//. The chat-subscription model is retired; the files it
// left behind are dead weight nothing reads any more:
//
//   - <data-dir>/chats.json, chat-subs.json, a chat-status/ directory and
//     migration/chat-subs.json.pre-migration (the cell-layout migration's
//     backup) — the daemon's chat registry and subscription store;
//   - <runtime-dir>/chat-<id>.{lock,pid,status} and session-*.chat — the
//     resident watcher's lock, pidfile, progress file and the statusLine's
//     session→chat cache (runtime dir = $XDG_RUNTIME_DIR/polyforge, else
//     $TMPDIR/polyforge-<uid>).
//
// The row reports them (warn, with the count and size); `pf doctor
// --prune-chat-state` deletes them and lists what it deleted. Removing
// them is safe at any time: no daemon or pf of this release opens them.
// This row is transition code: it goes once no machine carries these
// files (the next release line after every machine ran a prune).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const docChRetiredChat = "retired chat state"

// chatRuntimeDir answers the retired watcher's runtime directory.
func chatRuntimeDir() string {
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "polyforge")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("polyforge-%d", os.Getuid()))
}

// retiredChatFiles answers the leftovers under dataDir and runtimeDir,
// sorted, with their total size.
func retiredChatFiles(dataDir, runtimeDir string) ([]string, int64) {
	var found []string
	var size int64
	add := func(p string) {
		fi, err := os.Lstat(p)
		if err != nil {
			return
		}
		found = append(found, p)
		if fi.IsDir() {
			_ = filepath.Walk(p, func(_ string, i os.FileInfo, err error) error {
				if err == nil && !i.IsDir() {
					size += i.Size()
				}
				return nil
			})
			return
		}
		size += fi.Size()
	}
	for _, name := range []string{"chats.json", "chat-subs.json", "chat-status", filepath.Join("migration", "chat-subs.json.pre-migration")} {
		add(filepath.Join(dataDir, name))
	}
	if des, err := os.ReadDir(runtimeDir); err == nil {
		for _, de := range des {
			n := de.Name()
			chat := strings.HasPrefix(n, "chat-") && (strings.HasSuffix(n, ".lock") || strings.HasSuffix(n, ".pid") || strings.HasSuffix(n, ".status"))
			session := strings.HasPrefix(n, "session-") && strings.HasSuffix(n, ".chat")
			if chat || session {
				add(filepath.Join(runtimeDir, n))
			}
		}
	}
	sort.Strings(found)
	return found, size
}

// retiredChatStateCheck reports (prune=false) or deletes the leftovers.
func retiredChatStateCheck(dataDir, runtimeDir string, prune bool) doctorCheck {
	c := doctorCheck{Name: docChRetiredChat}
	found, size := retiredChatFiles(dataDir, runtimeDir)
	if len(found) == 0 {
		c.Status, c.Detail = "pass", "no chat-subscription state left (chats.json, chat-subs.json, chat-status/, watcher runtime files)"
		return c
	}
	sample := found
	more := ""
	if len(sample) > 5 {
		sample, more = found[:5], fmt.Sprintf(" (+%d more)", len(found)-5)
	}
	if !prune {
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%d file(s), %d bytes of retired chat-subscription state (; nothing reads it): %s%s — `pf doctor --prune-chat-state` deletes them",
			len(found), size, strings.Join(sample, ", "), more)
		return c
	}
	var failed []string
	for _, p := range found {
		if err := os.RemoveAll(p); err != nil {
			failed = append(failed, p+": "+err.Error())
		}
	}
	if len(failed) > 0 {
		c.Status, c.Detail = "warn", fmt.Sprintf("deleted %d of %d retired chat-state file(s); could not delete: %s", len(found)-len(failed), len(found), strings.Join(failed, "; "))
		return c
	}
	c.Status, c.Detail = "pass", fmt.Sprintf("deleted %d retired chat-state file(s), %d bytes: %s%s", len(found), size, strings.Join(sample, ", "), more)
	return c
}
