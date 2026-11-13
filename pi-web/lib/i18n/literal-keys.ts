// Static check that every literal t() key exists in the English message table.
// Test tooling only: lib/i18n/literal-keys.test.mjs runs it.
//
// A translation call is `t(…)`, `translate(…)` (the name two components give
// useI18n's t) or `<object>.t(…)`. Every literal leaf of its first argument — a
// string, a template without `${}`, a branch of `?:`, `??` or `||` — is checked
// against English. A key concatenated at run time (`${}` or `+`) cannot be
// checked: it must be listed in dynamic-key-allowlist.json with a one-line
// reason, and each entry must still match such a call, so none outlives its
// code (owner ruling on review r1#1). Other dynamic arguments (a variable, a
// table lookup) are not listed: their keys are literals somewhere else.
import { readdirSync } from "node:fs";
import { join } from "node:path";
import ts from "typescript";

/** One source file to scan, its path relative to the pi-web package root. */
export interface SourceFile {
  path: string;
  source: string;
}

export interface LiteralKeyReport {
  /** Human-readable problems; empty when every literal key exists and the allowlist is valid. */
  problems: string[];
  /** Number of literal keys checked (a guard against a scanner that matches nothing). */
  literalCount: number;
}

const SCANNED_DIRS = ["app", "components", "hooks", "lib"];

/** Lists the scanned source files (app/, components/, hooks/, lib/; .ts/.tsx, no tests). */
export function listSourceFiles(root: string): string[] {
  const files: string[] = [];
  const walk = (dir: string) => {
    for (const entry of readdirSync(join(root, dir), { withFileTypes: true })) {
      const path = `${dir}/${entry.name}`;
      if (entry.isDirectory()) {
        if (entry.name !== "node_modules") walk(path);
      } else if (/\.tsx?$/.test(entry.name) && !/\.test\.|\.d\.ts$/.test(entry.name)) {
        files.push(path);
      }
    }
  };
  for (const dir of SCANNED_DIRS) walk(dir);
  return files.sort();
}

function isTranslationCallee(callee: ts.Expression): boolean {
  if (ts.isIdentifier(callee)) return callee.text === "t" || callee.text === "translate";
  return ts.isPropertyAccessExpression(callee) && callee.name.text === "t";
}

function unwrap(node: ts.Expression): ts.Expression {
  while (ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isSatisfiesExpression(node)) {
    node = node.expression;
  }
  return node;
}

interface ArgumentLeaves {
  keys: string[];
  concatenated: boolean;
}

/** The literal keys of an argument's leaves, and whether any leaf concatenates a key. */
function argumentLeaves(node: ts.Expression): ArgumentLeaves {
  node = unwrap(node);
  // An empty fallback (`t(LABELS[v] ?? "")`) renders nothing; it names no key.
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return { keys: node.text ? [node.text] : [], concatenated: false };
  if (ts.isTemplateExpression(node)) return { keys: [], concatenated: true };
  const parts = ts.isConditionalExpression(node) ? [node.whenTrue, node.whenFalse]
    : ts.isBinaryExpression(node)
      && (node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken || node.operatorToken.kind === ts.SyntaxKind.BarBarToken)
      ? [node.left, node.right] : undefined;
  if (!parts) {
    const concatenated = ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken;
    return { keys: [], concatenated };
  }
  const leaves = parts.map(argumentLeaves);
  return { keys: leaves.flatMap((leaf) => leaf.keys), concatenated: leaves.some((leaf) => leaf.concatenated) };
}

/** Checks every literal translation key against English and the concatenated keys against the allowlist. */
export function checkLiteralKeys(
  files: readonly SourceFile[],
  englishKeys: ReadonlySet<string>,
  allowlist: unknown,
): LiteralKeyReport {
  const problems: string[] = [];
  const entries = Array.isArray(allowlist) ? allowlist as { file?: unknown; argument?: unknown; reason?: unknown }[] : [];
  const listed = new Set(entries
    .filter((entry) => typeof entry?.file === "string" && typeof entry.argument === "string")
    .map((entry) => `${entry.file}\0${entry.argument}`));
  const concatenatedCalls = new Set<string>();
  let literalCount = 0;

  for (const file of files) {
    const kind = file.path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
    const sourceFile = ts.createSourceFile(file.path, file.source, ts.ScriptTarget.Latest, true, kind);
    const visit = (node: ts.Node) => {
      if (ts.isCallExpression(node) && isTranslationCallee(node.expression) && node.arguments.length > 0) {
        const argument = node.arguments[0];
        const line = sourceFile.getLineAndCharacterOfPosition(argument.getStart(sourceFile)).line + 1;
        const leaves = argumentLeaves(argument);
        for (const key of leaves.keys) {
          literalCount++;
          if (!englishKeys.has(key)) problems.push(`${file.path}:${line} "${key}" is not in lib/i18n/messages/en.ts`);
        }
        if (leaves.concatenated) {
          const text = argument.getText(sourceFile).replace(/\s+/g, " ");
          concatenatedCalls.add(`${file.path}\0${text}`);
          if (Array.isArray(allowlist) && !listed.has(`${file.path}\0${text}`)) {
            problems.push(`${file.path}:${line} ${text} concatenates its key at run time: list it in lib/i18n/dynamic-key-allowlist.json with a one-line reason`);
          }
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(sourceFile);
  }

  if (!Array.isArray(allowlist)) {
    problems.push("dynamic-key allowlist must be a JSON array");
    return { problems, literalCount };
  }
  const seen = new Set<string>();
  entries.forEach((entry, index) => {
    if (typeof entry?.file !== "string" || typeof entry.argument !== "string") {
      problems.push(`allowlist[${index}]: file and argument must be strings`);
      return;
    }
    const label = `allowlist[${index}] ${entry.file} ${entry.argument}`;
    const id = `${entry.file}\0${entry.argument}`;
    if (typeof entry.reason !== "string" || !entry.reason.trim() || /[\r\n]/.test(entry.reason)) {
      problems.push(`${label}: reason must be one non-empty line`);
    } else if (seen.has(id)) {
      problems.push(`${label}: duplicate entry`);
    } else if (!concatenatedCalls.has(id)) {
      problems.push(`${label}: matches no concatenated t() key (stale)`);
    }
    seen.add(id);
  });
  return { problems, literalCount };
}
