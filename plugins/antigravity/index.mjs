import { spawn } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { ack, bridge, readInput, readRuntime, submitEvent, validCandidate, waitCandidate } from "./client.mjs";
import { bindOrca, submitNative } from "./orca.mjs";
import { identify, originalGoal, readObservedTitle, snapshot } from "./transcript.mjs";
import { writeTitle } from "./annotations.mjs";

function background(mode, payload) {
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), mode], { detached: true, windowsHide: true, stdio: ["pipe", "ignore", "ignore"], env: { ...process.env, ATM_ANTIGRAVITY_WORKER: "1" } });
  child.once("error", () => {}); child.stdin.on("error", () => {}); child.stdin.end(JSON.stringify(payload)); child.unref();
}
async function submitStop(input, identified, rpc) {
  const state = snapshot(identified, input); if (!state?.revision) return;
  const binding = await bindOrca(state.agent, state.sessionId, { locator: identified.sessionId });
  await submitEvent(rpc, { ...state, ...binding, originalGoal: originalGoal(identified) }, { fresh: () => snapshot(identified, input)?.revision === state.revision });
  const result = await waitCandidate(rpc, state, { fresh: () => snapshot(identified, input)?.revision === state.revision });
  if (result?.candidate) await confirm({ ...input, id: result.candidate.id, title: result.candidate.title, baseTitle: state.currentTitle, revision: state.revision }, identified, rpc);
}
async function confirm(input, identified, rpc) {
  if (typeof input.id !== "string" || typeof input.title !== "string") return;
  const current = readObservedTitle(identified);
  if (current !== input.baseTitle || input.revision !== snapshot(identified, input)?.revision) return;
  if (!writeTitle(identified.appDataDir, identified.sessionId, input.title)) return;
  const observed = readObservedTitle(identified); if (observed !== input.title) return;
  const result = await ack(rpc, { agent: "antigravity", sessionId: identified.sessionId }, input, observed);
  if (result?.syncStatus === "unbound") {
    const native = snapshot(identified, input), binding = await bindOrca("antigravity", identified.sessionId, { locator: identified.sessionId });
    if (native?.currentTitle === observed && binding.orcaSessionId) await submitNative(rpc, { ...native, revision: native.revision ?? "session-confirm" }, binding);
  }
}
export async function handleAntigravity(mode, input, rpc, runBackground = background, binding = bindOrca) {
  const identified = identify(input); if (!identified) return {};
  if (mode === "confirm") { await confirm(input, identified, rpc); return {}; }
  const event = input.hookEventName ?? input.hook_event_name;
  const reason = input.terminationReason;
  if (mode === "stop" && event === "Stop" && input.continuationActive !== true && reason !== "ERROR" && reason !== "USER_CANCELED") { await submitStop(input, identified, rpc); return { decision: "" }; }
  if (mode !== "start" && mode !== "prompt") return {};
  const state = snapshot(identified, input); if (!state?.revision) return {};
  const result = await rpc("title.claim", { agent: "antigravity", sessionId: state.sessionId, revision: state.revision, currentTitle: state.currentTitle });
  if (!validCandidate(result?.candidate, state)) {
    if (mode === "start" && state.currentTitle) {
      const b = await binding(state.agent, state.sessionId, { locator: identified.sessionId });
      if (b.orcaSessionId) await submitNative(rpc, state, b);
    }
    return {};
  }
  const latest = snapshot(identified, input); if (!latest || latest.revision !== state.revision || latest.currentTitle !== state.currentTitle) return {};
  runBackground("confirm", { ...input, conversationId: identified.sessionId, transcriptPath: identified.path, id: result.candidate.id, title: result.candidate.title, baseTitle: state.currentTitle, revision: state.revision });
  return {};
}
async function main() {
  const mode = process.argv[2];
  const fallback = mode === "stop" ? { decision: "" } : {};
  try {
    const input = await readInput();
    const rpc = bridge(readRuntime(import.meta.url), 750);
    if (process.env.ATM_ANTIGRAVITY_WORKER === "1") { await handleAntigravity(mode, input, rpc); return process.stdout.write(JSON.stringify(fallback) + "\n"); }
    background(mode, input);
  } catch { /* Hook 必须快速成功 */ }
  process.stdout.write(JSON.stringify(fallback) + "\n");
}
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
