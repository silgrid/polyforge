# Releasing Pi Web

Pi Web is developed in the Polyforge repository's `pi-web/` directory and is
released with Polyforge: one git tag, one release entry. There is no separate
Pi Web release procedure, workflow or version line.

## The one release entry

`.polyforge/release.sh` at the repository root cuts every artifact of a tag
(the full procedure, its gates and `--verify` are documented in the
repository's `docs/reference/release.md`). For Pi Web it:

1. exports the released commit's `pi-web/` tree to its staging directory (the
   worktree is never touched);
2. runs `npm ci`, then `npm run build` with `PI_WEB_RELEASE_VERSION=<tag>`, so
   the bundle reports the release it belongs to (`next.config.ts` strips the
   leading `v`). This production build is the documented exception to the
   "never run `next build` during development" rule;
3. stamps the tag's version into the exported package metadata
   (`npm version <version> --no-git-tag-version`) after the build and before
   pack, so `pi-web/package.json` on `main` keeps its development value and no
   release ever commits a version bump;
4. packs the package (`npm pack`) as `silgrid-pi-web-v<version>.tgz`, which the
   release publishes next to `pf`, the AIHub image digest, `SHA256SUMS` and
   `release-manifest.json`.

The tag is the only version source: `pf --version`, `pf aihub --version`, the
Pi Web tarball and the release manifest all carry it. The unified line starts at
`v0.2.0` (the one named cutover from the repository's retired `v2.20.x`
line); the former independent `0.1.x` Pi Web line has no successor.

## Tag pushes

The release entry pushes exactly the one release tag it cut — never `--tags`
and never `main`. When a tag is pushed by hand, push it by name:

```bash
git push origin v<version>
```

GitHub creates no `push` events for tags when more than three tags are pushed
at once, and a clone can hold unrelated local tags that a multi-tag push would
publish (measured 2026-10-23, v0.0.35: a `--tags` push shipped 58 tags at
once).

## Installing a release

- `pi-web update` updates a global install from the release channel named in
  `bin/pi-web-release-channel.json`: the Polyforge repository's GitHub
  Releases (the tarball and `SHA256SUMS` are downloaded, the checksum is
  verified, then `npm install -g` installs the tarball).
- `tools/install.sh --with-pi-web` installs Pi Web from the same release as
  `pf`, then runs the install check the package ships, which confirms the
  install scripts' effects even when npm skipped the scripts (e.g. npm 11
  under `ignore-scripts` or an explicit `allowScripts` denial) and otherwise exits non-zero naming the step. Pi
  Web ships only as part of the GitHub Release, so this is the install path;
  it is not installed from the npm registry. The check can be run again by
  hand: `node "$(npm root -g)/@silgrid/pi-web/bin/pi-web-install-check.js"`.
- `deploy-piweb.sh` installs a release for the container-autostart service
  (`PIWEB_INSTALL_REPO`, default `silgrid/polyforge`; `PIWEB_INSTALL_VERSION`
  pins one tag, else the latest release); the repository's
  `.polyforge/deploy.sh <version> --confirm <version> --component pi-web`
  drives it.

Nothing in this repository publishes to the npm registry.
