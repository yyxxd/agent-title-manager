import { setTimeout as delay } from "node:timers/promises";
import { pathToFileURL } from "node:url";
import { bridge, readRuntime } from "./client.mjs";
import { diagnostic } from "./diagnostics.mjs";
import { observeSettlements } from "./settlements.mjs";
import { processTurn } from "./index.mjs";
import { roots, transcriptPath, uuid } from "./paths.mjs";
import { openDatabase } from "./summaries.mjs";
import { completedTurn, identify, snapshot } from "./transcript.mjs";

const MAX_SESSIONS = 64;

function readRows(root, name, sql, ...args) {
  const db = openDatabase(root, name);
  if (!db) return [];
  try { return db.prepare(sql).all(...args); }
  finally { db.close(); }
}

export function readDesktopSettlements(root, since) {
  return readRows(root.dir, "conversation_summaries.db", `
    SELECT conversation_id AS native, step_count AS steps, last_user_input_step_index AS userStep
    FROM conversation_summaries
    WHERE status = 'CASCADE_RUN_STATUS_IDLE' AND not_fully_idle = 0
      AND julianday(last_modified_time) >= julianday(?, 'unixepoch')
    ORDER BY last_modified_time DESC LIMIT ?`, since / 1000, MAX_SESSIONS);
}

export function matchDesktopSettlement(root, row, since) {
  if (!uuid(row.native)) return;
  const identified = identify({ conversationId: row.native, transcriptPath: transcriptPath(root.dir, row.native) }, root.dir);
  if (!identified) return;
  const turn = completedTurn(identified);
  if (!turn?.answer.at || turn.answer.at < since || turn.answer.step === undefined ||
      row.steps <= turn.answer.step || (row.userStep >= 0 && row.userStep !== turn.user.step)) return;
  const state = snapshot(identified);
  if (state?.revision === turn.revision) return { identified, state };
}

export function createObserver(rpc, { started = Date.now(), process = processTurn } = {}) {
  const root = roots().find(root => root.kind === "desktop");
  return observeSettlements({
    started, read: since => readDesktopSettlements(root, since),
    match: (row, since) => matchDesktopSettlement(root, row, since),
    change: row => row.steps,
    process: (_row, identified, state, options) => process({}, identified, state, rpc, options),
  });
}

async function main() {
  try {
    const observer = createObserver(bridge(readRuntime(import.meta.url), 1000));
    diagnostic("observer.started");
    // 父进程持有 stdin，意外退出时观察器也会停止。
    let closed = false;
    process.stdin.resume();
    process.stdin.once("end", () => { closed = true; observer.stop(); });
    while (!closed) {
      await observer.scan();
      await delay(3000);
    }
  } catch (error) {
    diagnostic("observer.failed", error);
    process.exitCode = 1;
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
