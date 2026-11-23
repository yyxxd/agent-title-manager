import { spawn } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { ack, bridge, nativeOrigin, readInput, readRuntime, safeID, hostSessionHint, submitEvent, submitNative, waitCandidate } from "./client.mjs";
import { findCodex, withAppServer } from "./app-server.mjs";
import { latestTurn, readThread, stopContext } from "./history.mjs";

async function current(call, snapshot) {
  const thread = await readThread(call, snapshot.sessionId);
  if (!thread || (thread.name ?? "") !== snapshot.currentTitle) return false;
  const turn = await latestTurn(call, thread);
  return turn?.id === snapshot.revision && turn.status === "completed";
}

async function prepare(mode, input, rpc, call, protectionEpoch) {
  const sessionId = input.session_id;
  const thread = await readThread(call, sessionId);
  if (!thread) return;
  const snapshot = {
    agent: "codex", sessionId, revision: input.turn_id ?? "session-start",
    currentTitle: thread.name ?? "", titleOrigin: nativeOrigin(thread.name), project: thread.cwd, hostSessionHint: hostSessionHint(), protectionEpoch,
  };
  if (mode === "start") {
    if (snapshot.currentTitle && (await readThread(call, sessionId))?.name === snapshot.currentTitle) {
      await submitNative(rpc, snapshot);
    }
    const latest = await latestTurn(call, thread);
    if (safeID(latest?.id) && latest.status === "completed") return { ...snapshot, revision: latest.id };
    return;
  }
  const context = await stopContext(call, thread, snapshot.revision);
  if (!context?.turns.length) return;
  const event = { ...snapshot, ...context };
  const result = await submitEvent(rpc, event, {
    fresh: () => current(call, snapshot),
  });
  if (result?.accepted) return event;
}

export async function handleCodex(mode, input, rpc, withNative, polling) {
  if (!safeID(input.session_id) || !["start", "stop"].includes(mode)) return;
  if (mode === "stop" && (!safeID(input.turn_id) || input.stop_hook_active === true)) return;
  const snapshot = await withNative(call => prepare(mode, input, rpc, call, polling?.protectionEpoch));
  if (!snapshot) return;

  let retries = 0;
  const poll = async (method, params, signal) => {
    const reply = await rpc(method, params, signal);
    if (mode === "stop" && reply?.status === "none" && retries < 2) {
      retries++;
      await withNative(call => submitEvent(rpc, snapshot, { signal, fresh: () => current(call, snapshot) }));
    }
    return reply;
  };
  const { candidate } = await waitCandidate(poll, snapshot, { ...polling, resume: mode === "start" });
  if (!candidate) return;
  await withNative(async call => {
    if (!(await current(call, snapshot))) {
      if (Number.isSafeInteger(snapshot.protectionEpoch)) {
        await rpc("title.defer", { agent: snapshot.agent, sessionId: snapshot.sessionId, id: candidate.id });
      }
      return;
    }
    await call("thread/name/set", { threadId: snapshot.sessionId, name: candidate.title });
    const observed = await readThread(call, snapshot.sessionId);
    if (observed?.name === candidate.title) await ack(rpc, snapshot, candidate, observed.name);
  });
}

function launch(mode, payload, registration) {
  const args = registration ? [registration, "observe-" + mode] : ["observe-" + mode];
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), ...args], {
    detached: true, windowsHide: true, stdio: ["pipe", "ignore", "ignore"],
  });
  const report = () => process.stderr.write("标题管理器未能启动 Codex 后台标题处理。\n");
  child.once("error", report);
  child.stdin.on("error", report);
  child.stdin.end(JSON.stringify(payload));
  child.unref();
}

async function main() {
  try {
    const [first, second] = process.argv.slice(2);
    const registration = /^atm-registration-[A-Z2-7]{26}$/.test(first) ? first : "";
    const command = (registration ? second : first) ?? "";
    const worker = command.startsWith("observe-");
    const mode = worker ? command.slice("observe-".length) : command;
    const payload = await readInput();
    const event = mode === "start" ? "SessionStart" : mode === "stop" ? "Stop" : undefined;
    if (!event || payload.hook_event_name !== event || !safeID(payload.session_id) || process.env.ATM_CODEX_ADAPTER ||
        (mode === "stop" && (!safeID(payload.turn_id) || payload.stop_hook_active === true))) return;
    if (!worker) { launch(mode, payload, registration); return; }
    const config = readRuntime(import.meta.url);
    const rpc = bridge(config);
    if (registration) {
      try { await rpc("hook.observed", { registration }); }
      catch { process.stderr.write("标题管理器未能记录 Codex Hook 运行状态。\n"); }
    }
    const eligibility = await rpc("event.eligibility", { agent: "codex", sessionId: payload.session_id });
    if (eligibility?.locked || !Number.isSafeInteger(eligibility?.epoch)) return;
    const binary = findCodex(config.codexPath);
    const withNative = action => withAppServer(binary, action);
    await handleCodex(mode, payload, rpc, withNative, { protectionEpoch: eligibility.epoch });
  } catch {
    process.stderr.write("标题管理器未完成 Codex 标题处理。\n");
  } finally {
    process.stdout.write("{}\n");
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
