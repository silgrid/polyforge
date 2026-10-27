package cli

// config_edit.go: the byte-preserving cli.toml editor. Every edit is a
// line-level rewrite: a set on an existing key replaces exactly that
// line (comments, blank lines, order and every other byte survive); an
// unset removes exactly that key's line (or a whole section). The
// editor deliberately does NOT validate — the caller re-parses the
// resulting text through the one shared parser/validator and only writes
// when that accepts, so a broken file can be repaired by an edit that
// fixes the offending line.

import (
	"fmt"
	"strconv"
	"strings"
)

// configLines is the mutable line view of one file.
type configLines struct {
	lines []string
}

func newConfigLines(text string) *configLines { return &configLines{lines: strings.Split(text, "\n")} }

func (c *configLines) String() string { return strings.Join(c.lines, "\n") }

// trimmed answers the whitespace-trimmed content of a line.
func (c *configLines) trimmed(i int) string { return strings.TrimSpace(c.lines[i]) }

// isHeader reports whether line i is a `[...]` section header.
func (c *configLines) isHeader(i int) bool {
	t := c.trimmed(i)
	return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
}

// lineKey answers the `key` of a `key = ...` line, "" otherwise.
func (c *configLines) lineKey(i int) string {
	t := c.trimmed(i)
	if t == "" || strings.HasPrefix(t, "#") || c.isHeader(i) {
		return ""
	}
	eq := strings.IndexByte(t, '=')
	if eq < 0 {
		return ""
	}
	return strings.TrimSpace(t[:eq])
}

// findFlat locates the first `key =` line for a flat key (-1 = absent).
func (c *configLines) findFlat(key string) int {
	for i := range c.lines {
		if c.lineKey(i) == key {
			return i
		}
	}
	return -1
}

// findDaemonHeader locates the `[daemon]` header (-1 = absent).
func (c *configLines) findDaemonHeader() int {
	for i := range c.lines {
		if c.trimmed(i) == "[daemon]" {
			return i
		}
	}
	return -1
}

// firstSection answers the index of the first section header (len when
// none) — the insertion point for a new flat key.
func (c *configLines) firstSection() int {
	for i := range c.lines {
		if c.isHeader(i) {
			return i
		}
	}
	return len(c.lines)
}

// insertAt inserts newLines at index i.
func (c *configLines) insertAt(i int, newLines ...string) {
	tail := append([]string(nil), c.lines[i:]...)
	c.lines = append(append(c.lines[:i:i], newLines...), tail...)
}

// remove deletes lines [from, to).
func (c *configLines) remove(from, to int) {
	c.lines = append(c.lines[:from:from], c.lines[to:]...)
}

// appendBlock appends a block separated from the existing content by a
// blank line and keeps the file newline-terminated. It drops the
// implicit trailing empty element first so blocks never double up.
func (c *configLines) appendBlock(block ...string) {
	if n := len(c.lines); n > 0 && c.lines[n-1] == "" {
		c.lines = c.lines[:n-1]
	}
	if len(c.lines) > 0 {
		c.lines = append(c.lines, "")
	}
	c.lines = append(c.lines, block...)
	c.lines = append(c.lines, "")
}

// setFlat sets (or inserts) a flat key line. An existing line is replaced
// in place, byte-preserving every other line.
func (c *configLines) setFlat(key, value string) {
	line := fmt.Sprintf("%s = %s", key, strconv.Quote(value))
	if i := c.findFlat(key); i >= 0 {
		c.lines[i] = line
		return
	}
	i := c.firstSection()
	if i == len(c.lines) && i > 0 && c.lines[i-1] == "" {
		i-- // insert before the file's trailing newline
	}
	c.insertAt(i, line)
}

// unsetFlat removes a flat key's line (no-op when absent).
func (c *configLines) unsetFlat(key string) {
	if i := c.findFlat(key); i >= 0 {
		c.remove(i, i+1)
	}
}

// findDaemonBody answers [header+1, end) of the [daemon] table (false
// when absent), where end is the next section header or EOF.
func (c *configLines) findDaemonBody() (int, int, bool) {
	h := c.findDaemonHeader()
	if h < 0 {
		return 0, 0, false
	}
	end := len(c.lines)
	for i := h + 1; i < len(c.lines); i++ {
		if c.isHeader(i) {
			end = i
			break
		}
	}
	return h + 1, end, true
}

// setDaemon sets (or inserts) a [daemon] key line.
func (c *configLines) setDaemon(key, value string) {
	if start, end, ok := c.findDaemonBody(); ok {
		for i := start; i < end; i++ {
			if c.lineKey(i) == key {
				c.lines[i] = fmt.Sprintf("%s = %s", key, value)
				return
			}
		}
		// insert at the end of the table body, trimming a trailing blank
		insert := end
		for insert > start && c.lines[insert-1] == "" {
			insert--
		}
		c.insertAt(insert, fmt.Sprintf("%s = %s", key, value))
		return
	}
	c.appendBlock("[daemon]", fmt.Sprintf("%s = %s", key, value))
}

// unsetDaemon removes a [daemon] key's line.
func (c *configLines) unsetDaemon(key string) {
	start, end, ok := c.findDaemonBody()
	if !ok {
		return
	}
	for i := start; i < end; i++ {
		if c.lineKey(i) == key {
			c.remove(i, i+1)
			return
		}
	}
}

// workspaceHeader locates the header line for exactly this workspace key
// (-1 = absent).
func (c *configLines) workspaceHeader(key string) int {
	for i := range c.lines {
		if !c.isHeader(i) {
			continue
		}
		name, err := parseSectionHeader(c.trimmed(i), i+1)
		if err != nil {
			continue
		}
		if name == key {
			return i
		}
	}
	return -1
}

// setWorkspace replaces a whole [workspace."<key>"] section in place or
// appends it.
func (c *configLines) setWorkspace(key string, projects []string) {
	block := []string{
		fmt.Sprintf("[workspace.%s]", strconv.Quote(key)),
		"projects = [" + quoteList(projects) + "]",
	}
	if h := c.workspaceHeader(key); h >= 0 {
		end := len(c.lines)
		for i := h + 1; i < len(c.lines); i++ {
			if c.isHeader(i) {
				end = i
				break
			}
		}
		// trim trailing blank lines inside the old body so the replacement
		// keeps exactly one separating blank line
		for end > h+1 && c.lines[end-1] == "" {
			end--
		}
		c.lines = append(c.lines[:h:h], append(block, c.lines[end:]...)...)
		return
	}
	c.appendBlock(block...)
}

// unsetWorkspace removes a whole [workspace."<key>"] section.
func (c *configLines) unsetWorkspace(key string) {
	h := c.workspaceHeader(key)
	if h < 0 {
		return
	}
	end := len(c.lines)
	for i := h + 1; i < len(c.lines); i++ {
		if c.isHeader(i) {
			end = i
			break
		}
	}
	// swallow one immediately preceding blank line so no double gap is
	// left behind
	if h > 0 && c.lines[h-1] == "" {
		h--
	}
	c.remove(h, end)
}

// quoteList renders `["a", "b"]`'s inner body.
func quoteList(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = strconv.Quote(s)
	}
	return strings.Join(parts, ", ")
}
