package driver

import (
	"context"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_* so no test can touch the real
// install, and makes the driver's real wait a no-op: a test that leaves
// Config.Sleep unset must not sleep a real retry backoff (minutes).
func TestMain(m *testing.M) {
	timerSleep = func(context.Context, time.Duration) {}
	testenv.Main(m)
}
