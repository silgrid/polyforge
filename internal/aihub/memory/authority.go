// Package memory is the v1 Core-side memory store: FTS5-trigram search
// over memories kept entirely in the Core database.
//
// Each memory is one memories row whose content column holds the stored
// document — a YAML frontmatter block mirroring the row's columns, then
// the body verbatim. content_sha and content_len describe that document;
// search reads the memory_fts row written in the same transaction
// (delete-then-insert). Semantics of the search face are ported from the
// pilot-validated v0 implementation (design authority
// 06-memory-search.md).
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/silgrid/polyforge/internal/common/api"
)

// APIError mirrors the other aihub packages.
type APIError struct {
	Code   string
	Reason string
	Detail string
}

func (e *APIError) Error() string {
	if e.Reason != "" {
		return e.Code + " " + e.Reason + ": " + e.Detail
	}
	return e.Code + ": " + e.Detail
}

func errf(code, reason, format string, args ...any) *APIError {
	return &APIError{Code: code, Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Visibility tiers (06 §6; membership-first filtering is the server's).
var visibilities = map[string]bool{"admin": true, "private": true, "project": true, "public": true, "team": true}

// typeKinds is the memory type namespace: a type is kind or kind.name,
// its kind (before the first dot) one of these. This var is the one
// authority (polyforge#210); docs that list the kinds mirror it and
// change with it.
var typeKinds = []string{"experience", "fact", "doc", "rule", "methodology"}

// checkType refuses a type outside the namespace with MEM-400 naming the
// rejected type and the valid kinds.
func checkType(typ string) error {
	if typ == "" {
		return errf(api.ErrMemBadInput, "", "type required")
	}
	kind, _, _ := strings.Cut(typ, ".")
	if !slices.Contains(typeKinds, kind) {
		return errf(api.ErrMemBadInput, "", "type %q: kind (before the first dot) must be one of %s",
			typ, strings.Join(typeKinds, "|"))
	}
	return nil
}

// maxContentBytes is the MEM-413 body limit (≤ 1 MiB).
const maxContentBytes = 1 << 20

// frontmatter is the §4.2 field set, one-to-one mirror of the memories
// columns, serialized in this exact order (deterministic document bytes).
type frontmatter struct {
	ID             string   `yaml:"id"`
	Type           string   `yaml:"type"`
	OwnerUserID    string   `yaml:"owner_user_id"`
	Title          *string  `yaml:"title,omitempty"`
	Tags           []string `yaml:"tags,flow,omitempty"`
	Visibility     string   `yaml:"visibility"`
	WorkItemID     *string  `yaml:"work_item_id,omitempty"`
	ProjectID      string   `yaml:"project_id"`
	IdempotencyKey *string  `yaml:"idempotency_key,omitempty"`
	Related        []string `yaml:"related,flow,omitempty"`
	CreatedAt      string   `yaml:"created_at"`
	UpdatedAt      string   `yaml:"updated_at"`
}

// splitFrontmatter splits a stored document at its leading YAML
// frontmatter block. A leading "---" that never closes is a thematic
// break, not frontmatter: the whole content is the body.
func splitFrontmatter(content string) (fmBlock, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	idx := strings.Index(content[4:], "\n---")
	if idx < 0 {
		return "", content, false
	}
	end := 4 + idx
	rest := content[end+4:]
	if rest != "" && rest[0] != '\n' && rest[0] != '\r' {
		return "", content, false
	}
	return content[4:end], rest, true
}

// Body answers the memory body of a stored document: the
// leading frontmatter block and its closing delimiter line are dropped,
// everything after is returned verbatim (a "---" line inside the body is
// content, never a second delimiter). A document without a closed
// frontmatter block is all body.
func Body(raw []byte) string {
	_, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return body
	}
	body = strings.TrimPrefix(body, "\r")
	return strings.TrimPrefix(body, "\n")
}

// parseFrontmatterBlock parses a frontmatter block into a generic map.
func parseFrontmatterBlock(block string) (map[string]any, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(block), &raw); err != nil {
		return nil, errf(api.ErrMemBadInput, "FRONTMATTER_INVALID", "%v", err)
	}
	if raw == nil {
		return nil, errf(api.ErrMemBadInput, "FRONTMATTER_INVALID", "empty frontmatter block")
	}
	return raw, nil
}

// buildDocument renders the stored document: frontmatter, then body.
func buildDocument(fm *frontmatter, body string) (string, error) {
	y, err := yaml.Marshal(fm)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.Write(y)
	if !strings.HasSuffix(sb.String(), "\n") {
		sb.WriteByte('\n')
	}
	sb.WriteString("---\n")
	sb.WriteString(body)
	return sb.String(), nil
}

// extractBody derives the searchable text (06 §5.1): strip frontmatter,
// drop structural '#','*','_','>' from regular lines; fenced and indented
// code blocks stay verbatim.
func extractBody(content string) string {
	_, body, _ := splitFrontmatter(content)
	var sb strings.Builder
	inFence := false
	var fence string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case inFence:
			if isFenceLine(trimmed, fence) {
				inFence = false
			}
			sb.WriteString(line)
		case isFenceOpener(trimmed, &fence):
			inFence = true
			sb.WriteString(line)
		case strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t"):
			sb.WriteString(line)
		default:
			sb.WriteString(stripStructural(line))
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

func stripStructural(line string) string {
	var sb strings.Builder
	sb.Grow(len(line))
	for _, r := range line {
		switch r {
		case '#', '*', '_', '>':
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func isFenceOpener(trimmed string, fence *string) bool {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			*fence = f
			return true
		}
	}
	return false
}

func isFenceLine(trimmed, fence string) bool {
	if len(trimmed) < len(fence) {
		return false
	}
	marker := fence[0]
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != marker {
			return false
		}
	}
	return true
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
