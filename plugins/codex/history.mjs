import { setTimeout as delay } from "node:timers/promises";
import { isTextConfirmation, recentTurns, textOf } from "./client.mjs";

const users = turn => (turn.items ?? []).filter(item => item.type === "userMessage");
const full = turn => turn.itemsView == null || turn.itemsView === "full";

export async function readThread(call, threadId, includeTurns = false) {
  const thread = (await call("thread/read", { threadId, includeTurns }))?.thread;
  return thread?.id === threadId ? thread : undefined;
}

async function page(call, threadId, limit, sortDirection, itemsView) {
  const result = await call("thread/turns/list", { threadId, limit, sortDirection, itemsView });
  return Array.isArray(result?.data) ? result.data : [];
}

export async function latestTurn(call, thread) {
  if (thread.historyMode === "paginated") return (await page(call, thread.id, 1, "desc", "notLoaded"))[0];
  return (await readThread(call, thread.id, true))?.turns?.at(-1);
}

export async function stopContext(call, thread, revision) {
  const deadline = Date.now() + 3300;
  do {
    const paginated = thread.historyMode === "paginated";
    const history = paginated ? (await page(call, thread.id, 5, "desc", "full")).reverse() :
      (await readThread(call, thread.id, true))?.turns ?? [];
    const turn = history.at(-1);
    if (turn?.id !== revision) return undefined;
    if (turn.status === "completed") {
      const beginning = paginated ? await page(call, thread.id, 8, "asc", "full").catch(() => []) : history;
      const userMessages = users(turn);
      return {
        turns: recentTurns(messages(history.slice(-5))),
        originalGoal: firstGoal(beginning),
        confirmationOnly: (full(turn) && userMessages.length > 0 &&
          userMessages.every(item => isTextConfirmation(item.content))) || undefined,
      };
    }
    if (turn.status !== "inProgress" || Date.now() >= deadline) return undefined;
    await delay(300);
  } while (true);
}

function firstGoal(history) {
  for (const turn of history) {
    if (!full(turn)) return undefined;
    const first = users(turn)[0];
    if (first) return textOf(first.content) || undefined;
  }
}

function messages(history) {
  const result = [];
  for (const turn of history) {
    if (turn.status !== "completed") continue;
    for (const item of turn.items ?? []) {
      if (item.type === "userMessage") result.push({ role: "user", text: textOf(item.content) });
      else if (item.type === "agentMessage" && (item.phase == null || item.phase === "final_answer")) {
        result.push({ role: "assistant", text: textOf(item.text) });
      }
    }
  }
  return result;
}
