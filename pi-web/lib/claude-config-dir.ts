// Where the system claude keeps its transcripts: the
// CLAUDE_CONFIG_DIR pi-web inherited, else ~/.claude — the user's own claude
// config, exactly the dir a `claude` run from pi-web's terminal would use.
// pi-web finds a session's transcript here by id (and its subagents' sidecars beside
// it) and, on delete, removes that one transcript file; the only
// thing it writes here is a fork's copy, beside its source's transcript
// (forkClaudeSession()). It never creates the dir itself. The retired
// `<pi agent dir>/claude` is neither read, created, migrated nor deleted.
import { existsSync, readdirSync, unlinkSync } from "fs";
import { homedir } from "os";
import { dirname, join } from "path";

/** The inherited `CLAUDE_CONFIG_DIR` (trimmed) when set, else `~/.claude`. */
export function claudeConfigDir(env: NodeJS.ProcessEnv = process.env): string {
  const inherited = env.CLAUDE_CONFIG_DIR?.trim();
  return inherited ? inherited : join(homedir(), ".claude");
}

const SESSION_ID_PATTERN = /^[A-Za-z0-9-]{1,128}$/;

/**
 * The transcript claude writes for `sessionId`: `<dir>/projects/<any>/<id>.jsonl`.
 * Looked up by id, never by guessing claude's cwd encoding. Null when absent.
 */
export function claudeTranscriptFile(sessionId: string, configDir: string = claudeConfigDir()): string | null {
  if (!SESSION_ID_PATTERN.test(sessionId)) return null;
  const projects = join(configDir, "projects");
  let dirs: string[];
  try {
    dirs = readdirSync(projects, { withFileTypes: true }).filter((entry) => entry.isDirectory()).map((entry) => entry.name);
  } catch {
    return null;
  }
  for (const dir of dirs) {
    const file = join(projects, dir, `${sessionId}.jsonl`);
    if (existsSync(file)) return file;
  }
  return null;
}

const SIDECAR_PATTERN = /^agent-([A-Za-z0-9_-]{1,128})\.jsonl$/;

/** One subagent's sidecar files: claude's `agent-<id>.jsonl` and its `agent-<id>.meta.json`. */
export interface ClaudeSidecarFile {
  agentId: string;
  file: string;
  metaFile: string;
}

/**
 * The sidecars claude wrote for a session's subagents, every depth alike:
 * `<transcript dir>/<id>/subagents/agent-<agent id>.jsonl` (+ `.meta.json`).
 * Empty when the transcript or the directory is absent.
 */
export function claudeSidecarFiles(sessionId: string, configDir: string = claudeConfigDir(), transcript: string | null = claudeTranscriptFile(sessionId, configDir)): ClaudeSidecarFile[] {
  if (!transcript || !SESSION_ID_PATTERN.test(sessionId)) return [];
  const dir = join(dirname(transcript), sessionId, "subagents");
  let names: string[];
  try {
    names = readdirSync(dir);
  } catch {
    return [];
  }
  return names.sort().flatMap((name) => {
    const match = SIDECAR_PATTERN.exec(name);
    return match ? [{ agentId: match[1], file: join(dir, name), metaFile: join(dir, `agent-${match[1]}.meta.json`) }] : [];
  });
}

/** Removes that session's transcript only. True when one was removed. */
export function removeClaudeTranscript(sessionId: string, configDir?: string): boolean {
  const file = claudeTranscriptFile(sessionId, configDir);
  if (!file) return false;
  try {
    unlinkSync(file);
    return true;
  } catch {
    return false;
  }
}
