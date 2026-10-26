# Quickstart (macOS — one Mac as one machine: local aihub + pf + daemon + lane)

Everything below uses existing commands only — the macOS-native aihub
asset from the release, the same CLI verbs, and `tools/install.sh` (a
port conflict beware: aihub here keeps the standard port 8080 so the
installer's config matches; use another port only if 8080 is taken —
both sides, consistently). The terms this walkthrough uses (work item,
project, machine, workspace, daemon, lane) are defined on the
[concepts page](concepts.md).

**1. Download both native darwin assets from the current release** —
`aihub-darwin-arm64` (or `-amd64` on an Intel Mac) and `pf-darwin-*`:

```
VER=$(gh release view --repo silgrid/polyforge --json tagName -q .tagName)
gh release download "$VER" --repo silgrid/polyforge --pattern 'aihub-darwin-*' --pattern 'pf-darwin-*' \
  --dir /tmp/pf-quickstart/bin
chmod +x /tmp/pf-quickstart/bin/*
xattr -c /tmp/pf-quickstart/bin/*
codesign -f -s - /tmp/pf-quickstart/bin/*
mkdir -p /tmp/pf-quickstart/aihub-data
```

The `xattr -c` / `codesign -f -s -` pair clears the quarantine attribute and
ad-hoc signs each downloaded binary, so macOS does not kill it on first run
(`Killed: 9`).

**2. Start the local aihub and keep it alive.** The keep-alive is a HAND-RUN
tmux restart loop — you start it, and after a reboot you start it again
by hand; nothing in pf starts, restarts or supervises aihub:

```
tmux new-session -d -s aihub 'while :; do \
  /tmp/pf-quickstart/bin/aihub-darwin-arm64 --data-dir /tmp/pf-quickstart/aihub-data \
  --listen 127.0.0.1:8080 >> /tmp/pf-quickstart/aihub.log 2>&1; sleep 5; done'
```

On first start over an empty data dir, aihub seeds a bootstrap admin
(`usr_root`) and writes `<data-dir>/bootstrap-admin.key` (mode 0600; the
log names the file, never the key). Copy the key, then DELETE the file —
the key is not stored anywhere else:

```
cat /tmp/pf-quickstart/aihub-data/bootstrap-admin.key   # copy it, then delete the file
rm /tmp/pf-quickstart/aihub-data/bootstrap-admin.key
```

**3. Create the project and a machine identity** with the downloaded pf,
pointed at the local aihub with the bootstrap key (every connection entry
is also a per-command flag). The machine user's API key is the daemon's
credential; its plaintext is shown exactly once. The last command grants
the machine user the maintainer role on demo — aihub's first-heartbeat
machine registration admits only an admin or a project maintainer, so
without the grant the daemon's heartbeat can never register this machine:

```
PF=/tmp/pf-quickstart/bin/pf-darwin-arm64
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" projects create --name demo
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" users create --display-name smoke-machine --user-type machine
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" api-keys create --user <MACHINE_USER_ID>
$PF --server http://127.0.0.1:8080 --api-key "<BOOTSTRAP_KEY>" projects members add demo --user <MACHINE_USER_ID> --role maintainer
```

**4. Install pf the ordinary way, against your own local aihub** — `PF_KEY`
is the MACHINE key just created (read with echo off, so it never lands in
your shell history), the host argument `127.0.0.1` makes the config point at
this Mac's aihub (single-machine form):

```
read -rs PF_KEY
curl -s -H "Authorization: token $(gh auth token)" \
  https://raw.githubusercontent.com/silgrid/polyforge/main/tools/install.sh \
  | bash -s -- "$PF_KEY" 127.0.0.1
```

**5. Add the `[workspace]` mapping to your config** — the daemon refuses
to start without one (polyforge#113): the installer creates the config
at `~/.config/polyforge/cli.toml` but the mapping below is what makes
`pf daemon ensure` startable; the mapped directory must pre-exist:

```
mkdir -p "$HOME/polyforge-workspace"
cat >> ~/.config/polyforge/cli.toml <<EOF

[workspace."$HOME/polyforge-workspace"]
projects = ["demo"]
EOF
```

**6. Start the daemon** — the tmux session `polyforge-daemon` supervises
it (the same single supervision path as on Linux):

```
pf daemon ensure      # starts the daemon; tmux session polyforge-daemon supervises it
```

The daemon's first heartbeat registers this machine — because its key's
user is a maintainer of demo (the grant of step 3): machine registration
requires an admin or a project maintainer, and a registration failure
shows in `~/.local/share/polyforge/daemon.log`. Then continue with the
[from-zero smoke run](quickstart-developers.md) from step **5. File a minimal smoke work item** —
drop the `bin/` prefix (pf is installed) and the `--config` flags (your
config lives at `~/.config/polyforge/cli.toml`; same verbs). File →
submit → watch → wrap; `pf doctor` at any point.

