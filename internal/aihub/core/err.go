package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/silgrid/polyforge/internal/common/api"
)

// Sha256Hex is the storage digest for every secret (api keys, push
// credentials, project identifiers) and the HTTP auth middleware's
// token hash alike: only hashes are persisted, plaintext is handed out
// exactly once.
func Sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Fail builds the typed wire error. Reason is the machine-readable
// sub-reason carried in Extra (ALREADY_CLAIMED etc.); Detail is the human
// sentence.
func Fail(code, reason, format string, args ...any) *api.Error {
	detail := fmt.Sprintf(format, args...)
	e := &api.Error{Code: code, Detail: detail}
	if reason != "" {
		raw, _ := json.Marshal(map[string]string{"reason": reason})
		e.Extra = raw
	}
	return e
}
