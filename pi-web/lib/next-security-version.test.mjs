// Pins the Next.js patch line pi-web ships: `next` and `eslint-config-next`
// move together, and package-lock.json stays in sync, so `npm ci` installs
// exactly the version `next build`'s security-advisory check sees.
// polyforge#104: npm advisory 1247486 (moderate, affects next >=16.3.0
// <=16.4.4) stopped the v0.2.2 releases with SecurityFatalError; 16.4.6 is
// the same patch line's fix. A future bump moves this pin in the same commit
// as package.json and package-lock.json.
import "#test-env";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import test from "node:test";

const require = createRequire(import.meta.url);
const packageJson = require("../package.json");
const lock = require("../package-lock.json");

const NEXT_VERSION = "16.4.6"; // advisory 1247486: >=16.3.0 <=16.4.4 affected

test("pins next and eslint-config-next at 16.4.6 with package-lock.json in sync", () => {
  assert.equal(packageJson.dependencies.next, NEXT_VERSION, "dependencies.next");
  assert.equal(packageJson.devDependencies["eslint-config-next"], NEXT_VERSION, "devDependencies.eslint-config-next");
  assert.equal(lock.packages["node_modules/next"].version, NEXT_VERSION, "package-lock node_modules/next");
  assert.equal(lock.packages["node_modules/eslint-config-next"].version, NEXT_VERSION, "package-lock node_modules/eslint-config-next");
});
