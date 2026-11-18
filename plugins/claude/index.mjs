import { spawn } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { ack, bridge, readInput, readRuntime, textOf, validCandidate } from "./client.mjs";
import { bindOrca, submitNative } from "./orca.mjs";
import { identify, snapshot } from "./transcript.mjs";

const START_SOURCES = new Set(["startup", "resume", "fork"]);

function background(mode, payload) {
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), mode], {
    detached: true, windowsHide: true, stdio: ["pipe", "ignore", "ignore"],
  });
  const report = () => process.stderr.write("标题管理器未能启动 Claude 后台标题处理。\n");
  child.once("error", report);
  child.stdin.on("error", report);
  child.stdin.end(JSON.stringify(payload));
  child.unref();
}

async function submitStop(input, identified, rpc, binding) {
  const finishing = textOf(input.last_assistant_message);
  const deadline = Date.now() + 1000;
  do {
    const state = snapshot(identified, input);
    if (state?.revision && (!finishing || state.turns.at(-1)?.text === finishing)) {
      await rpc("event.submit", { ...state, ...await binding(state.agent, state.sessionId) });
      return;
    }
    if (Date.now() >= deadline) return;
    await delay(120);
  } while (true);
}

async function confirm(input, identified, rpc) {
  if (typeof input.id !== "string" || typeof input.title !== "string") return;
  const deadline = Date.now() + 12_000;
  do {
    const observed = snapshot(identified);
    if (observed?.currentTitle === input.title) {
      try { await ack(rpc, observed, input, observed.currentTitle); return; }
      catch (error) { if (Date.now() >= deadline) throw error; }
    } else if (observed?.currentTitle && observed.currentTitle !== input.baseTitle) {
      return;
    }
    if (Date.now() >= deadline) return;
    await delay(250);
  } while (true);
}

export async function handleClaude(mode, input, rpc, runBackground = background, binding = bindOrca, configDir) {
  const identified = identify(input, configDir);
  if (!identified) return {};
  if (mode === "confirm") {
    await confirm(input, identified, rpc);
    return {};
  }
  if (mode === "native") {
    const native = snapshot(identified, input);
    if (native?.currentTitle) {
      const orca = await binding(native.agent, native.sessionId);
      if (snapshot(identified, input)?.currentTitle === native.currentTitle) {
        await submitNative(rpc, { ...native, revision: native.revision ?? "session-start" }, orca);
      }
    }
    return {};
  }
  if (mode === "stop" && input.hook_event_name === "Stop" && !input.stop_hook_active) {
    await submitStop(input, identified, rpc, binding);
    return {};
  }

  const start = mode === "start" && input.hook_event_name === "SessionStart" && START_SOURCES.has(input.source);
  const prompt = mode === "prompt" && input.hook_event_name === "UserPromptSubmit";
  if (!start && !prompt) return {};
  const state = snapshot(identified, input);
  if (start && state?.currentTitle) {
    runBackground("native", input);
    return {};
  }
  if (!state?.revision) return {};
  const { agent, sessionId, revision, currentTitle } = state;
  const { candidate } = (await rpc("title.claim", { agent, sessionId, revision, currentTitle })) ?? {};
  if (!validCandidate(candidate, state)) return {};
  const latest = snapshot(identified, input);
  if (latest?.currentTitle !== currentTitle || latest?.revision !== revision) return {};
  runBackground("confirm", { session_id: sessionId, transcript_path: identified.path,
    id: candidate.id, title: candidate.title, baseTitle: currentTitle });
  return { hookSpecificOutput: { hookEventName: start ? "SessionStart" : "UserPromptSubmit", sessionTitle: candidate.title } };
}

async function main() {
  let output = {};
  try {
    const input = await readInput();
    const rpc = bridge(readRuntime(import.meta.url), 750);
    output = await handleClaude(process.argv[2], input, rpc);
  } catch {
    process.stderr.write("标题管理器未完成 Claude 标题处理。\n");
  }
  process.stdout.write(JSON.stringify(output) + "\n");
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
