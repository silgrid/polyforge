import "#test-env";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

// the translated READMEs follow the English README's update
// text (the release channel, not a re-run of the npm install) and carry no
// link to the upstream agegr/pi-web repository.
const TRANSLATIONS = ["README.zh-CN.md", "README.ja.md", "README.ru.md"];
const read = (name) => readFileSync(new URL(`./${name}`, import.meta.url), "utf8");

for (const name of TRANSLATIONS) {
  test(`${name} carries no agegr reference`, () => {
    assert.doesNotMatch(read(name), /agegr/i);
  });

  test(`${name} describes the release-channel update`, () => {
    const text = read(name);
    assert.match(text, /`bin\/pi-web-release-channel\.json`/, "names the shipped channel marker");
    assert.match(text, /`pi-web update --check`/, "explains the report-only check");
    assert.match(text, /SHA256SUMS/, "names the verified checksum of the internal line");
    // the shipped channel is the unified Polyforge release.
    assert.match(text, /`silgrid-pi-web-<tag>\.tgz`/, "names the unified release's Pi Web tarball");
  });

  test(`${name} uses the local screenshot`, () => {
    assert.match(read(name), /\]\(\.\/screenshot2\.png\)/);
  });

  // the sidebar worktree switcher is retired, so a translation
  // neither advertises it nor links its retired docs/worktrees*.md page.
  test(`${name} carries no retired worktree-switcher claim`, () => {
    const text = read(name);
    assert.doesNotMatch(text, /\*\*Git worktree\*\*/, "no worktree-switcher bullet");
    assert.doesNotMatch(text, /\]\(\.\/worktrees[^)]*\.md\)/, "no link to the retired worktrees page");
  });
}
