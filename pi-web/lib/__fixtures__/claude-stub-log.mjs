// Reads the claude stub's JSON-lines log (claude-stub/claude.mjs, $PI_TEST_CLAUDE_STUB_LOG) while stubs may
// still be appending to it. A read can land mid-append, so the file can end in a record whose newline is not
// there yet: only newline-terminated lines are complete records, and the unterminated tail is left for the
// next read. Any other unparsable line still throws: that is a broken log, not a race.
import fs from "node:fs";

/** The complete records of the stub log ([] while it does not exist). */
export function readStubLog(file) {
  if (!fs.existsSync(file)) return [];
  const text = fs.readFileSync(file, "utf8");
  return text.slice(0, text.lastIndexOf("\n") + 1).split("\n").filter(Boolean).map((line) => JSON.parse(line));
}
