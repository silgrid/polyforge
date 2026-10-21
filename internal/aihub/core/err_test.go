package core

// err_test.go: typed-error inspection helpers shared by the core pins.

import (
	"encoding/json"

	"github.com/silgrid/polyforge/internal/common/api"
)

// IsCode reports whether err is a typed *api.Error with the given code.
func IsCode(err error, code string) bool {
	ae, ok := err.(*api.Error)
	return ok && ae.Code == code
}

// ErrorReason extracts the machine-readable sub-reason from a typed error.
func ErrorReason(err error) string {
	ae, ok := err.(*api.Error)
	if !ok || len(ae.Extra) == 0 {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(ae.Extra, &m) != nil {
		return ""
	}
	return m["reason"]
}
