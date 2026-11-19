import { closeSync, fstatSync, openSync, readFileSync, readSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, join, relative, resolve } from "node:path";
import { StringDecoder } from "node:string_decoder";
import { fingerprint, isTextConfirmation, nativeOrigin, recentTurns, safeID, textOf } from "./client.mjs";

const maxTailBytes = 768 * 1024;
const promptSnapshotFields = new Set(["type", "systemPrompt", "tools", "cliPrefix", "reminderFold", "systemTurns",
  "toolChangeHeader", "inlineTools", "keptReminders", "echoWireToolInputs", "contextRendering"]);
const hookSuccessFields = new Set(["type", "hookName", "toolUseID", "hookEvent", "content", "stdout",
  "stderr", "exitCode", "command", "durationMs"]);
const bookkeepingHooks = new Set(["SessionStart", "UserPromptSubmit", "Stop"]);

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

function readWindow(path, head = false) {
  const descriptor = openSync(path, "r");
  try {
    const info = fstatSync(descriptor);
    if (!info.isFile()) throw new Error("Claude 记录路径不是普通文件");
    const length = Math.min(head ? 96 * 1024 : maxTailBytes, info.size);
    const start = head ? 0 : info.size - length;
    const buffer = Buffer.allocUnsafe(length);
    const bytes = readSync(descriptor, buffer, 0, length, start);
    const lines = buffer.toString("utf8", 0, bytes).split("\n");
    if (start) lines.shift();
    if (head && bytes < info.size) lines.pop();
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

function isBookkeeping(attachment) {
  // 转录中的提示词快照只是宿主对当前环境的记录。
  if (attachment?.type === "prompt_snapshot") {
    return Array.isArray(attachment.systemPrompt) && Object.keys(attachment).every(key => promptSnapshotFields.has(key));
  }
  if (attachment?.type !== "hook_success" || typeof attachment.stdout !== "string") return false;
  const stdout = attachment.stdout.trim();
  return bookkeepingHooks.has(attachment.hookEvent) && attachment.exitCode === 0 &&
    attachment.content === "" && attachment.stderr === "" && (!stdout || /^\{\s*\}$/.test(stdout)) &&
    Object.keys(attachment).every(key => hookSuccessFields.has(key));
}

function collect(line, sessionId) {
  let record;
  try { record = JSON.parse(line); } catch { return undefined; }
  if (!record || (record.sessionId && record.sessionId !== sessionId) || record.isSidechain) return undefined;
  if (record.type === "custom-title" && typeof record.customTitle === "string") return { title: record.customTitle };
  if (record.type === "attachment") return isBookkeeping(record.attachment) ? undefined : { extra: true };
  if (!safeID(record.uuid) || record.isMeta || record.isSynthetic || record.isCompactSummary) return undefined;
  const role = record.type;
  if (role !== "user" && role !== "assistant") return undefined;
  const content = record.message?.content;
  const text = role === "user" ? cleanUser(content) : textOf(content);
  if (/^(?:<local-command-|<command-name>|<command-message>|\[Request interrupted)/.test(text)) return undefined;
  const parts = Array.isArray(content) ? content : [];
  if (role === "user") {
    if (parts.length && parts.every(part => part?.type === "tool_result")) return { extra: true };
    if (!text && !parts.some(part => part?.type !== "text")) return undefined;
    const extras = record.attachments?.length || record.message?.attachments?.length || record.imagePasteIds?.length;
    return { id: record.uuid, role, text, confirmation: !extras && isTextConfirmation(content) };
  }
  const tools = parts.some(part => typeof part?.type === "string" && /(?:^|_)tool_(?:use|result)$/.test(part.type)) ||
    record.message?.stop_reason === "tool_use";
  if (text) return { id: record.uuid, role, text, final: !tools, extra: tools };
  if (parts.length || tools) return { extra: true };
}

export function originalGoal({ path, sessionId }) {
  for (const line of readWindow(path, true).lines) {
    const entry = collect(line, sessionId);
    if (entry?.role === "user") return entry.text || undefined;
  }
}

function latestConfirmation(messages, user, truncated) {
  const index = messages.lastIndexOf(user);
  const previousAnswer = messages.findLastIndex((entry, position) => position < index && entry.final);
  if (previousAnswer < 0 && truncated) return undefined;
  const latest = messages.slice(previousAnswer + 1);
  return latest.every(entry => !entry.extra && (entry.role !== "user" || entry.confirmation)) || undefined;
}

function scanTail(identified) {
  const { lines, truncated } = readWindow(identified.path);
  let recordTitle;
  const messages = [];
  for (const line of lines) {
    const data = collect(line, identified.sessionId);
    if (data?.id || data?.extra) messages.push(data);
    else if (data && "title" in data) recordTitle = data.title;
  }
  return { recordTitle, messages, truncated };
}

const scanChunkBytes = 1024 * 1024;
// Stop 与确认轮询会在同一进程内反复调用；记录只追加写入，大小不变即可复用结果。
let scanCache = { path: "", size: -1, title: undefined };

// 尾部窗口可能落在标题记录之后；sidecar 缺失时完整扫描一次，取最后一条标题记录。
function scanRecordedTitle({ path, sessionId }) {
  const descriptor = openSync(path, "r");
  try {
    const info = fstatSync(descriptor);
    if (!info.isFile()) return undefined;
    if (scanCache.path === path && scanCache.size === info.size) return scanCache.title;
    const decoder = new StringDecoder("utf8");
    const buffer = Buffer.allocUnsafe(scanChunkBytes);
    let carry = "";
    let title;
    for (let position = 0; position < info.size; ) {
      const bytes = readSync(descriptor, buffer, 0, Math.min(buffer.length, info.size - position), position);
      if (bytes <= 0) break;
      position += bytes;
      const lines = (carry + decoder.write(buffer.subarray(0, bytes))).split("\n");
      carry = lines.pop();
      for (const line of lines) {
        const found = recordedTitle(line, sessionId);
        if (found !== undefined) title = found;
      }
    }
    const found = recordedTitle(carry + decoder.end(), sessionId);
    const result = found === undefined ? title : found;
    scanCache = { path, size: info.size, title: result };
    return result;
  } finally { closeSync(descriptor); }
}

function recordedTitle(line, sessionId) {
  if (!line.includes('"custom-title"')) return undefined;
  const data = collect(line, sessionId);
  return data && "title" in data ? data.title : undefined;
}

function reconcileTitle(identified, recordTitle, truncated, input = {}) {
  const sidecar = customTitleFromSidecar(identified);
  if (recordTitle !== undefined && sidecar !== undefined && recordTitle !== sidecar) return undefined;
  // 大文件不代表标题未知：整份记录都确认没有标题，才按无标题处理。
  const title = recordTitle ?? sidecar ?? (truncated ? scanRecordedTitle(identified) : undefined);
  const hookTitle = typeof input.session_title === "string" ? input.session_title : undefined;
  if (hookTitle !== undefined && title !== undefined && hookTitle !== title) return undefined;
  return title ?? hookTitle ?? "";
}

export function readObservedTitle(identified) {
  const { recordTitle, truncated } = scanTail(identified);
  return reconcileTitle(identified, recordTitle, truncated);
}

export function snapshot(identified, input = {}) {
  const { recordTitle, messages, truncated } = scanTail(identified);
  const currentTitle = reconcileTitle(identified, recordTitle, truncated, input);
  if (currentTitle === undefined) return undefined;

  const user = messages.findLast(message => message.role === "user");
  const last = messages.findLast(message => message.id);
  const complete = user && last.role === "assistant";
  const turns = complete ? recentTurns(messages.filter(message => message.id).map(({ role, text }) => ({ role, text }))) : [];
  return {
    agent: "claude", sessionId: identified.sessionId, currentTitle, titleOrigin: nativeOrigin(currentTitle),
    revision: complete ? fingerprint(user.id, last.id, last.text) : undefined,
    project: typeof input.cwd === "string" && isAbsolute(input.cwd) ? input.cwd : undefined,
    turns,
    confirmationOnly: complete ? latestConfirmation(messages, user, truncated) : undefined,
  };
}
