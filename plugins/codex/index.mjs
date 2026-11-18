import { pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { ack, bridge, nativeOrigin, readInput, readRuntime, recentTurns, safeID, textOf, waitCandidate } from "./client.mjs";
import { bindOrca, submitNative } from "./orca.mjs";
import { findCodex, withAppServer } from "./app-server.mjs";

function eventTurns(turns) {
  const messages = [];
  for (const turn of turns.slice(-5)) {
    if (turn.status !== "completed") continue;
    for (const item of turn.items ?? []) {
      if (item.type === "userMessage") messages.push({ role: "user", text: textOf(item.content) });
      else if (item.type === "agentMessage" && (item.phase == null || item.phase === "final_answer")) {
        messages.push({ role: "assistant", text: textOf(item.text) });
      }
    }
  }
  return recentTurns(messages);
}

async function waitSettled(read, turnID) {
  const deadline = Date.now() + 3300;
  do {
    const thread = await read();
    const last = thread?.turns?.at(-1);
    if (last?.id !== turnID) return undefined;
    if (last.status === "completed") return thread;
    if (last.status !== "inProgress" || Date.now() >= deadline) return undefined;
    await delay(125);
  } while (true);
}

export async function handleCodex(mode, input, rpc, call, resolveBinding = bindOrca) {
  const sessionId = input.session_id;
  const revision = input.turn_id;
  if (!safeID(sessionId) || !["start", "stop"].includes(mode)) return;
  if (mode === "stop" && (!safeID(revision) || input.stop_hook_active === true)) return;
  const read = async () => {
    const thread = (await call("thread/read", { threadId: sessionId, includeTurns: mode === "stop" }))?.thread;
    return thread?.id === sessionId ? thread : undefined;
  };
  const thread = mode === "start" ? await read() : await waitSettled(read, revision);
  if (!thread) return;
  if (mode === "start" && !thread.name) return;
  const snapshot = {
    agent: "codex", sessionId, revision: revision ?? "session-start",
    currentTitle: thread.name ?? "", titleOrigin: nativeOrigin(thread.name), project: thread.cwd,
    turns: mode === "stop" ? eventTurns(thread.turns) : [],
  };
  const binding = await resolveBinding(snapshot.agent, sessionId);
  if (mode === "start") {
    if (binding.orcaSessionId && (await read())?.name === snapshot.currentTitle) await submitNative(rpc, snapshot, binding);
    return;
  }
  if (!snapshot.turns.length) return;
  if (!(await rpc("event.submit", { ...snapshot, ...binding }))?.accepted) return;

  async function fresh() {
    const current = await read();
    const last = current?.turns?.at(-1);
    return last?.id === revision && last.status === "completed" && (current.name ?? "") === snapshot.currentTitle;
  }
  const candidate = await waitCandidate(rpc, snapshot, fresh);
  if (!candidate || !(await fresh())) return;
  await call("thread/name/set", { threadId: sessionId, name: candidate.title });
  const observed = await read();
  if (observed?.name === candidate.title) await ack(rpc, snapshot, candidate, observed.name);
}

async function main() {
  try {
    const mode = process.argv[2];
    const payload = await readInput();
    const event = mode === "start" ? "SessionStart" : mode === "stop" ? "Stop" : undefined;
    if (!event || payload.hook_event_name !== event || !safeID(payload.session_id) || process.env.ATM_CODEX_ADAPTER ||
        (mode === "stop" && (!safeID(payload.turn_id) || payload.stop_hook_active === true))) return;
    const config = readRuntime(import.meta.url);
    const rpc = bridge(config);
    await withAppServer(findCodex(config.codexPath), call => handleCodex(mode, payload, rpc, call));
  } catch {
    process.stderr.write("标题管理器未完成 Codex 标题处理。\n");
  } finally {
    process.stdout.write("{}\n");
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
