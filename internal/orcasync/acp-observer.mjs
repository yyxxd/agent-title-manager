import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { createInterface } from "node:readline";
import { setTimeout as delay } from "node:timers/promises";

const [pluginDir] = process.argv.slice(1);
const load = name => import(pathToFileURL(join(pluginDir, name)).href);
const { transcriptPath } = await load("paths.mjs");
const { completedTurn, identify, snapshot } = await load("transcript.mjs");
const { processTurn } = await load("index.mjs");
const { observeSettlements } = await load("settlements.mjs");
const { diagnostic } = await load("diagnostics.mjs");
function match(row, since) {
  const identified = identify({ conversationId: row.native, transcriptPath: transcriptPath(row.root, row.native) }, row.root);
  if (!identified) return;
  const turn = completedTurn(identified);
  if (!turn?.answer.at || turn.answer.at < since || turn.answer.at > row.at || turn.user.at > row.at) return;
  const state = snapshot(identified);
  if (state?.revision === turn.revision) return { identified, state };
}

const input = createInterface({ input: process.stdin });
const rpc = parentRPC(input, process.stdout, diagnostic);
const observer = observeSettlements({
  started: Date.now(), read: since => rpc(null, "settlements.list", { since }),
  match,
  change: row => JSON.stringify([row.orca, row.at, row.root]),
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
