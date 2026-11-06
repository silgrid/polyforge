import "#test-env";
import assert from "node:assert/strict";
import test from "node:test";
import { createJiti } from "jiti";

const jiti = createJiti(import.meta.url);
const { getPackageSource, isDisabledPackage } = await jiti.import("./package-settings.ts");

test("getPackageSource reads the source off a string or object entry", () => {
  assert.equal(getPackageSource("/clone/release/current"), "/clone/release/current");
  assert.equal(getPackageSource({ source: "npm:@scope/pkg", autoload: true }), "npm:@scope/pkg");
  assert.equal(
    getPackageSource({ source: "/clone", extensions: [], skills: ["./skills/polyforge"] }),
    "/clone",
  );
});

test("a plain string entry is never disabled", () => {
  assert.equal(isDisabledPackage("/clone/release/current"), false);
});

test("disabled = the object entry whose four resource arrays are all empty", () => {
  assert.equal(isDisabledPackage({ source: "/clone", extensions: [], skills: [], prompts: [], themes: [] }), true);
  assert.equal(isDisabledPackage({ source: "/clone", extensions: [], skills: ["./skills/polyforge"], prompts: [], themes: [] }), false);
  assert.equal(isDisabledPackage({ source: "/clone", extensions: ["./ext"], skills: [], prompts: [], themes: [] }), false);
  // An object entry carrying none of the four arrays is not the disabled
  // shape (the SDK reports every object entry as filtered).
  assert.equal(isDisabledPackage({ source: "/clone", autoload: false }), false);
});

test("the plugins route keeps no second copy of the package-entry rules", async () => {
  const { readFile } = await import("node:fs/promises");
  const routeSource = await readFile(new URL("../app/api/plugins/route.ts", import.meta.url), "utf8");
  assert.match(routeSource, /from "@\/lib\/package-settings"/, "the route imports the shared rules");
  assert.doesNotMatch(routeSource, /function getPackageSource\(/, "getPackageSource lives once, in lib/package-settings.ts");
  assert.doesNotMatch(routeSource, /function isDisabledPackage\(/, "isDisabledPackage lives once, in lib/package-settings.ts");
});