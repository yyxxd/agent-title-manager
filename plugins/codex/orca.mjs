import { execFile } from "node:child_process";
import { isAbsolute } from "node:path";
import { promisify } from "node:util";
import { fingerprint, safeID, submitEvent } from "./client.mjs";

const exec = promisify(execFile);
const prefix = "orca_session_id:";
const rawPattern = /^[A-Za-z0-9][A-Za-z0-9_-]{0,179}$/;

async function show(id, signal) {
  const { ORCA_PAIRING_CODE, ORCA_ENVIRONMENT, ORCA_REMOTE_PAIRING, ...env } = process.env;
  const hint = env.ORCA_CLI_COMMAND;
  const binary = hint && isAbsolute(hint) && (process.platform !== "win32" || /\.exe$/i.test(hint))
    ? hint : process.platform === "win32" ? "orca.exe" : "orca";
  const result = await exec(binary, ["agent", "show", id, "--json"], {
    env, timeout: 900, signal, windowsHide: true, maxBuffer: 64 * 1024,
  }).catch(error => {
    if (!error.stdout) return { stdout: "{}" };
    return error;
  });
  try { return JSON.parse(result.stdout); } catch { return {}; }
}

export function resolveRawID(reply) {
  if (reply?.ok !== false || reply.error?.code !== "agent_provider_id") return undefined;
  const message = reply.error.message;
  if (typeof message !== "string") return undefined;
  const raw = message.match(/ Address it as orca_session_id:([A-Za-z0-9_-]+)\.$/)?.[1];
  return raw && rawPattern.test(raw) ? raw : undefined;
}

export async function bindOrca(agent, sessionId, options = {}) {
  const { signal, lookup = show, inherited = process.env.ORCA_AGENT_SESSION_ID, locator = sessionId } = options;
  if (!safeID(sessionId) || signal?.aborted) return {};
  if (locator !== sessionId && (agent !== "pi" || typeof locator !== "string" || !isAbsolute(locator) || !/\.jsonl$/i.test(locator))) return {};
  const id = resolveRawID(await lookup(locator, signal));
  if (!id || (inherited && inherited.replace(/^orca_session_id:/, "") !== id)) return {};
  const reply = await lookup(prefix + id, signal);
  const result = reply?.result;
  if (reply?.ok !== true || result?.orcaSessionId !== id || !Array.isArray(result.places) || result.places.length !== 1) return {};
  const [place] = result.places;
  if (place?.agent !== agent || place.orcaSessionId !== id || (place.title !== null && typeof place.title !== "string")) return {};
  const terminal = place.kind === "terminal";
  if ((agent === "codex" || agent === "claude") && !(place.kind === undefined || place.kind === "chat")) return {};
  if ((agent === "pi" || agent === "antigravity") && !(place.kind === undefined || place.kind === "chat" || terminal)) return {};
  return { orcaSessionId: id, orcaTitle: place.title ?? "", orcaTitleOrigin: "unknown", orcaNativeHandle: locator };
}

export async function submitNative(rpc, snapshot, binding, signal) {
  if (!snapshot.currentTitle) return;
  if (!binding.orcaSessionId || signal?.aborted) return;
  const { turns, originalGoal, confirmationOnly, ...native } = snapshot;
  return submitEvent(rpc, {
    ...native, ...binding, nativeTitleOnly: true, turns: [],
    revision: fingerprint("native-title", native.revision, native.currentTitle, binding.orcaSessionId, binding.orcaTitle),
  }, { signal });
}
