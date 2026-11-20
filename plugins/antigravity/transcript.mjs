import { closeSync, fstatSync, openSync, readSync } from "node:fs";
import { isAbsolute } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fingerprint, isTextConfirmation, recentTurns, textOf } from "./client.mjs";
import { readTitle } from "./annotations.mjs";
import { roots, safeID, safeStat, sameFileIdentity, samePath, transcriptPath } from "./paths.mjs";

function clean(value) {
  return textOf(value).replace(/<USER_REQUEST>([\s\S]*?)<\/USER_REQUEST>/i, "$1")
    .replace(/<ADDITIONAL_METADATA>[\s\S]*?<\/ADDITIONAL_METADATA>/gi, "").trim().slice(0, 1500);
}

export function identify(input, dataDir) {
  const id = input?.conversationId ?? input?.conversation_id;
  const file = input?.transcriptPath ?? input?.transcript_path;
  if (dataDir !== undefined && (typeof dataDir !== "string" || !isAbsolute(dataDir))) return;
  if (!safeID(id) || typeof file !== "string" || !isAbsolute(file) || /(?:^|[\\/])\.\.(?:[\\/]|$)/.test(file)) return;
  const candidates = dataDir ? [{ dir: dataDir, kind: "explicit" }] : roots();
  for (const root of candidates) {
    const path = transcriptPath(root.dir, id);
    if (!samePath(file, path)) continue;
    const info = safeStat(root.dir, path);
    if (!info) continue;
    if (!info.isFile() || info.nlink !== 1) throw new Error("Invalid Antigravity transcript");
    return { sessionId: id, path, appDataDir: root.dir, kind: root.kind };
  }
}

export function locateNative(id) {
  if (!safeID(id)) return;
  const found = roots().map(root => identify({ conversationId: id, transcriptPath: transcriptPath(root.dir, id) })).filter(Boolean);
  if (found.length === 1) return found[0];
}

function readWindow(identified, limit, head) {
  const info = safeStat(identified.appDataDir, identified.path);
  if (!info) return { lines: [], partial: false };
  const fd = openSync(identified.path, "r");
  try {
    const stat = fstatSync(fd);
    if (!stat.isFile() || stat.nlink !== 1) throw new Error("Invalid Antigravity transcript");
    if (!sameFileIdentity(info, stat)) return { lines: [], partial: true };
    const start = head ? 0 : Math.max(0, stat.size - limit);
    const seek = start ? start - 1 : start;
    const buf = Buffer.alloc(Math.min(stat.size - seek, limit + 1));
    let length = 0;
    while (length < buf.length) {
      const read = readSync(fd, buf, length, buf.length - length, seek + length);
      if (!read) break;
      length += read;
    }
    if (fstatSync(fd).size !== stat.size || length !== buf.length) return { lines: [], partial: true };
    const lines = buf.subarray(0, length).toString("utf8").split(/\r?\n/);
    if (start) lines.shift();
    if (head && length < stat.size) lines.pop();
    return { lines, partial: false, truncated: start > 0 };
  } finally { closeSync(fd); }
}

function collect(identified, limit, head = false) {
  const window = readWindow(identified, limit, head);
  const rows = [];
  let partial = window.partial;
  for (const line of window.lines) {
    if (!line.trim()) continue;
    let row;
    try { row = JSON.parse(line); } catch { partial = true; rows.length = 0; continue; }
    if (!row || typeof row !== "object") continue;
    const finished = row.status === undefined || row.status === "DONE";
    const tools = Array.isArray(row.tool_calls) && row.tool_calls.length > 0;
    const step = Number.isSafeInteger(row.step_index) ? row.step_index : undefined;
    const metadata = { step, at: Date.parse(row.created_at) || undefined };
    if (row.type === "USER_INPUT" && row.source === "USER_EXPLICIT") {
      const text = clean(row.content);
      rows.push({ ...metadata, role: "user", text: finished && !text.startsWith("/") ? text : "",
        confirmation: isTextConfirmation(text), media: Array.isArray(row.media) && row.media.length > 0, tools });
      partial = false;
    } else if (row.type === "PLANNER_RESPONSE" && row.source === "MODEL") {
      const text = clean(row.content);
      rows.push(finished && !tools && text ? { ...metadata, role: "assistant", text } : { role: "boundary" });
    } else if (row.source === "MODEL" || row.type === "ERROR_MESSAGE") {
      rows.push({ role: "boundary" });
    }
  }
  return { rows, partial, truncated: window.truncated };
}

export function originalGoal(identified) {
  return collect(identified, 256 * 1024, true).rows.find(row => row.role === "user" && row.text)?.text;
}

export function readObservedTitle(identified) {
  return readTitle(identified.appDataDir, identified.sessionId);
}

function follows(user, answer) {
  return (user.step === undefined || answer.step === undefined || answer.step > user.step) &&
    (!user.at || !answer.at || answer.at >= user.at);
}

export function completedTurn(identified) {
  let collected = collect(identified, 1024 * 1024);
  if (collected.truncated && !collected.rows.some(row => row.role === "user")) collected = collect(identified, 8 * 1024 * 1024);
  const { rows, partial } = collected;
  const index = rows.findLastIndex(row => row.role === "user");
  const user = rows[index], answer = rows.at(-1);
  if (partial || !user?.text || answer?.role !== "assistant" || index >= rows.length - 1 || !follows(user, answer)) return;
  const id = fingerprint(user.step, user.at, user.text, answer.step, answer.at, answer.text);
  // 每个历史回合最多保留一条回复；工具调用前的中间答复不加入上下文。
  const turns = [];
  let pending, reply;
  for (const row of rows) {
    if (row.role === "user") {
      if (pending && reply) turns.push(pending, reply);
      pending = row.text ? row : undefined;
      reply = undefined;
    } else if (pending) {
      reply = row.role === "assistant" && follows(pending, row) ? row : undefined;
    }
  }
  turns.push(user, answer);
  const confirmationOnly = user.confirmation && !user.media && !user.tools &&
    (rows.slice(0, index).some(row => row.role === "user" && row.text) ||
      (collected.truncated && (user.step ?? 0) > 0 && Boolean(originalGoal(identified))));
  return { user, answer, revision: id, turns: recentTurns(turns.map(({ role, text }) => ({ role, text }))), confirmationOnly };
}

export function snapshot(identified, input = {}) {
  const turn = completedTurn(identified);
  if (!turn) return;
  const currentTitle = readObservedTitle(identified);
  return { agent: "antigravity", sessionId: identified.sessionId, currentTitle,
    titleOrigin: currentTitle ? "unknown" : "auto",
    revision: turn.revision, project: typeof input.cwd === "string" && isAbsolute(input.cwd) ? input.cwd : undefined,
    turns: turn.turns, confirmationOnly: turn.confirmationOnly };
}

export async function waitSnapshot(identified, input, { pause = delay, signal } = {}) {
  const intervals = [150, 350, 750];
  for (const ms of intervals) {
    if (ms) await pause(ms, undefined, { signal });
    const session = identified ?? identify(input);
    if (!session) continue;
    const state = snapshot(session, input);
    if (state) return { identified: session, state };
  }
}
