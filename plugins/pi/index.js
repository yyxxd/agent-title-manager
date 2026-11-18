import { ack, bridge, fingerprint, isTextConfirmation, nativeOrigin, readRuntime, recentTurns, safeID, submitEvent, textOf, waitCandidate } from "./client.mjs";
import { bindOrca, submitNative } from "./orca.mjs";

const userMessage = entry => entry.type === "message" && entry.message.role === "user";

function snapshot(pi, ctx, startLeaf) {
  const sessionId = ctx.sessionManager.getSessionId();
  const leaf = ctx.sessionManager.getLeafId();
  if (!safeID(sessionId)) return undefined;
  const currentTitle = pi.getSessionName() ?? "";
  const branch = ctx.sessionManager.getBranch();
  const messages = branch
    .filter(entry => entry.type === "message" && ["user", "assistant"].includes(entry.message.role))
    .slice(-16)
    .map(({ message }) => ({ role: message.role, text: textOf(message.content) }));
  const turns = recentTurns(messages);
  const before = startLeaf === null ? 0 : branch.findIndex(entry => entry.id != null && entry.id === startLeaf) + 1;
  const newUsers = startLeaf === null || before > 0 ? branch.slice(before).filter(userMessage) : [];
  return {
    agent: "pi", sessionId, revision: fingerprint(leaf, turns), currentTitle,
    titleOrigin: nativeOrigin(currentTitle), project: ctx.cwd, turns,
    originalGoal: textOf(branch.find(userMessage)?.message.content) || undefined,
    confirmationOnly: (newUsers.length > 0 && newUsers.every(entry => isTextConfirmation(entry.message.content))) || undefined,
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

async function applySettled({ pi, rpc, resolveBinding, ctx, signal, setName, resume, startLeaf, polling }) {
  const state = snapshot(pi, ctx, startLeaf);
  if (!state?.turns.length) return;
  const leaf = ctx.sessionManager.getLeafId();
  const fresh = () => !signal.aborted && ctx.isIdle() && ctx.sessionManager.getSessionId() === state.sessionId &&
    ctx.sessionManager.getLeafId() === leaf && (pi.getSessionName() ?? "") === state.currentTitle;
  if (!resume) {
    const binding = await resolveBinding(state.agent, state.sessionId, { signal, locator: ctx.sessionManager.getSessionFile() });
    if (!(await submitEvent(rpc, { ...state, ...binding }, { signal, fresh }))?.accepted) return;
  }
  const { candidate, status } = await waitCandidate(rpc, state, { ...polling, fresh, signal, resume });
  if (status === "failed" && !resume && fresh()) ctx.ui.notify("自动命名失败，当前标题保留。", "warning");
  if (!candidate || !fresh()) return;
  setName(candidate.title);
  const observed = ctx.sessionManager.getSessionName();
  if (ctx.sessionManager.getSessionId() !== state.sessionId || observed !== candidate.title) return;
  ctx.ui.notify("会话已命名为 " + observed, "info");
  await ack(rpc, state, candidate, observed);
}

export function register(pi, rpc, resolveBinding = bindOrca, polling) {
  let active;
  let writing;
  let startLeaf;
  const cancel = () => { active?.abort(); active = undefined; };
  const setName = name => {
    writing = name;
    try { pi.setSessionName(name); } finally { writing = undefined; }
  };
  const run = (ctx, resume) => {
    cancel();
    const controller = new AbortController();
    active = controller;
    const signal = controller.signal;
    const job = { pi, rpc, resolveBinding, ctx, signal, setName, resume, startLeaf, polling };
    void (async () => {
      if (resume) await syncExisting(job);
      await applySettled(job);
    })().catch(() => {
      if (!signal.aborted) ctx.ui.notify("标题管理器未完成会话标题处理。", "warning");
    }).finally(() => {
      if (active === controller) active = undefined;
    });
  };
  pi.on("session_start", (_event, ctx) => { startLeaf = undefined; run(ctx, true); });
  for (const name of ["session_shutdown", "session_before_switch", "session_before_fork", "session_before_tree"]) {
    pi.on(name, cancel);
  }
  pi.on("before_agent_start", (_event, ctx) => { cancel(); startLeaf = ctx.sessionManager.getLeafId(); });
  pi.on("session_info_changed", event => { if (event.name !== writing) cancel(); });
  pi.on("agent_settled", (event, ctx) => {
    if (!event.aborted && ctx.isIdle()) run(ctx, false);
    startLeaf = undefined;
  });
}

export default function (pi) {
  try { register(pi, bridge(readRuntime(import.meta.url))); }
  catch {
    pi.on("session_start", (_event, ctx) => ctx.ui.notify("标题管理器缺少桥接程序配置。", "warning"));
  }
}
