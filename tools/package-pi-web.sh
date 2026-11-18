#!/bin/sh
# package-pi-web.sh — the ONE Pi Web packaging path, shared by the formal
# release (.polyforge/release.sh) and the installer's development channel
# (tools/install.sh --dev --with-pi-web, polyforge#136): only one
# implementation, so the two channels cannot drift.
#
# Usage (from the repository root, whose history carries <commit>):
#   tools/package-pi-web.sh <commit> <tag> <out-dir>
#
# <commit>'s pi-web/ tree is exported to a staging directory (the
# documented exception to pi-web's "never next build" development rule;
# pi-web/package.json keeps its development value and no bump commit
# exists), npm ci installs the locked dependencies, the package builds
# with PI_WEB_RELEASE_VERSION=<tag> (the release path's build), the
# package version is stamped to <tag> without a git tag
# (npm version <tag without v> --no-git-tag-version) and npm pack packs
# silgrid-pi-web-<tag>.tgz into <out-dir>, whose packed package.json must
# carry <tag without v> — a pack that lost the version refuses.
#
# This script publishes NOTHING — the caller owns its channel: the
# release publishes the tarball with the release's assets, the dev
# channel installs it locally. It writes one tarball into <out-dir> and
# touches nothing else.
set -eu

[ $# -eq 3 ] || { echo "usage: tools/package-pi-web.sh <commit> <tag> <out-dir>" >&2; exit 2; }
COMMIT=$1
TAG=$2
OUT=$3
[ -d "$OUT" ] || { echo "package-pi-web.sh: the out dir $OUT does not exist" >&2; exit 2; }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/package-pi-web.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM

mkdir "$WORK/piweb"
git archive "$COMMIT" pi-web | tar -x -C "$WORK/piweb" || {
  echo "package-pi-web.sh: exporting pi-web/ of $COMMIT failed" >&2
  exit 1
}
( cd "$WORK/piweb/pi-web" \
  && npm ci --no-audit --no-fund \
  && PI_WEB_RELEASE_VERSION="$TAG" npm run build \
  && npm version "${TAG#v}" --no-git-tag-version \
  && mkdir -p "$WORK/pack" \
  && npm pack --pack-destination "$WORK/pack" ) || {
  echo "package-pi-web.sh: building or packing the pi-web package failed" >&2
  exit 1
}
set -- "$WORK/pack"/*.tgz
if [ $# -ne 1 ] || [ ! -f "$1" ]; then
  echo "package-pi-web.sh: npm pack did not produce exactly one tarball" >&2
  exit 1
fi
mv "$1" "$OUT/silgrid-pi-web-$TAG.tgz"
PV=$(tar -xzOf "$OUT/silgrid-pi-web-$TAG.tgz" package/package.json 2>/dev/null | sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
if [ "$PV" != "${TAG#v}" ]; then
  echo "package-pi-web.sh: the packed pi-web package does not carry version ${TAG#v} (it says \"${PV:-<unreadable>}\")" >&2
  exit 1
fi
