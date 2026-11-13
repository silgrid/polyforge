# pf 2.20.21 `pf --json config list` recording

`list.json` is the verbatim stdout of a real `pf --json config list`, recorded
on 2026-11-03. Not hand-authored and not reformatted.
`lib/polyforge-config.test.mjs` parses it.

- Source: `polyforge` commit `18412ea6` — the head of the
  branch merged to main as PR #787 (summarize and `memory_policy` move into
  cli.toml; `config list` is their only read face).
- Build: `GOWORK=off go build -trimpath -ldflags '-X github.com/silgrid/polyforge/internal/version.Version=2.20.21' -o /tmp/pi-web-fixture-290/pf ./cmd/pf`
  (`pf --version` printed `2.20.21`).
- Recording: `env -u PF_WI_ID -u PF_DAEMON_ID -u PF_DAEMON_SOCKET -u PF_TOOL_WINDOW_MS HOME=/tmp/pi-web-fixture-290/home /tmp/pi-web-fixture-290/pf --json config list`,
  run from `/tmp` with a throwaway HOME; no live cli.toml or `.polyforge.yaml`
  was read or written, and no daemon was involved.
- Sample: `cli.toml` beside this file is byte-identical to the recorded
  `/tmp/pi-web-fixture-290/home/.config/polyforge/cli.toml` (its sha256 is
  `list.json`'s `file.sha256`, `8eaacffb…e526`). It maps two workspaces
  (`/tmp/pi-web-fixture/ws-alpha` → `alpha`, `gamma`; `/tmp/pi-web-fixture/ws-beta`
  → `beta`), declares one `[summarize]` table and `[memory_policy."<path>".<entry>]`
  blocks for both paths.

What the recording shows, kept as facts:

- The three `summarize.*` items carry value, `source: "file"`, `class: "live"`
  and their schemas (`model-list`, `string`, `int` 10–3600).
- `memory_policy` is a top-level object keyed by workspace path; per path the
  `default` entry carries its effective lists (ws-beta's undeclared `record`
  is the built-in default's) and every mapped project its OWN lists, never
  merged with `default` (`beta`, undeclared, is `declared: false` with empty
  lists). pf emits the entries in alphabetical order (`alpha` before `default`).
