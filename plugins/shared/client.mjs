import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { isAbsolute } from "node:path";
import { promisify } from "node:util";
import { setTimeout as delay } from "node:timers/promises";

const exec = promisify(execFile);
const terminalStatuses = new Set(["kept", "failed", "interrupted", "stale", "applied", "disabled"]);
const confirmationTexts = new Set(["好", "好的", "可以", "收到", "谢谢", "继续", "ok", "okay", "thanks", "thank you", "continue"]);
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
    if (reply?.ok !== true) {
      const error = new Error("标题管理器拒绝了请求");
      if (reply?.error === "ingress_busy" || (typeof reply?.error === "string" && reply.error.startsWith("ingress_busy:"))) {
        error.code = "ingress_busy";
      }
      throw error;
    }
    return reply.result;
  };
}

export async function submitEvent(rpc, snapshot, { signal, fresh = () => true, pause = delay } = {}) {
  const intervals = [250, 750, 2000];
  for (let attempt = 0; ; attempt++) {
    if (signal?.aborted || !(await fresh())) return;
    try { return await rpc("event.submit", snapshot, signal); }
    catch (error) {
      if (error.code !== "ingress_busy" || attempt >= intervals.length) throw error;
      await pause(intervals[attempt], undefined, { signal });
    }
  }
}

// 宿主提示只作原样透传，绑定由桌面服务验证。
export function hostSessionHint() {
  return process.env.ORCA_AGENT_SESSION_ID || undefined;
}

export async function submitNative(rpc, snapshot, signal) {
  if (!snapshot.currentTitle || signal?.aborted) return;
  const { turns, originalGoal, confirmationOnly, ...native } = snapshot;
  return submitEvent(rpc, {
    ...native, nativeTitleOnly: true, turns: [],
    revision: fingerprint("native-title", native.revision, native.currentTitle),
  }, { signal });
}

export function textOf(content) {
  const flatten = value => Array.isArray(value)
    ? value.filter(part => part?.type === "text" && typeof part.text === "string").map(part => part.text).join("\n")
    : typeof value === "string" ? value : "";
  return Array.from(flatten(content).trim()).slice(0, 1500).join("");
}

export function isTextConfirmation(content) {
  if (Array.isArray(content)) {
    if (!content.length || content.some(part => part?.type !== "text" || typeof part.text !== "string" ||
        ["text_elements", "textElements"].some(key =>
          key in part && (!Array.isArray(part[key]) || part[key].length > 0)))) return false;
    content = content.map(part => part.text).join("\n");
  }
  if (typeof content !== "string" || content.length > 64) return false;
  return confirmationTexts.has(content.trim().replace(/[.!。！]+$/, "").trimEnd().toLowerCase());
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

export async function waitCandidate(rpc, snapshot, options = {}) {
  const { fresh = () => true, signal, resume = false, now = Date.now, pause = delay } = options;
  const { agent, sessionId, revision, currentTitle } = snapshot;
  const started = now();
  const deadline = started + 8 * 60_000;
  let interval = 1000;
  while (!signal?.aborted && fresh()) {
    const result = await rpc("title.claim", { agent, sessionId, revision, currentTitle }, signal);
    const status = result?.status;
    if (validCandidate(result?.candidate, snapshot)) return { candidate: result.candidate, status };
    if (result?.candidate) return { status: "stale" };
    if (terminalStatuses.has(status)) return { status };
    const elapsed = now();
    if (resume && (status === "none" || !status)) return { status };
    if (elapsed >= deadline) return { status: "pending" };
    await pause(Math.min(interval, deadline - elapsed), undefined, { signal });
    interval = Math.min(interval * 2, 20_000);
  }
  return { status: "stale" };
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
