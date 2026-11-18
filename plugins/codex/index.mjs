import { spawn } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { ack, bridge, nativeOrigin, readInput, readRuntime, safeID, submitEvent, waitCandidate } from "./client.mjs";
import { bindOrca, submitNative } from "./orca.mjs";
import { findCodex, withAppServer } from "./app-server.mjs";
import { latestTurn, readThread, stopContext } from "./history.mjs";

async function current(call, snapshot) {
  const thread = await readThread(call, snapshot.sessionId);
  if (!thread || (thread.name ?? "") !== snapshot.currentTitle) return false;
  const turn = await latestTurn(call, thread);
  return turn?.id === snapshot.revision && turn.status === "completed";
}

async function prepare(mode, input, rpc, call, resolveBinding) {
  const sessionId = input.session_id;
  const thread = await readThread(call, sessionId);
  if (!thread) return;
  const snapshot = {
    agent: "codex", sessionId, revision: input.turn_id ?? "session-start",
    currentTitle: thread.name ?? "", titleOrigin: nativeOrigin(thread.name), project: thread.cwd,
  };
  if (mode === "start") {
    if (snapshot.currentTitle) {
      const binding = await resolveBinding(snapshot.agent, sessionId);
      if (binding.orcaSessionId && (await readThread(call, sessionId))?.name === snapshot.currentTitle) {
        await submitNative(rpc, snapshot, binding);
      }
    }
    const latest = await latestTurn(call, thread);
    if (safeID(latest?.id) && latest.status === "completed") return { ...snapshot, revision: latest.id };
    return;
  }
  const context = await stopContext(call, thread, snapshot.revision);
  if (!context?.turns.length) return;
  const binding = await resolveBinding(snapshot.agent, sessionId);
  const result = await submitEvent(rpc, { ...snapshot, ...context, ...binding }, {
    fresh: () => current(call, snapshot),
  });
  if (result?.accepted) return snapshot;
}

export async function handleCodex(mode, input, rpc, withNative, resolveBinding = bindOrca, polling) {
  if (!safeID(input.session_id) || !["start", "stop"].includes(mode)) return;
  if (mode === "stop" && (!safeID(input.turn_id) || input.stop_hook_active === true)) return;
  const snapshot = await withNative(call => prepare(mode, input, rpc, call, resolveBinding));
  if (!snapshot) return;

  const { candidate } = await waitCandidate(rpc, snapshot, { ...polling, resume: mode === "start" });
  if (!candidate) return;
  await withNative(async call => {
    if (!(await current(call, snapshot))) return;
    await call("thread/name/set", { threadId: snapshot.sessionId, name: candidate.title });
    const observed = await readThread(call, snapshot.sessionId);
    if (observed?.name === candidate.title) await ack(rpc, snapshot, candidate, observed.name);
  });
}

function launch(mode, payload) {
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), "observe-" + mode], {
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
    const command = process.argv[2] ?? "";
    const worker = command.startsWith("observe-");
    const mode = worker ? command.slice("observe-".length) : command;
    const payload = await readInput();
    const event = mode === "start" ? "SessionStart" : mode === "stop" ? "Stop" : undefined;
    if (!event || payload.hook_event_name !== event || !safeID(payload.session_id) || process.env.ATM_CODEX_ADAPTER ||
        (mode === "stop" && (!safeID(payload.turn_id) || payload.stop_hook_active === true))) return;
    if (!worker) { launch(mode, payload); return; }
    const config = readRuntime(import.meta.url);
    const binary = findCodex(config.codexPath);
    const withNative = action => withAppServer(binary, action);
    await handleCodex(mode, payload, bridge(config), withNative);
  } catch {
    process.stderr.write("标题管理器未完成 Codex 标题处理。\n");
  } finally {
    process.stdout.write("{}\n");
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
