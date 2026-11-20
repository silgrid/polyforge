#!/bin/sh
# dist/workstation/build-seed.sh — assembles the workstation image's
# component seed (polyforge#162, 84-components-A). Runs INSIDE the
# Containerfile's seed stage (WORKDIR /seed), never on a host: it is
# the recipe's assembly half, and seedtool (built in the go stage) is
# the contract half — the digests, the component set and the manifest
# shape are defined once, in internal/polyforge/components, the same
# package the install transaction reads.
#
# usage (the Containerfile passes the injection points through):
#   build-seed.sh <version> <commit> <channel-repo> <pi-harness-version> <claude-harness-version> <targetarch>
#
# Everything installs at BUILD time: the workstation boots with no
# network dependency, no global npm package, nothing left to install.
set -eu

version="$1"
commit="$2"
channel_repo="$3"
pi_harness_version="$4"
claude_harness_version="$5"
if [ -n "${6:-}" ]; then
	targetarch="$6"
else
	targetarch="$(dpkg --print-architecture)"
fi
platform="linux/${targetarch}"
here="$(pwd)"

echo "== seed ${version}: platform ${platform}, channel ${channel_repo}, commit ${commit}"

# Pi Web: its production dependencies and its build, the same shape
# its own Dockerfile ships (no dev dependencies, no build cache in the
# seed). npm ci runs the package's own postinstall (patch-package and
# the terminal preparation), and node-pty's native binary is built for
# the target platform here — the manifest's digest covers all of it.
echo "== pi-web: npm ci + production build"
cd pi-web
NEXT_TELEMETRY_DISABLED=1 npm ci --no-audit --no-fund
# 产品版本同时写入包与 Next 的构建常量，页面重连后报告同一版本。
npm pkg set "version=${version#v}"
NEXT_PUBLIC_APP_VERSION="${version#v}" NEXT_TELEMETRY_DISABLED=1 npm run build
rm -rf .next/cache
npm prune --omit=dev
cd "$here"

# The two harnesses, their official packages pinned and installed
# as-published (the settled preinstall terms: no patching, no
# credentials; each user logs in themselves). They install into the
# versioned harness/ directory — a harness is versioned with the
# release, and its own package version is what the manifest records.
echo "== harnesses: pi @${pi_harness_version}, claude @${claude_harness_version}"
mkdir -p harness/pi
printf '{"name":"polyforge-harness-pi","private":true}\n' > harness/pi/package.json
npm install --prefix harness/pi --no-audit --no-fund "@earendil-works/pi-coding-agent@${pi_harness_version}"
mkdir -p harness/claude
printf '{"name":"polyforge-harness-claude","private":true}\n' > harness/claude/package.json
npm install --prefix harness/claude --no-audit --no-fund "@anthropic-ai/claude-code@${claude_harness_version}"

# The manifest: the digests, the component versions (the harness
# versions read from the installed packages' own package.json files),
# the provenance. seedtool refuses a seed that lacks one of the four
# component categories; its verify pass is the build's own check of
# the assembled copy.
echo "== the seed's manifest"
seedtool write --root "$here" \
	--version "${version}" \
	--commit "${commit}" \
	--platform "${platform}" \
	--channel-repo "${channel_repo}" \
	--harness-pi-package "harness/pi/node_modules/@earendil-works/pi-coding-agent" \
	--harness-claude-package "harness/claude/node_modules/@anthropic-ai/claude-code"
seedtool verify --root "$here"

echo "== seed ${version} complete at ${here}"
