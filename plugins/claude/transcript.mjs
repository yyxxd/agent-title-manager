import { closeSync, fstatSync, openSync, readFileSync, readSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fingerprint, nativeOrigin, recentTurns, safeID, textOf } from "./client.mjs";

const maxTailBytes = 768 * 1024;

export function identify(input, configDir = process.env.CLAUDE_CONFIG_DIR || join(homedir(), ".claude")) {
  const sessionId = input.session_id;
  const location = input.transcript_path;
  if (!safeID(sessionId) || typeof location !== "string" || !isAbsolute(location) ||
      basename(location) !== sessionId + ".jsonl") return undefined;

  const root = join(resolve(configDir), "projects");
  const withinRoot = (rootPath, file) => {
    const path = relative(rootPath, file);
    return path.length > 0 && !path.startsWith("..") && !isAbsolute(path);
  };
  if (!withinRoot(root, location)) return undefined;
  try {
    const path = realpathSync(location);
    if (!withinRoot(realpathSync(root), path)) return undefined;
    return { sessionId, path };
  } catch (error) {
    if (error.code === "ENOENT") return undefined;
    throw error;
  }
}

function readTail(path) {
  const descriptor = openSync(path, "r");
  try {
    const info = fstatSync(descriptor);
    if (!info.isFile()) throw new Error("Claude 记录路径不是普通文件");
    const length = Math.min(maxTailBytes, info.size);
    const start = info.size - length;
    const buffer = Buffer.allocUnsafe(length);
    const bytes = readSync(descriptor, buffer, 0, length, start);
    const lines = buffer.toString("utf8", 0, bytes).split("\n");
    if (start) lines.shift();
    return { lines, truncated: start > 0 };
  } finally { closeSync(descriptor); }
}

function customTitleFromSidecar({ path, sessionId }) {
  const directory = dirname(path);
  const file = join(directory, sessionId, "custom-title.json");
  let real;
  try { real = realpathSync(file); }
  catch (error) {
    if (error.code === "ENOENT") return undefined;
    throw error;
  }
  if (real !== file) return undefined;
  const descriptor = openSync(real, "r");
  try {
    const info = fstatSync(descriptor);
    if (!info.isFile() || info.size > 32 * 1024) return undefined;
    const value = JSON.parse(readFileSync(descriptor, "utf8")).customTitle;
    return typeof value === "string" ? value : undefined;
  } finally { closeSync(descriptor); }
}

function cleanUser(content) {
  return textOf(content).replace(/<(system-reminder|local-command-caveat|ide_opened_file|ide_selection)>[\s\S]*?<\/\1>/g, "").trim();
}

function collect(line, sessionId) {
  let record;
  try { record = JSON.parse(line); } catch { return undefined; }
  if (!record || (record.sessionId && record.sessionId !== sessionId) || record.isSidechain) return undefined;
  if (record.type === "custom-title" && typeof record.customTitle === "string") return { title: record.customTitle };
  if (!safeID(record.uuid) || record.isMeta || record.isSynthetic || record.isCompactSummary) return undefined;
  const role = record.type;
  if (role !== "user" && role !== "assistant") return undefined;
  const text = role === "user" ? cleanUser(record.message?.content) : textOf(record.message?.content);
  if (!text || /^(?:<local-command-|<command-name>|<command-message>|\[Request interrupted)/.test(text)) return undefined;
  return { id: record.uuid, role, text };
}

export function snapshot(identified, input = {}) {
  const { lines, truncated } = readTail(identified.path);
  let recordTitle;
  const messages = [];
  for (const line of lines) {
    const data = collect(line, identified.sessionId);
    if (data?.id) messages.push(data);
    else if (data && "title" in data) recordTitle = data.title;
  }
  const title = recordTitle ?? customTitleFromSidecar(identified);
  const hookTitle = typeof input.session_title === "string" ? input.session_title : undefined;
  if (hookTitle !== undefined && title !== undefined && hookTitle !== title) return undefined;
  const currentTitle = title ?? hookTitle ?? (truncated ? undefined : "");
  if (currentTitle === undefined) return undefined;

  const user = messages.findLast(message => message.role === "user");
  const last = messages.at(-1);
  const complete = user && last.role === "assistant";
  const turns = complete ? recentTurns(messages.map(({ role, text }) => ({ role, text }))) : [];
  return {
    agent: "claude", sessionId: identified.sessionId, currentTitle, titleOrigin: nativeOrigin(currentTitle),
    revision: complete ? fingerprint(user.id, last.id, last.text) : undefined,
    project: typeof input.cwd === "string" && isAbsolute(input.cwd) ? input.cwd : undefined,
    turns,
  };
}
