import "#test-env";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { getPiWebReleaseUrl, isNewerStableVersion } = await jiti.import("./app-update.ts");

test("detects newer stable Pi Web versions", () => {
  assert.equal(isNewerStableVersion("0.8.8", "0.8.7"), true);
  assert.equal(isNewerStableVersion("0.9.0", "0.8.7"), true);
  assert.equal(isNewerStableVersion("1.0.0", "0.9.9"), true);
});

test("does not report equal, older, or unsupported versions as updates", () => {
  assert.equal(isNewerStableVersion("0.8.7", "0.8.7"), false);
  assert.equal(isNewerStableVersion("0.8.6", "0.8.7"), false);
  assert.equal(isNewerStableVersion("0.8.8-beta.1", "0.8.7"), false);
  assert.equal(isNewerStableVersion("invalid", "0.8.7"), false);
});

test("builds a release-notes URL only for stable versions", () => {
  assert.equal(
    getPiWebReleaseUrl("0.8.8"),
    "https://github.com/silgrid/pi-web/releases/tag/v0.8.8",
  );
  assert.equal(getPiWebReleaseUrl("0.8.8-beta.1"), null);
});


// ---- the workstation image refuses the self-update (polyforge#177) ----

test("inside the image the route answers like SKIP_VERSION_CHECK — no request, no update", async () => {
  const routeJiti = createJiti(import.meta.url, { alias: { "@": process.cwd() }, interopDefault: true });
  const { GET } = await routeJiti.import("../app/api/app-update/route.ts");
  const seed = mkdtempSync(join(tmpdir(), "pf-app-update-seed-"));
  mkdirSync(join(seed, "releases", "v0.2.4-dev.1"), { recursive: true });
  writeFileSync(join(seed, "releases", "v0.2.4-dev.1", "manifest.json"), "{}\n");
  const previousSeam = globalThis.__piWebTestSeedRoot;
  const realFetch = globalThis.fetch;
  let fetchCalls = 0;
  globalThis.__piWebTestSeedRoot = seed;
  globalThis.fetch = async () => {
    fetchCalls += 1;
    throw new Error("no request may leave inside the workstation image");
  };
  try {
    const response = await GET();
    assert.equal(response.status, 200, "the read degrades, never 5xx");
    assert.deepEqual(await response.json(), {
      currentVersion: process.env.NEXT_PUBLIC_APP_VERSION ?? "0.0.0",
      latestVersion: process.env.NEXT_PUBLIC_APP_VERSION ?? "0.0.0",
      updateAvailable: false,
      releaseUrl: "",
      imageManaged: true,
    }, "the image mode is the SKIP_VERSION_CHECK shape plus the fact");
    assert.equal(fetchCalls, 0, "the registry was never asked");
  } finally {
    globalThis.__piWebTestSeedRoot = previousSeam;
    globalThis.fetch = realFetch;
    rmSync(seed, { recursive: true, force: true });
  }
});

test("the route's gate sits before any version read, and the response type carries the fact", async () => {
  const source = await readFile(new URL("../app/api/app-update/route.ts", import.meta.url), "utf8");
  const types = await readFile(new URL("./api-types.ts", import.meta.url), "utf8");
  assert.match(
    source,
    /if \(isImageManaged\(\)\) \{[\s\S]*?imageManaged: true[\s\S]*?\}/,
    "the image gate answers the fact",
  );
  assert.ok(
    source.indexOf("isImageManaged()") < source.indexOf("loadUpdateStatus()"),
    "the gate precedes every version read — nothing is fetched inside the image",
  );
  assert.match(
    types,
    /export interface AppUpdateResponse \{[\s\S]*?imageManaged\?: boolean/,
    "AppUpdateResponse carries the optional fact",
  );
});
