import { ack, bridge, fingerprint, nativeOrigin, readRuntime, recentTurns, safeID, textOf, waitCandidate } from "./client.mjs";
import { bindOrca, submitNative } from "./orca.mjs";

function snapshot(pi, ctx) {
  const sessionId = ctx.sessionManager.getSessionId();
  const leaf = ctx.sessionManager.getLeafId();
  if (!safeID(sessionId)) return undefined;
  const currentTitle = pi.getSessionName() ?? "";
  const messages = ctx.sessionManager.getBranch()
    .filter(entry => entry.type === "message" && ["user", "assistant"].includes(entry.message.role))
    .slice(-16)
    .map(({ message }) => ({ role: message.role, text: textOf(message.content) }));
  const turns = recentTurns(messages);
  return {
    agent: "pi", sessionId, revision: fingerprint(leaf, turns), currentTitle,
    titleOrigin: nativeOrigin(currentTitle), project: ctx.cwd, turns,
  };
}

async function syncExisting({ pi, rpc, resolveBinding, ctx, signal }) {
  const state = snapshot(pi, ctx);
  if (!state?.currentTitle) return;
  const binding = await resolveBinding(state.agent, state.sessionId, { signal, locator: ctx.sessionManager.getSessionFile() });
  if (!signal.aborted && ctx.sessionManager.getSessionId() === state.sessionId && pi.getSessionName() === state.currentTitle) {
    await submitNative(rpc, state, binding, signal);
  }
}

async function applySettled({ pi, rpc, resolveBinding, ctx, signal, setName }) {
  const state = snapshot(pi, ctx);
  if (!state?.turns.length) return;
  const leaf = ctx.sessionManager.getLeafId();
  const fresh = () => !signal.aborted && ctx.isIdle() && ctx.sessionManager.getSessionId() === state.sessionId &&
    ctx.sessionManager.getLeafId() === leaf && (pi.getSessionName() ?? "") === state.currentTitle;
  const binding = await resolveBinding(state.agent, state.sessionId, { signal, locator: ctx.sessionManager.getSessionFile() });
  if (!fresh() || !(await rpc("event.submit", { ...state, ...binding }, signal))?.accepted) return;
  const candidate = await waitCandidate(rpc, state, fresh, signal);
  if (!candidate || !fresh()) return;
  setName(candidate.title);
  const observed = ctx.sessionManager.getSessionName();
  if (ctx.sessionManager.getSessionId() !== state.sessionId || observed !== candidate.title) return;
  ctx.ui.notify("会话已命名为 " + observed, "info");
  await ack(rpc, state, candidate, observed);
}

export function register(pi, rpc, resolveBinding = bindOrca) {
  let active;
  let writing;
  const cancel = () => { active?.abort(); active = undefined; };
  const setName = name => {
    writing = name;
    try { pi.setSessionName(name); } finally { writing = undefined; }
  };
  const run = (job, ctx) => {
    cancel();
    const controller = new AbortController();
    active = controller;
    const signal = controller.signal;
    void job({ pi, rpc, resolveBinding, ctx, signal, setName }).catch(() => {
      if (!signal.aborted) ctx.ui.notify("标题管理器未完成会话标题处理。", "warning");
    }).finally(() => {
      if (active === controller) active = undefined;
    });
  };
  pi.on("session_start", (_event, ctx) => run(syncExisting, ctx));
  for (const name of ["session_shutdown", "session_before_switch", "session_before_fork", "session_before_tree", "before_agent_start"]) {
    pi.on(name, cancel);
  }
  pi.on("session_info_changed", event => { if (event.name !== writing) cancel(); });
  pi.on("agent_settled", (event, ctx) => {
    if (!event.aborted && ctx.isIdle()) run(applySettled, ctx);
  });
}

export default function (pi) {
  try { register(pi, bridge(readRuntime(import.meta.url))); }
  catch {
    pi.on("session_start", (_event, ctx) => ctx.ui.notify("标题管理器缺少桥接程序配置。", "warning"));
  }
}
