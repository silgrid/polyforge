/**
 * Typed guard for client strings passed as POSITIONAL arguments to the
 * bundled skills CLI (audit S7).
 *
 * Verification (2026-09-27, skills@1.5.21 — the version `npx skills`
 * resolves here, read from the npx cache and confirmed empirically):
 *
 * - `parseAddOptions` (dist/cli.mjs) treats EVERY argument starting with `-`
 *   as an option: known flags mutate options (`-g`, `--yes`, `--copy`,
 *   `--metadata <json>`, `--agent <names>…`), unknown leading-dash strings
 *   are silently dropped from the source list.
 * - `parseFindOptions` consumes `--owner` / `--owner=<owner>` as a filter
 *   option instead of query text.
 * - The CLI does NOT honor `--` as an end-of-options marker: it is dropped
 *   like any unknown leading-dash string, so the classic separator does not
 *   protect a client-supplied positional. Verified empirically:
 *   `skills add -- "-g" …` still swallows `-g` as the global flag
 *   ("Missing required argument: source").
 *
 * Therefore the routes must refuse leading-dash client strings BEFORE they
 * reach argv. Typed refusal codes only — never English prose.
 */

export type CliArgumentRefusalReason = "leadingDashCliArgument";

export type CliArgumentCheck =
  | { ok: true; value: string }
  | { ok: false; reason: CliArgumentRefusalReason };

/**
 * A client string is safe as a positional only when it cannot be parsed as
 * an option, i.e. it does not start with `-` after trimming. This is the
 * narrowest check that covers the verified CLI behavior (every option form
 * starts with `-`); it does not try to predict which flags exist.
 */
export function validatePositionalCliArgument(value: string): CliArgumentCheck {
  const trimmed = value.trim();
  if (trimmed.startsWith("-")) return { ok: false, reason: "leadingDashCliArgument" };
  return { ok: true, value: trimmed };
}
