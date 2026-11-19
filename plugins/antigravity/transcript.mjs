import { closeSync, fstatSync, openSync, readFileSync, readSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fingerprint, nativeOrigin, recentTurns, safeID, textOf } from "./client.mjs";
import { readTitle, rootDir } from "./annotations.mjs";

const MAX = 8 * 1024 * 1024;
function clean(value) {
  let text = textOf(value).replace(/<USER_REQUEST>([\s\S]*?)<\/USER_REQUEST>/i, "$1");
  text = text.replace(/<ADDITIONAL_METADATA>[\s\S]*?<\/ADDITIONAL_METADATA>/gi, "").trim();
  return text.slice(0, 1500);
}
function allowedPath(file, base) {
  try {
    const real = realpathSync(file), root = realpathSync(base), rel = relative(root, real);
    return rel && !rel.startsWith("..") && !isAbsolute(rel) ? real : undefined;
  } catch (error) { if (error.code === "ENOENT") return undefined; throw error; }
}
export function identify(input, dataDir = process.env.ANTIGRAVITY_DATA_DIR || rootDir()) {
  const id = input?.conversationId ?? input?.conversation_id;
  const file = input?.transcriptPath ?? input?.transcript_path;
  if (!safeID(id) || typeof file !== "string" || !isAbsolute(file) || basename(file) !== "transcript.jsonl") return undefined;
  let configured = resolve(dataDir);
  const parts = file.split(/[\\/]/), brainIndex = parts.lastIndexOf("brain");
  if (!process.env.ANTIGRAVITY_DATA_DIR && brainIndex >= 1) {
    const inferred = parts.slice(0, brainIndex).join("/");
    const gemini = resolve(join(homedir(), ".gemini"));
    const rel = relative(gemini, resolve(inferred));
    if (basename(inferred).startsWith("antigravity") && rel && !rel.startsWith("..") && !isAbsolute(rel)) configured = resolve(inferred);
  }
  const appRoot = join(configured, "brain");
  const path = allowedPath(file, appRoot);
  if (!path) return undefined;
  const expected = join(appRoot, id, ".system_generated", "logs", "transcript.jsonl");
  if (resolve(path) !== resolve(expected)) return undefined;
  return { sessionId: id, path, appDataDir: configured };
}
function readLines(path) {
  const fd = openSync(path, "r");
  try { const stat = fstatSync(fd); if (!stat.isFile() || stat.size > MAX) return []; const buf = Buffer.alloc(stat.size); readSync(fd, buf, 0, stat.size, 0); return buf.toString("utf8").split(/\r?\n/).filter(Boolean); }
  finally { closeSync(fd); }
}
function collect(path) {
  const rows = [];
  for (const line of readLines(path)) {
    let row; try { row = JSON.parse(line); } catch { continue; }
    if (!row || !["USER_INPUT", "PLANNER_RESPONSE"].includes(row.type)) continue;
    if (row.type === "USER_INPUT" && row.source === "USER_EXPLICIT") {
      const text = clean(row.content); if (text && !/^\s*\//.test(text)) rows.push({ role: "user", text, id: String(row.step_index ?? rows.length) });
    } else if (row.type === "PLANNER_RESPONSE" && row.source === "MODEL") {
      const hasTools = Array.isArray(row.tool_calls) && row.tool_calls.length > 0;
      const text = clean(row.content); if (text && !hasTools) rows.push({ role: "assistant", text, id: String(row.step_index ?? rows.length) });
    }
  }
  return rows;
}
export function originalGoal(identified) { return collect(identified.path).find(row => row.role === "user")?.text; }
export function readObservedTitle(identified) { return readTitle(identified.appDataDir, identified.sessionId); }
export function snapshot(identified, input = {}) {
  const rows = collect(identified.path), user = rows.findLast(row => row.role === "user"), answer = rows.findLast(row => row.role === "assistant");
  if (!user || !answer) return undefined;
  const currentTitle = readObservedTitle(identified);
  const turns = recentTurns(rows.map(({ role, text }) => ({ role, text })));
  return { agent: "antigravity", sessionId: identified.sessionId, currentTitle, titleOrigin: rows.filter(r => r.role === "user").length <= 1 ? "auto" : "unknown", revision: fingerprint(user.id, answer.id, answer.text), project: typeof input.cwd === "string" && isAbsolute(input.cwd) ? input.cwd : undefined, turns, confirmationOnly: false };
}
