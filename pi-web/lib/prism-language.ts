// react-syntax-highlighter's PrismAsyncLight (backed by refractor's async
// loader table) keys every per-language dynamic import by refractor's
// CANONICAL grammar name. Most of the language ids this app already uses
// (data.language from the file API, see EXT_TO_LANGUAGE in
// app/api/files/[...path]/route.ts) already match that canonical id 1:1 —
// "rust", "java", "toml", "csharp", etc. all load with no translation.
//
// A few ids we use are refractor ALIASES rather than canonical names. An
// alias only becomes registered as a side effect of its canonical grammar
// module executing (e.g. refractor/lang/docker.js ends with
// `Prism.languages.dockerfile = Prism.languages.docker`), and
// PrismAsyncLight's default loader table has no entry keyed by the alias
// itself. Passing an alias straight through as the `language` prop would
// never trigger a load — `isSupportedLanguage()` checks
// `languageLoaders[language]`, finds nothing for e.g. "dockerfile", and the
// file renders as plain unhighlighted text forever.
//
// Keep this map to exactly the mismatches that exist; do not add entries for
// ids that already equal their canonical name.
const PRISM_LANGUAGE_ALIASES: Record<string, string> = {
  dockerfile: "docker",
  html: "markup",
  xml: "markup",
};

/**
 * Translate an internal language id (from `data.language`) into the id
 * react-syntax-highlighter's async per-language loader table actually keys
 * its dynamic import on. Ids with no known alias pass through unchanged.
 */
export function resolveHighlightLanguage(language: string): string {
  return PRISM_LANGUAGE_ALIASES[language] ?? language;
}
