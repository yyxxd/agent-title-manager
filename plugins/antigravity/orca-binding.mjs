import { execFile } from "node:child_process";
import { isAbsolute, join } from "node:path";
import { existsSync } from "node:fs";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";
import { safeID } from "./paths.mjs";
import { diagnostic } from "./diagnostics.mjs";
import { resolveRawID } from "./orca.mjs";

const exec = promisify(execFile);
const prefix = "orca_session_id:";

async function show(id, signal, timeout) {
  const { ORCA_PAIRING_CODE, ORCA_ENVIRONMENT, ORCA_REMOTE_PAIRING, ...env } = process.env;
  const hint = env.ORCA_CLI_COMMAND;
  const installed = [env.LOCALAPPDATA && join(env.LOCALAPPDATA, "Programs"), env.ProgramFiles]
    .filter(dir => dir && isAbsolute(dir)).map(dir => join(dir, "orca", "resources", "bin", "orca.exe"));
  const binary = hint && isAbsolute(hint) && (process.platform !== "win32" || /\.exe$/i.test(hint))
    ? hint : process.platform === "win32" ? installed.find(existsSync) ?? "orca.exe" : "orca";
  const result = await exec(binary, ["agent", "show", id, "--environment", "local", "--json"], {
    env, timeout, signal, windowsHide: true, maxBuffer: 64 * 1024,
  }).catch(error => {
    if (!error.stdout) {
      if (error.code === "ENOENT") return { missing: true };
      diagnostic("orca.lookup.unavailable", error);
      return { stdout: "{}" };
    }
    return error;
  });
  if (result.missing) return result;
  try { return JSON.parse(result.stdout); }
  catch (error) { diagnostic("orca.lookup.invalid", error); return {}; }
}

export async function bindOrca(agent, sessionId, options = {}) {
  const { signal, lookup = show, inherited = process.env.ORCA_AGENT_SESSION_ID, locator = sessionId,
    kind, retry = false, pause = delay } = options;
  if (!safeID(sessionId) || signal?.aborted) return {};
  if (agent !== "antigravity" || locator !== sessionId) return {};
  for (const ms of retry ? [0, 250, 750, 1500] : [0]) {
    if (ms) await pause(ms, undefined, { signal });
    const timeout = ms ? 1500 : 900;
    const mapping = await lookup(locator, signal, timeout);
    if (mapping?.missing) return {};
    const id = resolveRawID(mapping);
    if (!id) continue;
    if (inherited && inherited.replace(/^orca_session_id:/, "") !== id) return {};
    const reply = await lookup(prefix + id, signal, timeout);
    const result = reply?.result;
    if (reply?.ok !== true || result?.orcaSessionId !== id || !Array.isArray(result.places) || result.places.length !== 1) continue;
    const [place] = result.places;
    if (place?.agent !== agent || place.orcaSessionId !== id || place.hostId !== "local" ||
        !["chat", "terminal"].includes(place.kind) || (kind && place.kind !== kind) ||
        (place.title !== null && typeof place.title !== "string")) return {};
    return { orcaSessionId: id, orcaTitle: place.title ?? "", orcaTitleOrigin: "unknown", orcaNativeHandle: locator };
  }
  if (inherited || kind) diagnostic("orca.binding.unavailable");
  return {};
}
