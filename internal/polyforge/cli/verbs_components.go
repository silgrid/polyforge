package cli

// verbs_components.go (polyforge#190): the workstation-only component
// upgrade command. The image is identified by its seed manifest; host
// installs remain owned by tools/install.sh.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/components"
)

var workstationBasePf = "/usr/local/libexec/polyforge/pf"

const componentsUsage = `pf components check|status|upgrade|rollback|cancel

In the workstation image, upgrade pf (including daemon and aihub), lanes,
skills and Pi Web on the persistent components volume. The image base,
node, python, git, podman, supervise and harnesses use a new image.`

var cmdComponents = &Command{
	Name: "components", Group: GroupProcess, Summary: "Upgrade components inside the workstation image", Usage: componentsUsage,
	Setup: func(fs *flag.FlagSet) {
		fs.String("version", "", "release version")
		fs.Bool("force", false, "upgrade while work is running")
		fs.String("seed-dir", "", "local complete version directory")
	},
	Run: runComponents,
}

func componentsRootError() error {
	return errors.New("pf components: this is not a workstation image (no seed manifest); use tools/install.sh for host upgrades")
}

func componentsBaseVersion() (string, error) {
	out, err := exec.Command(workstationBasePf, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("read workstation base version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func componentsDataDir() string {
	home, _ := os.UserHomeDir()
	return DataDir(home)
}

func componentsFaceFor(ctx *Context) *componentsDaemonFace {
	return &componentsDaemonFace{d: NewDaemonClient(ctx.Cfg.DaemonSocket)}
}

type componentsDaemonFace struct{ d *DaemonClient }

func (f *componentsDaemonFace) Drain(ctx context.Context, force bool) ([]string, error) {
	raw, err := f.d.doCtx(ctx, "POST", "/ops/drain", "", DrainRequest{Upgrade: true, Force: force}, nil)
	if err != nil {
		var wire *api.Error
		if errors.As(err, &wire) && wire.Code == api.ErrDaemonConflict {
			return busyCellsOf(wire.Detail), nil
		}
		return nil, err
	}
	var answer DrainResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("decode drain response: %w", err)
	}
	if answer.Detail != "" {
		return busyCellsOf(answer.Detail), nil
	}
	return nil, nil
}

func (f *componentsDaemonFace) Restart(ctx context.Context) error {
	_, err := f.d.doCtx(ctx, "POST", "/ops/restart", "", map[string]any{}, nil)
	return err
}

func busyCellsOf(detail string) []string {
	const prefix = "Cell(s) "
	start := strings.Index(detail, prefix)
	if start < 0 {
		return nil
	}
	text := detail[start+len(prefix):]
	if end := strings.Index(text, " still hold work"); end >= 0 {
		text = text[:end]
	}
	var cells []string
	for _, cell := range strings.Split(text, ",") {
		if cell = strings.TrimSpace(cell); cell != "" {
			cells = append(cells, cell)
		}
	}
	return cells
}

func componentManifest(root, target string) (*components.Manifest, error) {
	return components.ReadManifest(filepath.Join(root, target))
}

func componentSummary(root string) (map[string]any, error) {
	current, err := components.CurrentTarget(root)
	if err != nil {
		return nil, err
	}
	cur, err := componentManifest(root, current)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"current": cur.Version, "previous": nil, "status": nil}
	if prev, err := os.Readlink(filepath.Join(root, components.PreviousLinkname)); err == nil {
		if m, e := componentManifest(root, prev); e == nil {
			result["previous"] = m.Version
		}
	}
	status, err := components.ReadUpgradeStatus(root)
	if err != nil {
		return nil, err
	}
	if status != nil {
		result["status"] = status
	}
	return result, nil
}

func emitComponents(ctx *Context, value any, text string) error {
	if ctx.JSON {
		return json.NewEncoder(ctx.Stdout).Encode(value)
	}
	_, err := io.WriteString(ctx.Stdout, text+"\n")
	return err
}

func runComponents(ctx *Context, fs *flag.FlagSet, args []string) error {
	if !workstationImage() {
		return componentsRootError()
	}
	if len(args) != 1 {
		return errors.New("pf components: expected one of check, status, upgrade, rollback or cancel")
	}
	sub := args[0]
	root := componentsVolumeRoot
	if (sub == "upgrade" || sub == "rollback") && boolFlag(fs, "force") {
		status, err := components.ReadUpgradeStatus(root)
		if err != nil {
			return err
		}
		if status != nil && status.State == components.UpgradeStateWaiting {
			if status.Operation != sub {
				return fmt.Errorf("a %s is waiting; force that operation instead", status.Operation)
			}
			if err := components.RequestUpgradeForce(root); err != nil {
				return err
			}
			return emitComponents(ctx, status, "已请求正在等待的事务强制继续")
		}
	}
	base, err := componentsBaseVersion()
	if err != nil && sub != "check" && sub != "status" && sub != "cancel" {
		return err
	}
	switch sub {
	case "check":
		current, err := components.CurrentTarget(root)
		if err != nil {
			return err
		}
		manifest, err := componentManifest(root, current)
		if err != nil {
			return err
		}
		latest, err := components.LatestRelease(context.Background(), manifest.ChannelRepo)
		if err != nil {
			return err
		}
		value := map[string]any{"current": manifest.Version, "latest": latest.Tag, "channelRepo": manifest.ChannelRepo, "upgradable": manifest.Version != latest.Tag}
		return emitComponents(ctx, value, fmt.Sprintf("current %s; latest %s; upgradable %t", manifest.Version, latest.Tag, manifest.Version != latest.Tag))
	case "status":
		value, err := componentSummary(root)
		if err != nil {
			return err
		}
		return emitComponents(ctx, value, fmt.Sprintf("current %v; previous %v; status %v", value["current"], value["previous"], value["status"]))
	case "cancel":
		if err := components.RequestUpgradeCancel(root); err != nil {
			return err
		}
		return emitComponents(ctx, map[string]string{"state": components.UpgradeStateCancelled}, "已请求取消，等待编排重启 daemon")
	case "upgrade":
		if seed := stringFlag(fs, "seed-dir"); seed != "" && stringFlag(fs, "version") != "" {
			manifest, err := components.ReadManifest(seed)
			if err != nil {
				return err
			}
			if manifest.Version != stringFlag(fs, "version") {
				return fmt.Errorf("requested version %s does not match seed version %s", stringFlag(fs, "version"), manifest.Version)
			}
		}
		result, err := components.Upgrade(context.Background(), components.UpgradeOptions{Root: root, DataDir: componentsDataDir(), BaseVersion: base, Platform: runtime.GOOS + "/" + runtime.GOARCH, Version: stringFlag(fs, "version"), Force: boolFlag(fs, "force"), SeedDir: stringFlag(fs, "seed-dir"), Face: componentsFaceFor(ctx)})
		if err != nil {
			return err
		}
		return emitComponents(ctx, result, fmt.Sprintf("upgraded %s to %s", result.From, result.To))
	case "rollback":
		result, err := components.Rollback(context.Background(), components.RollbackOptions{Root: root, BaseVersion: base, Force: boolFlag(fs, "force"), Face: componentsFaceFor(ctx)})
		if err != nil {
			return err
		}
		return emitComponents(ctx, result, fmt.Sprintf("rolled back %s to %s", result.From, result.To))
	default:
		return fmt.Errorf("pf components: unknown subcommand %q", sub)
	}
}
