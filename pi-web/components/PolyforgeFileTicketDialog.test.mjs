import "#test-env";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./PolyforgeFileTicketDialog.tsx", import.meta.url), "utf8");

test("the dialog opens prefilled from the analysis and every field is editable", () => {
  assert.match(source, /useState\(\(\) => fileTicketDialogDefaults\(analysis, message, projects\)\)/, "one prefill from the pure defaults");
  assert.match(source, /<input value=\{goal\} onChange=\{\(event\) => setGoal\(event\.target\.value\)\}/);
  assert.match(source, /<select value=\{project\} onChange=\{\(event\) => setProject\(event\.target\.value\)\}/);
  assert.match(source, /projects\.map\(\(name\) => <option key=\{name\} value=\{name\}>\{name\}<\/option>\)/, "the select lists the mapped projects");
  assert.match(source, /<textarea value=\{content\} onChange=\{\(event\) => setContent\(event\.target\.value\)\}/);
  assert.match(source, /role="dialog"/);
  assert.match(source, /aria-modal="true"/);
});

test("cancel writes nothing; confirm runs exactly one request; a refusal keeps the dialog with pf's text", () => {
  assert.equal(source.split("fetch(").length - 1, 1, "one request, in confirm only");
  const confirm = source.slice(source.indexOf("const confirm = async"), source.indexOf("const field ="));
  assert.match(confirm, /if \(busy\) return;/, "a confirm in flight starts no second request");
  assert.match(confirm, /fetch\("\/api\/polyforge\/messages\/file-ticket", \{\s*method: "POST",/);
  assert.match(confirm, /body: JSON\.stringify\(\{ goal, project, content \}\)/);
  assert.match(confirm, /setError\(body\.error \?\? t\("polyforge\.messages\.ticketFailed"\)\);\s*return;/, "a refusal stays open with pf's text");
  assert.match(confirm, /onCreated\(body\.slug\);/, "success hands the slug up");
  assert.doesNotMatch(confirm, /retry|setTimeout/, "never retried");
  const cancel = source.slice(source.indexOf('<button type="button" onClick={onClose}'));
  assert.match(cancel, /t\("polyforge\.messages\.ticketCancel"\)/);
  assert.doesNotMatch(source, /bundle_name|bundle:|"--bundle"|<select[^>]*bundle/, "the dialog offers no lane or bundle");
  assert.doesNotMatch(source, /localStorage|sessionStorage/);
});
