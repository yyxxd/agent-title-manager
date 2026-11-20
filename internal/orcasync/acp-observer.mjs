import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { createInterface } from "node:readline";
import { setTimeout as delay } from "node:timers/promises";

const [pluginDir, home] = process.argv.slice(1);
const load = name => import(pathToFileURL(join(pluginDir, name)).href);
const { transcriptPath } = await load("paths.mjs");
const { completedTurn, identify, snapshot } = await load("transcript.mjs");
const { processTurn } = await load("index.mjs");
const { observeSettlements } = await load("settlements.mjs");
const { diagnostic } = await load("diagnostics.mjs");
function match(root, row, since) {
  const identified = identify({ conversationId: row.native, transcriptPath: transcriptPath(root.dir, row.native) }, root.dir);
  if (!identified) return;
  const turn = completedTurn(identified);
  if (!turn?.answer.at || turn.answer.at < since || turn.answer.at > row.at || turn.user.at > row.at) return;
  const state = snapshot(identified);
  if (state?.revision === turn.revision) return { identified, state };
}

// 只有桌面服务持有这对管道。工作台标识来自当前数据库记录，而非 Hook 请求。
function parentRPC(input) {
  let sequence = 0;
  const pending = new Map();
  input.on("line", line => {
    try {
      const { id, result, error } = JSON.parse(line);
      const job = pending.get(id);
      pending.delete(id);
      if (error) job?.reject(new Error(error));
      else job?.resolve(result);
    } catch (error) { diagnostic("observer.reply.invalid", error); }
  });
  input.once("close", () => {
    for (const job of pending.values()) job.reject(new Error("Observer parent disconnected"));
    pending.clear();
  });
  return (row, method, params, signal) => new Promise((resolve, reject) => {
    if (input.closed || signal?.aborted) return reject(new Error("Observer stopped"));
    const id = ++sequence;
    pending.set(id, { resolve, reject });
    process.stdout.write(JSON.stringify({ id, orcaSessionId: row?.orca, method, params }) + "\n", error => {
      if (error) {
        pending.delete(id);
        reject(error);
      }
    });
  });
}

const accountHome = join(home, "antigravity-chat");
const root = { dir: join(accountHome, "antigravity-acp"), accountHome };
const input = createInterface({ input: process.stdin });
const rpc = parentRPC(input);
const observer = observeSettlements({
  started: Date.now(), read: since => rpc(null, "settlements.list", { since }),
  match: (row, since) => match(root, row, since),
  change: row => row.orca + "/" + row.at,
  process: (row, identified, state, options) =>
    processTurn({}, identified, state, (method, params, signal) => rpc(row, method, params, signal), options),
});
const controller = new AbortController();
input.once("close", () => { observer.stop(); controller.abort(); });
while (!controller.signal.aborted) {
  await observer.scan();
  try { await delay(3000, undefined, { signal: controller.signal }); }
  catch (error) { if (!controller.signal.aborted) throw error; }
}
