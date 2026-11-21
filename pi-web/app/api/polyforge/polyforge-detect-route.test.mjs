// carries: app/api/polyforge/detect/*, lib/polyforge-detect.ts
import "#test-env";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createJiti } from "jiti";

// polyforge#177: the Settings detection read answers the image-managed fact
// at the top level (owner Q2: status, detect and the install GET are the
// three read interfaces, one shared judgment). The route degrades on its
// own with no pf on PATH, so the behavioral drive needs no stub binary —
// the pf-absent shape is the honest one here.

const jiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
const { GET } = await jiti.import("./detect/route.ts");
const source = await readFile(new URL("./detect/route.ts", import.meta.url), "utf8");

const get = (host = "127.0.0.1:30141") => new Request(`http://${host}/api/polyforge/detect?`, { headers: { host } });

test("the detect read answers imageManaged beside its own fields, in both the host and the image mode", async () => {
  // the host (no seed root): the fact is false; the detection's own fields
  // are detectPolyforge's business (its own tests pin them)
  const previous = globalThis.__piWebTestSeedRoot;
  globalThis.__piWebTestSeedRoot = join(tmpdir(), "pf-detect-route-no-seed");
  try {
    const host = await GET(get());
    assert.equal(host.status, 200);
    assert.equal((await host.json()).imageManaged, false, "a host answers the image fact as false");
  } finally {
    globalThis.__piWebTestSeedRoot = previous;
  }

  // the image mode: the top-level fact is true
  const seed = mkdtempSync(join(tmpdir(), "pf-detect-route-seed-"));
  mkdirSync(join(seed, "releases", "v0.2.4-dev.1"), { recursive: true });
  writeFileSync(join(seed, "releases", "v0.2.4-dev.1", "manifest.json"), "{}\n");
  globalThis.__piWebTestSeedRoot = seed;
  try {
    const image = await GET(get());
    assert.equal(image.status, 200);
    assert.equal((await image.json()).imageManaged, true, "the image mode answers the fact at the top level");
  } finally {
    globalThis.__piWebTestSeedRoot = previous;
    rmSync(seed, { recursive: true, force: true });
  }
});

test("the route's catch fallback carries the image fact too — no branch may answer a bare-machine guess", () => {
  assert.match(
    source,
    /return NextResponse\.json\(\{\s*\.\.\.\(await detectPolyforge\(force\)\),\s*imageManaged: isImageManaged\(\)\s*\}\);/,
    "the success branch assembles the fact beside the detection",
  );
  assert.match(
    source,
    /imageManaged: isImageManaged\(\),\s*pfPresent: false,/,
    "the catch fallback names the fact as well",
  );
});
