// A Host pattern list (polyforge#51) as the editor holds it — one regex per
// line — and as pf stores it: the lines a save writes, and the text a landed
// save settles to (polyforge#277), are one fact.

/** The patterns an editor text writes: each line trimmed, blank lines dropped. */
export function patternLines(text: string): string[] {
  return text.split("\n").map((line) => line.trim()).filter((line) => line !== "");
}
