# Tracking the upstream (agegr/pi-web)

Pi Web is a fork of [agegr/pi-web](https://github.com/agegr/pi-web), its
direct upstream. Since Pi Web lives in the Polyforge repository's `pi-web/`
directory, upstream releases are taken by a documented **prefix-diff
procedure**: no new tool, no `git subtree`, and no upstream commit, tag or
merge parent is ever imported into this repository's history.

## The recorded baseline

`pi-web/PROVENANCE.md` records the upstream baseline the subtree carries —
today `agegr/pi-web` `v0.14.2`, commit `025e24e` — and the README's
"Fork baseline" table maps every fork version to the upstream version and
commit it was built on. Both always name the same latest baseline.

## Taking one upstream release

One commit per upstream release, in order (never skip a release; take
`v0.14.3` before `v0.15.0`):

1. Start from the recorded baseline. Fetch the upstream release's objects
   into this repository without keeping a ref or a parent (only
   `FETCH_HEAD` names them; nothing enters this repository's history):

   ```bash
   git fetch --no-tags https://github.com/agegr/pi-web.git refs/tags/<release-tag>
   git rev-parse --verify <baseline>^{commit}   # PROVENANCE.md's upstream commit, today 025e24e
   ```

2. Produce the upstream release's diff against that baseline, rewritten to
   the `pi-web/` prefix:

   ```bash
   git diff --binary <baseline> FETCH_HEAD \
     --src-prefix=a/pi-web/ --dst-prefix=b/pi-web/ > /tmp/upstream-<release-tag>.diff
   ```

3. Apply it on a work branch, as a three-way merge (the fetched objects make
   the baseline blobs available) so the recorded adaptations below are kept
   and conflicts surface in the files they touch:

   ```bash
   git apply --3way --index /tmp/upstream-<release-tag>.diff
   ```

   Resolve every conflict in favour of the adaptation unless the upstream
   change supersedes it; nothing outside `pi-web/` may change.

4. In the same commit, update `pi-web/PROVENANCE.md` (the upstream table:
   the new version and commit) and add the release's row to the README's
   "Fork baseline" table. The commit message names the upstream release and
   commit, for example `pi-web: take agegr/pi-web v0.14.3 (<commit>) by prefix
   diff`.

5. Run the full bar (a `pi-web/` change always runs it: `npm ci`, lint,
   `tsc --noEmit`, `npm test`, `npm run test:e2e:all`) and deliver the
   commit like any other change.

A reproduction check for a reviewer: in a scratch tree, apply the same
diff to the previous commit's `pi-web/` tree and compare — the commit's
`pi-web/` changes must be exactly the upstream diff plus the conflict
resolutions the commit message lists.

## The recorded adaptations

What this repository changes relative to the upstream, beyond the fork's
own features, so a prefix diff that touches these files keeps them:

- `package.json`: the `@silgrid/pi-web` identity, the development version
  (`0.2.0-dev.0`; the release tag is the only released version source), no
  `release` script, the repository/homepage/bugs fields of the Polyforge
  repository (directory `pi-web`), and the exact-pinned pi packages.
- `bin/pi-web-release-channel.json`: the release channel is the Polyforge
  repository's GitHub Releases.
- `deploy-piweb.sh`: the release channel check verifies only the tarball's
  own `SHA256SUMS` line (one release carries every artifact of the tag).
- No release or CI workflow and no lane or sync script under `pi-web/`:
  releases come from the repository root's `.polyforge/release.sh`, CI from
  the root `.github/workflows/pi-web-ci.yml`; an upstream workflow file
  (`.github/workflows/*`) is not taken.
- The e2e suites' `// carries:` headers stay rooted at `pi-web/`, and
  `polyforge-workflow-216.mjs` builds this repository's own `pf`.
