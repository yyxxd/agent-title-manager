import { spawn } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { ack, bridge, readInput, readRuntime, submitEvent, validCandidate, waitCandidate } from "./client.mjs";
import { diagnostic } from "./diagnostics.mjs";
import { submitNative } from "./orca.mjs";
import { bindOrca } from "./orca-binding.mjs";
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

function bindingOptions(kind, signal) {
  return kind === "acp"
    ? { kind: "chat", signal, inherited: "" }
    : { kind: "terminal", signal, retry: kind === "cli" };
}

export async function processTurn(input, identified, state, rpc, bindingFn = bindOrca, options = {}) {
  const { signal, expectedOrca } = options;
  rpc = diagnosedRPC(rpc);
  const config = bindingOptions(identified.kind, signal);
  const binding = identified.kind === "desktop" ? {} : await bindingFn(state.agent, state.sessionId, config);
  if (identified.kind === "acp" && binding.orcaSessionId !== expectedOrca) return false;
  const fresh = () => completedTurn(identified)?.revision === state.revision && readObservedTitle(identified) === state.currentTitle;
  const submitted = await submitEvent(rpc, { ...state, ...binding, originalGoal: originalGoal(identified) }, { fresh, signal });
  if (!submitted || submitted.accepted === false) return false;
  diagnostic("submit.accepted");
  const result = await waitCandidate(rpc, state, { fresh, signal });
  if (result?.candidate) {
    diagnostic("claim.ready");
    await confirm(input, identified, state, result.candidate, rpc, bindingFn, options);
  } else {
    diagnostic(result?.status === "pending" ? "claim.pending" : "claim.no_candidate");
  }
  return true;
}

async function confirm(input, identified, state, candidate, rpc, bindingFn, { signal, expectedOrca, canWrite } = {}) {
  if (signal?.aborted || !validCandidate(candidate, state)) return;
  if (canWrite && !canWrite()) {
    diagnostic("write.settlement_changed");
    return;
  }
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
  const status = result?.syncStatus;
  if (!result?.accepted) diagnostic("ack.rejected");
  else if (status === "protected") diagnostic("orca.title.protected");
  else if (status === "pending") diagnostic("orca.sync.queued");
  else if (status === "unbound" && identified.kind !== "desktop") {
    const binding = await bindingFn(state.agent, state.sessionId, bindingOptions(identified.kind, signal));
    if (!binding.orcaSessionId || (expectedOrca && binding.orcaSessionId !== expectedOrca)) return;
    const native = snapshot(identified, input);
    if (native?.currentTitle === observed && native.revision === state.revision) {
      await submitNative(rpc, native, binding, signal);
    }
  }
}

export async function handleAntigravity(mode, input, rpc, runBackground = background, binding = bindOrca) {
  if (mode === "stop") {
    const event = input.hookEventName ?? input.hook_event_name;
    const continuation = input.continuationActive ?? input.continuation_active;
    const termination = input.terminationReason ?? input.termination_reason;
    if (event !== "Stop" || continuation === true || ["ERROR", "USER_CANCELED", "USER_CANCELLED"].includes(termination)) return {};
    const ready = await waitSnapshot(identify(input), input);
    if (!ready) diagnostic("snapshot.stop.unavailable");
    else if (ready.identified.kind !== "acp") await processTurn(input, ready.identified, ready.state, rpc, binding);
    return { decision: "" };
  }
  const identified = identify(input);
  if (!identified || identified.kind === "acp") {
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
    if (candidate?.id === input.id && candidate?.revision === input.revision) await confirm(input, identified, state, candidate, rpc, binding);
  } else if (validCandidate(candidate, state)) {
    runBackground("confirm", { ...input, id: candidate.id, title: candidate.title, revision: candidate.revision });
  } else if (mode === "start" && state.currentTitle && identified.kind === "cli") {
    const bound = await binding(state.agent, state.sessionId, bindingOptions(identified.kind));
    if (bound.orcaSessionId) await submitNative(rpc, state, bound);
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
