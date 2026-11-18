import { pathToFileURL } from "node:url";
import { ack, bridge, nativeOrigin, readInput, readRuntime, recentTurns, safeID, textOf, validCandidate } from "./client.mjs";
import manifest from "./agent-title-plugin.json" with { type: "json" };

// 外部宿主根据自己的生命周期调用此函数。参数是宿主的读写接口。
export async function nameSession(host, rpc) {
  const current = await host.read();
  const { sessionId, revision, currentTitle } = current;
  if (!safeID(sessionId) || typeof revision !== "string" || !revision || typeof currentTitle !== "string") {
    throw new Error("外部宿主提供的会话信息无效");
  }
  const agent = manifest.id;
  const turns = recentTurns(current.turns
    .filter(turn => ["user", "assistant"].includes(turn.role))
    .map(({ role, text }) => ({ role, text: textOf(text) })));
  if (!turns.length) return { status: "empty" };
  const fields = { agent, sessionId, revision, currentTitle };
  const submitted = await rpc("event.submit", { ...fields, turns, titleOrigin: nativeOrigin(currentTitle) });
  if (!submitted?.accepted) return { status: "skipped" };
  const { candidate } = (await rpc("title.claim", fields)) ?? {};
  if (!validCandidate(candidate, fields)) return { status: "pending" };

  // 宿主应在一次事务或同一事件循环回调内比较版本和标题，再执行写入。
  if (!(await host.applyIfCurrent(current, candidate.title))) return { status: "stale" };
  const observed = await host.read();
  if (observed.sessionId !== sessionId || observed.currentTitle !== candidate.title) return { status: "unconfirmed" };
  await ack(rpc, fields, candidate, observed.currentTitle);
  return { status: "applied" };
}

// 命令行只提交由外部宿主读出的快照；标题写回需使用上面的宿主接口。
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url && process.argv[2] === "submit") {
  try {
    const input = await readInput();
    if (!safeID(input.sessionId) || typeof input.revision !== "string" || !input.revision ||
        typeof input.currentTitle !== "string" || !Array.isArray(input.turns)) throw new Error();
    const turns = recentTurns(input.turns
      .filter(turn => turn && ["user", "assistant"].includes(turn.role))
      .map(turn => ({ role: turn.role, text: textOf(turn.text) })));
    if (!turns.length) throw new Error();
    const result = await bridge(readRuntime(import.meta.url))("event.submit", {
      agent: manifest.id, sessionId: input.sessionId, revision: input.revision,
      currentTitle: input.currentTitle, titleOrigin: nativeOrigin(input.currentTitle), turns,
    });
    process.stdout.write(JSON.stringify({ ok: true, result }) + "\n");
  } catch {
    process.stdout.write(JSON.stringify({ ok: false, error: "提交外部宿主快照失败" }) + "\n");
    process.exitCode = 1;
  }
}
