import "#test-env";
import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createJiti } from "jiti";

const { execFileAsync, runGit } = await createJiti(import.meta.url, {
  interopDefault: true,
  moduleCache: false,
}).import("./exec-file.ts");

test("runGit runs `git -C <cwd>` and can trim stdout", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "pi-web-exec-file-"));
  t.after(async () => {
    await rm(dir, { recursive: true, force: true });
  });
  await execFileAsync("git", ["init", dir]);
  await writeFile(join(dir, "a.txt"), "a\n");
  const raw = await runGit(dir, ["status", "--porcelain"]);
  assert.match(raw, /\n$/);
  const trimmed = await runGit(dir, ["status", "--porcelain"], { trim: true });
  assert.equal(trimmed, "?? a.txt");
  assert.equal(raw.trim(), trimmed);
});

test("runGit failures reject with the underlying error", async () => {
  await assert.rejects(runGit("/definitely-not-a-repo", ["rev-parse", "--show-toplevel"]));
});

test("execFileAsync is the promisified execFile", async () => {
  const { stdout } = await execFileAsync(process.execPath, ["--version"]);
  assert.match(stdout, /v\d+\.\d+\.\d+/);
});
