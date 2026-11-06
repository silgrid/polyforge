package core

// w446_exclusive_create_test.go ( AC2, AC3): an exclusive
// create is refused while another work item of the same project on the
// same bundle is unfinished — atomically, inside the create transaction,
// so two creators racing never both commit — and writes nothing when
// refused; a non-exclusive create, another project and another bundle
// are unaffected, and a finished (wrapped, failed or canceled) work item
// no longer counts.

import (
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func w446Exclusive(env *testEnv, project, bundle string) (*WIView, error) {
	req := CreateRequest{Project: project, Goal: "batch", Priority: api.PriorityHigh,
		Labels: []string{"integration"}, Reporter: env.UserID, Exclusive: true}
	if bundle != "" {
		req.BundleName = strPtrTest(bundle)
	}
	return env.WIs.Create(req)
}

func w446Count(t *testing.T, env *testEnv) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM work_items`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestW446ExclusiveCreate(t *testing.T) {
	env, sm := sessionEnv(t)
	for _, name := range []string{"p", "q"} {
		if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: name}); err != nil {
			t.Fatalf("create project %s: %v", name, err)
		}
	}

	first, err := w446Exclusive(env, "p", "integrate")
	if err != nil {
		t.Fatalf("the first exclusive create succeeds: %v", err)
	}
	before := w446Count(t, env)
	_, err = w446Exclusive(env, "p", "integrate")
	if !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "OPEN_ON_BUNDLE" {
		t.Fatalf("a second exclusive create is refused CORE-409 OPEN_ON_BUNDLE: %v", err)
	}
	if d := err.(*api.Error).Detail; !strings.Contains(d, first.Slug) || !strings.Contains(d, api.StatusQueued) {
		t.Fatalf("the refusal names the open work item %s and its status: %q", first.Slug, d)
	}
	if got := w446Count(t, env); got != before {
		t.Fatalf("a refused create writes nothing: %d rows, want %d", got, before)
	}

	if _, err := env.WIs.Create(CreateRequest{Project: "p", Goal: "fix", Reporter: env.UserID,
		BundleName: strPtrTest("integrate")}); err != nil {
		t.Fatalf("a non-exclusive create on the same bundle still succeeds: %v", err)
	}
	if _, err := w446Exclusive(env, "q", "integrate"); err != nil {
		t.Fatalf("another project is unaffected: %v", err)
	}
	if _, err := w446Exclusive(env, "p", "develop"); err != nil {
		t.Fatalf("another bundle is unaffected: %v", err)
	}
	if _, err := w446Exclusive(env, "p", ""); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("exclusive without a bundle is CORE-400: %v", err)
	}

	// The non-exclusive fix ticket above is also unfinished on the bundle:
	// finish it so only the batch under test holds the rule.
	open, err := env.WIs.List(ListFilters{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, wi := range open {
		if wi.ID != first.ID && wi.BundleName != nil && *wi.BundleName == "integrate" {
			if _, err := env.WIs.Cancel(wi.ID, env.UserID, api.CancelRequest{}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// canceled, wrapped and failed each release the rule.
	if _, err := env.WIs.Cancel(first.ID, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	second, err := w446Exclusive(env, "p", "integrate")
	if err != nil {
		t.Fatalf("once the open one is canceled the next exclusive create succeeds: %v", err)
	}
	if _, err := w446Exclusive(env, "p", "integrate"); ErrorReason(err) != "OPEN_ON_BUNDLE" {
		t.Fatalf("the new one holds the rule again: %v", err)
	}
	for i, end := range []string{api.StatusWrapped, api.StatusFailed} {
		secret := strings.Repeat(string(rune('a'+i)), 32)
		mustSessionClaim(t, sm, env, second.ID, secret)
		if _, err := w446Exclusive(env, "p", "integrate"); ErrorReason(err) != "OPEN_ON_BUNDLE" {
			t.Fatalf("a running work item holds the rule: %v", err)
		}
		if err := sm.End(second.ID, api.SessionEndRequest{SessionID: secret, Status: end}, env.UserID); err != nil {
			t.Fatalf("end %s: %v", end, err)
		}
		next, err := w446Exclusive(env, "p", "integrate")
		if err != nil {
			t.Fatalf("once the open one is %s the next exclusive create succeeds: %v", end, err)
		}
		second = next
	}

	// Racing creators: exactly one commits.
	if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: "r"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, refused := 0, 0
	var other []error
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := w446Exclusive(env, "r", "integrate")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case ErrorReason(err) == "OPEN_ON_BUNDLE":
				refused++
			default:
				other = append(other, err)
			}
		}()
	}
	wg.Wait()
	if won != 1 || refused != 7 || len(other) != 0 {
		t.Fatalf("8 racing exclusive creates: %d won, %d refused, other %v; want exactly one", won, refused, other)
	}
}
