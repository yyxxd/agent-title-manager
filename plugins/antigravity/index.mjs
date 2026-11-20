import { spawn } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { ack, bridge, readInput, readRuntime, hostSessionHint, submitEvent, submitNative, validCandidate, waitCandidate } from "./client.mjs";
import { diagnostic } from "./diagnostics.mjs";
import { completedTurn, identify, originalGoal, readObservedTitle, snapshot, waitSnapshot } from "./transcript.mjs";
import { writeTitle } from "./annotations.mjs";

function background(mode, payload) {
  const env = { ...process.env, ATM_ANTIGRAVITY_WORKER: "1" };
  delete env.NODE_OPTIONS;
  delete env.NODE_PATH;
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), mode], {
    detached: true, windowsHide: true, stdio: ["pipe", "ignore", "ignore"], env,
  });
  child.once("error", error => diagnostic("worker.spawn.failed", error));
  child.stdin.on("error", error => diagnostic("worker.payload.failed", error));
  child.stdin.end(JSON.stringify(payload));
  child.unref();
}

function diagnosedRPC(rpc) {
  const stages = { "event.submit": "submit", "title.claim": "claim", "title.ack": "ack" };
  return async (method, params, signal) => {
    const stage = stages[method];
    try { return await rpc(method, params, signal); }
    catch (error) {
      if (!signal?.aborted) diagnostic(stage + ".failed", error);
      throw error;
    }
  };
}

export async function processTurn(input, identified, state, rpc, options = {}) {
  const { signal } = options;
  rpc = diagnosedRPC(rpc);
  const fresh = () => completedTurn(identified)?.revision === state.revision && readObservedTitle(identified) === state.currentTitle;
  const submitted = await submitEvent(rpc, { ...state, originalGoal: originalGoal(identified) }, { fresh, signal });
  if (!submitted || submitted.accepted === false) return false;
  diagnostic("submit.accepted");
  const result = await waitCandidate(rpc, state, { fresh, signal });
  if (result?.candidate) {
    diagnostic("claim.ready");
    await confirm(input, identified, state, result.candidate, rpc, options);
  } else {
    diagnostic(result?.status === "pending" ? "claim.pending" : "claim.no_candidate");
  }
  return true;
}

async function confirm(input, identified, state, candidate, rpc, { signal, canWrite } = {}) {
  if (signal?.aborted || !validCandidate(candidate, state)) return;
  if (canWrite && !await canWrite()) {
    diagnostic("write.settlement_changed");
    return;
  }
  if (signal?.aborted) return;
  const latest = snapshot(identified, input);
  if (latest?.revision !== state.revision || latest.currentTitle !== state.currentTitle) {
    diagnostic("readback.stale");
    return;
  }
  if (!writeTitle(identified.appDataDir, identified.sessionId, candidate.title, state.currentTitle)) {
    diagnostic("write.unconfirmed");
    return;
  }
  const observed = readObservedTitle(identified);
  if (observed !== candidate.title) {
    diagnostic("readback.mismatch");
    return;
  }
  diagnostic("readback.confirmed");
  const result = await ack(rpc, state, candidate, observed);
  if (!result?.accepted) diagnostic("ack.rejected");
}

function hookSnapshot(identified, state) {
  return identified.kind === "cli" ? {
    ...state, nativeHandle: state.sessionId, hostSessionHint: hostSessionHint(),
  } : state;
}

export async function handleAntigravity(mode, input, rpc, runBackground = background) {
  if (mode === "stop") {
    const event = input.hookEventName ?? input.hook_event_name;
    const continuation = input.continuationActive ?? input.continuation_active;
    const termination = input.terminationReason ?? input.termination_reason;
    if (event !== "Stop" || continuation === true || ["ERROR", "USER_CANCELED", "USER_CANCELLED"].includes(termination)) return {};
    const ready = await waitSnapshot(identify(input), input);
    if (!ready) diagnostic("snapshot.stop.unavailable");
    else await processTurn(input, ready.identified, hookSnapshot(ready.identified, ready.state), rpc);
    return { decision: "" };
  }
  const identified = identify(input);
  if (!identified) {
    diagnostic("snapshot.path.unavailable");
    return {};
  }
  if (!["start", "prompt", "confirm"].includes(mode)) return {};
  const state = snapshot(identified, input);
  if (!state?.revision) return {};
  rpc = diagnosedRPC(rpc);
  const result = await rpc("title.claim", { agent: state.agent, sessionId: state.sessionId, revision: state.revision, currentTitle: state.currentTitle });
  const candidate = result?.candidate;
  if (mode === "confirm") {
    if (candidate?.id === input.id && candidate?.revision === input.revision) await confirm(input, identified, state, candidate, rpc);
  } else if (validCandidate(candidate, state)) {
    runBackground("confirm", { ...input, id: candidate.id, title: candidate.title, revision: candidate.revision });
  } else if (mode === "start" && state.currentTitle && identified.kind === "cli") {
    await submitNative(rpc, hookSnapshot(identified, state));
  }
  return {};
}

async function main() {
  const mode = process.argv[2];
  const fallback = mode === "stop" ? { decision: "" } : {};
  let stage = "payload";
  try {
    const input = await readInput();
    if (process.env.ATM_ANTIGRAVITY_WORKER !== "1") {
      diagnostic("hook.received");
      stage = "worker";
      background(mode, input);
    } else {
      stage = "worker";
      await handleAntigravity(mode, input, bridge(readRuntime(import.meta.url), 1000));
      diagnostic("worker.completed");
    }
  } catch (error) {
    diagnostic(stage + ".failed", error);
  }
  process.stdout.write(JSON.stringify(fallback) + "\n");
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
