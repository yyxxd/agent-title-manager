import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { isAbsolute } from "node:path";
import { promisify } from "node:util";
import { setTimeout as delay } from "node:timers/promises";

const exec = promisify(execFile);
export const safeID = value => typeof value === "string" && /^[A-Za-z0-9_-]{1,120}$/.test(value);
export const fingerprint = (...values) => createHash("sha256").update(JSON.stringify(values)).digest("hex");
export const nativeOrigin = title => title ? "unknown" : "auto";

export function readRuntime(entry) {
  const config = JSON.parse(readFileSync(new URL("./atm-runtime.json", entry), "utf8"));
  if (typeof config?.bridgePath !== "string" || !isAbsolute(config.bridgePath)) {
    throw new Error("桥接程序需要绝对路径");
  }
  return config;
}

export async function readInput(stream = process.stdin) {
  const chunks = [];
  let size = 0;
  for await (const chunk of stream) {
    size += chunk.length;
    if (size > 1024 * 1024) throw new Error("Hook 输入超出限制");
    chunks.push(chunk);
  }
  const payload = JSON.parse(Buffer.concat(chunks).toString("utf8"));
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) throw new Error("Hook 输入格式无效");
  return payload;
}

export function bridge({ bridgePath }, timeout = 1000) {
  return async (method, params, signal) => {
    const request = JSON.stringify({ v: 1, method, params });
    const result = exec(bridgePath, ["rpc"], { timeout, signal, windowsHide: true, maxBuffer: 64 * 1024 });
    result.child.stdin.on("error", () => {}); // 进程先行退出时由 execFile 报告。
    result.child.stdin.end(request);
    const { stdout } = await result;
    const reply = JSON.parse(stdout);
    if (reply?.ok !== true) throw new Error("标题管理器拒绝了请求");
    return reply.result;
  };
}

export function textOf(content) {
  const flatten = value => Array.isArray(value)
    ? value.filter(part => part?.type === "text" && typeof part.text === "string").map(part => part.text).join("\n")
    : typeof value === "string" ? value : "";
  return Array.from(flatten(content).trim()).slice(0, 1500).join("");
}

export function recentTurns(turns) {
  const recent = turns.filter(turn => turn.text).slice(-8);
  return recent.some(turn => turn.role === "user") ? recent : [];
}

export function validCandidate(candidate, snapshot) {
  return typeof candidate?.id === "string" && candidate.id.length > 0 &&
    typeof candidate.title === "string" && Boolean(candidate.title.trim()) &&
    candidate.title.length <= 256 && !/[\u0000-\u001f\u007f]/.test(candidate.title) &&
    candidate.title !== snapshot.currentTitle && candidate.baseTitle === snapshot.currentTitle &&
    candidate.revision === snapshot.revision;
}

export async function waitCandidate(rpc, snapshot, fresh, signal) {
  const { agent, sessionId, revision, currentTitle } = snapshot;
  const deadline = Date.now() + 65_000;
  do {
    if (signal?.aborted || !(await fresh())) return null;
    const result = await rpc("title.claim", { agent, sessionId, revision, currentTitle }, signal);
    if (validCandidate(result?.candidate, snapshot)) return result.candidate;
    if (result?.candidate) return null;
    if (Date.now() >= deadline) return null;
    await delay(1250, undefined, { signal });
  } while (true);
}

export async function ack(rpc, snapshot, candidate, observedTitle) {
  const params = { agent: snapshot.agent, sessionId: snapshot.sessionId, id: candidate.id, observedTitle };
  for (let attempt = 1; ; attempt++) {
    try { return await rpc("title.ack", params); }
    catch (error) {
      if (attempt === 3) throw error;
      await delay(200 * attempt);
    }
  }
}
