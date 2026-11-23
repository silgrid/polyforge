package runtime

// The capability probe's real-harness proof (polyforge#227 AC1): a REAL pi
// process, running against the daemon-owned resources, loads the built-in
// subagent and guard extensions, offers the subagent tool to the model, and
// executes one controlled call whose result returns to the model — all
// against a loopback fake provider with a fake key over a temporary
// models.json, so no real credential is read and no paid model is called.
//
// The negative classes reuse the SAME pi version: a test-only loading
// adapter replaces the embedded subagent entry point in the probe's resource
// view. Hiding registerTool makes the extension fail to load (the API class);
// swallowing the registration lets it load but offers no subagent tool (the
// tool-registration class). The production extension is never edited.

import (
	"context"
	"io/fs"
	"os/exec"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// compatResourceMap copies the embedded resource tree into an in-memory
// twin the probe will load instead.
func compatResourceMap(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	err := fs.WalkDir(stepagentassets.FS, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		raw, err := stepagentassets.FS.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = &fstest.MapFile{Data: raw}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// compatLoadingAdapter replaces the subagent extension's entry point with a
// wrapper that proxies the extension API: the named member is either removed
// (an extension-loading failure, the API class) or replaced by a no-op (the
// extension loads and registers nothing, the tool-registration class).
func compatLoadingAdapter(t *testing.T, fsys fstest.MapFS, mode string) fstest.MapFS {
	t.Helper()
	entry := "extensions/subagent/index.ts"
	raw, ok := fsys[entry]
	if !ok {
		t.Fatal("the embedded tree carries no subagent entry point")
	}
	delete(fsys, entry)
	fsys["extensions/subagent/real.ts"] = raw
	body := `import real from "./real.ts";
export default function (pi: any) {
  const proxy: any = new Proxy(pi, {
    get(target: any, prop: string | symbol) {
      if (prop === "registerTool") {
`
	if mode == "hide" {
		body += `        return undefined;
`
	} else {
		body += `        return () => {};
`
	}
	body += `      }
      const value = target[prop];
      return typeof value === "function" ? value.bind(target) : value;
    },
  });
  return real(proxy);
}
`
	fsys[entry] = &fstest.MapFile{Data: []byte(body)}
	return fsys
}

func TestPiCompatibilityRealPi(t *testing.T) {
	bin := "pi"
	if _, err := exec.LookPath(bin); err != nil {
		t.Fatalf("the capability proof needs the real pi on PATH (the verification image pins it): %v", err)
	}
	healthy := compatResourceMap(t)
	t.Run("healthy", func(t *testing.T) {
		old := piCompatResourceFS
		piCompatResourceFS = healthy
		defer func() { piCompatResourceFS = old }()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := validatePiCompatibility(ctx, bin); err != nil {
			t.Fatalf("a healthy pi 1.4.0 tree must pass the capability probe: %v", err)
		}
	})
	t.Run("api-defect", func(t *testing.T) {
		old := piCompatResourceFS
		piCompatResourceFS = compatLoadingAdapter(t, compatResourceMap(t), "hide")
		defer func() { piCompatResourceFS = old }()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := validatePiCompatibility(ctx, bin)
		if err == nil {
			t.Fatal("a pi whose extension API cannot load the built-in subagent tool must be refused")
		}
		if got := compatReason(err); got != "pi_api" {
			t.Fatalf("reason = %q, want pi_api (%v)", got, err)
		}
		for _, want := range []string{"pi compatibility rejected [pi_api]", "extension API probe failed"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
	t.Run("tool-registration-defect", func(t *testing.T) {
		old := piCompatResourceFS
		piCompatResourceFS = compatLoadingAdapter(t, compatResourceMap(t), "swallow")
		defer func() { piCompatResourceFS = old }()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := validatePiCompatibility(ctx, bin)
		if err == nil {
			t.Fatal("a pi that loads the extension but never registers the subagent tool must be refused")
		}
		if got := compatReason(err); got != "pi_tool_registration" {
			t.Fatalf("reason = %q, want pi_tool_registration (%v)", got, err)
		}
		for _, want := range []string{"pi compatibility rejected [pi_tool_registration]", "subagent tool is unavailable"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q lacks %q", err, want)
			}
		}
	})
}
