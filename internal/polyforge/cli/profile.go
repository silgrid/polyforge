package cli

// profile.go: the CLI's deterministic profile resolver. Every Cell-scoped
// command resolves its Cell here, first hit wins:
//
//	--profile (the global flag) → PF_PROFILE → the unique cwd match in the
//	Host's EFFECTIVE workspace index (GET /profiles) → default
//
// A follow-up on a work item (answer, watch --once) resolves like every
// other Cell-scoped command (the chat-subscription step that once came
// right after --profile is retired).
//
// No LLM, prompt or skill ever infers a profile, and the Host never guesses
// one from a cwd, a work item or a server: the CLI resolves, the Host
// routes. The cwd step reads the Host's effective index and nothing else
// (a file on disk never demotes or re-routes a command). When that index
// cannot be read, a business command fails closed (DAEMON-410 naming the
// index, nothing sent); only local verbs that run with the daemon down
// (pf config, pf profile) may match the cwd against the index built from
// the files on disk — that source is labelled "disk".

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// Profile sources, in resolution order.
const (
	ProfileSourceFlag    = "flag"
	ProfileSourceEnv     = "env"
	ProfileSourceCwd     = "cwd"
	ProfileSourceDisk    = "disk"
	ProfileSourceDefault = "default"
)

// profileEnv is the environment default of the resolver. The harness never
// sets it; a user may.
const profileEnv = "PF_PROFILE"

// Resolution is one resolved profile and where it came from.
type Resolution struct {
	Profile string `json:"profile"`
	Source  string `json:"source"`
}

// resolveProfile resolves the invocation's Cell. allowDisk lets a local
// verb fall back to the on-disk index when no Host answers; without it an
// unreadable effective index is the typed indexUnreadable refusal.
func (ctx *Context) resolveProfile(allowDisk bool) (Resolution, error) {
	if p := strings.TrimSpace(ctx.FlagProfile); p != "" {
		if err := cellcfg.ValidName(p); err != nil {
			return Resolution{}, err
		}
		return Resolution{p, ProfileSourceFlag}, nil
	}
	if p := strings.TrimSpace(os.Getenv(profileEnv)); p != "" {
		if err := cellcfg.ValidName(p); err != nil {
			return Resolution{}, err
		}
		return Resolution{p, ProfileSourceEnv}, nil
	}
	cwd, _ := os.Getwd()
	idx, err := ctx.hostIndex()
	if err == nil {
		if p, hit := cellcfg.MatchCwd(idx, cwd); hit {
			return Resolution{p, ProfileSourceCwd}, nil
		}
		return Resolution{cellcfg.DefaultProfile, ProfileSourceDefault}, nil
	}
	if !allowDisk {
		return Resolution{}, indexUnreadable(err)
	}
	if set, lerr := LoadHostSet(""); lerr == nil {
		if p, hit := cellcfg.MatchCwd(set.DiskIndex(), cwd); hit {
			return Resolution{p, ProfileSourceDisk}, nil
		}
	}
	return Resolution{cellcfg.DefaultProfile, ProfileSourceDefault}, nil
}

// indexUnreadable is a business command's refusal when the Host's
// effective workspace index cannot be read: the command's Cell is
// unknown, so nothing is sent anywhere (DAEMON-410, exit 1).
func indexUnreadable(cause error) error {
	return &api.Error{Code: api.ErrDaemonGone, Detail: fmt.Sprintf(
		"the Host's effective workspace index could not be read (GET /profiles: %v), so this command's Cell is unknown and nothing was sent — start or repair the daemon with `pf daemon ensure` and retry, or name the Cell with --profile; local commands (pf config, pf doctor, pf init, pf profile) work without it", cause)}
}

// hostIndex reads the Host's effective workspace index (an error: no Host
// answered it). A short bound: resolution must never hang on a wedged
// socket.
func (ctx *Context) hostIndex() ([]cellcfg.IndexEntry, error) {
	d := NewDaemonClient(ctx.hostSocket())
	d.HTTP.Timeout = 5 * time.Second
	resp, err := d.Profiles()
	if err != nil {
		return nil, err
	}
	return resp.Index, nil
}

// hostSocket answers the Host's socket for this invocation: --socket >
// PF_DAEMON_SOCKET > the Host file's daemon_socket > the default. A verb
// that loads no config (NoConfigLoad) still finds the Host this way.
func (ctx *Context) hostSocket() string {
	if ctx.FlagSocket != "" {
		return ctx.FlagSocket
	}
	if v := os.Getenv("PF_DAEMON_SOCKET"); v != "" {
		return v
	}
	if host, err := LoadFileConfig(""); err == nil && host.DaemonSocket != "" {
		return host.DaemonSocket
	}
	return ctx.Cfg.DaemonSocket
}

// routedCellMapping answers the [workspace] mapping the invocation's
// placement actions (file create, recall, remember) validate against:
// the mapping of the Cell this call routes to — the resolution above —
// not the Host file's (polyforge#17: --profile or the cwd match selects a
// named Cell, and a project mapped only there must place through it).
// The default Cell reads the Host file's mapping; a step or task agent
// reads it too (its Cell is the Host's own ownership record, never a CLI
// resolution — and its placement verbs are refused or go through the
// daemon anyway); a named Cell reads its own cli-<profile>.toml. file is
// the mapping file's base name, for messages. An unreadable effective
// index is the resolver's fail-closed refusal (the Cell is unknown, so
// nothing is sent anywhere); a profile no Cell is configured under — a
// name that is invalid or whose Cell file does not exist — is the Host's
// typed DAEMON-404 refusal (cellcfg.UnknownProfileDetail), still nothing
// sent; any other read or parse error of a present Cell file is a loud
// local error — placement never validates against a mapping it could
// not read.
func (ctx *Context) routedCellMapping() (plan *mapping.Plan, file string, err error) {
	if ctx.Step != "" || ctx.Task != "" {
		return ctx.Cfg.Mapping, cellcfg.HostFileName, nil
	}
	profile := ctx.cellProfile()
	if ctx.resolveErr != nil {
		return nil, "", ctx.resolveErr
	}
	if profile == "" || profile == cellcfg.DefaultProfile {
		return ctx.Cfg.Mapping, cellcfg.HostFileName, nil
	}
	hostPath := ctx.ConfigPath
	if hostPath == "" {
		hostPath = DefaultConfigPath()
	}
	dir := filepath.Dir(hostPath)
	// an invalid spelling is never a configured Cell (and never reaches a
	// path): the Host's registry has no such row, so its typed refusal is
	// the answer here too
	if verr := cellcfg.ValidName(profile); verr != nil {
		return nil, "", &api.Error{Code: api.ErrDaemonNotFound, Detail: cellcfg.UnknownProfileDetail(profile, dir)}
	}
	path := filepath.Join(dir, cellcfg.CellFileName(profile))
	cfg, err := LoadCellFile(ctx.Cfg, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", &api.Error{Code: api.ErrDaemonNotFound, Detail: cellcfg.UnknownProfileDetail(profile, dir)}
	}
	if err != nil {
		return nil, "", err
	}
	return cfg.Mapping, filepath.Base(path), nil
}
