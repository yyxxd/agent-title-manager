import { isAbsolute } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { pathToFileURL } from "node:url";
import { bridge, readRuntime } from "./client.mjs";
import { diagnostic } from "./diagnostics.mjs";
import { processTurn } from "./index.mjs";
import { orcaHome, roots, samePath, transcriptPath, uuid } from "./paths.mjs";
import { openDatabase } from "./summaries.mjs";
import { completedTurn, identify, snapshot } from "./transcript.mjs";

const MAX_SESSIONS = 64;
const REPLAY_WINDOW = 5 * 60_000;

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

export function readACPSettlements(root, home, since) {
  if (!home) return [];
  const rows = readRows(home, "agent-session-domain.db", `
    SELECT session_id AS orca, json_extract(record_json, '$.lastTurn.at') AS at,
           json_extract(record_json, '$.accountHome.path') AS home,
           json_extract(record_json, '$.providerHandleChain[#-1].handle.nativeId') AS native
    FROM agent_session_records
    WHERE json_extract(record_json, '$.schemaVersion') = 2
      AND json_extract(record_json, '$.sessionId') = session_id
      AND json_extract(record_json, '$.provider') = 'antigravity'
      AND json_extract(record_json, '$.location.executionHostId') = 'local'
      AND json_extract(record_json, '$.accountHome.variable') = 'GEMINI_HOME'
      AND json_extract(record_json, '$.providerHandleChain[#-1].handle.transport') = 'acp'
      AND json_extract(record_json, '$.providerHandleChain[#-1].handle.agent') = 'antigravity'
      AND json_extract(record_json, '$.lastTurn.state') = 'settled'
      AND json_extract(record_json, '$.lastTurn.outcome') = 'success'
      AND coalesce(json_extract(record_json, '$.conversationNameSource'), '') <> 'user'
      AND json_extract(record_json, '$.lastTurn.at') >= ?
    ORDER BY at DESC LIMIT ?`, since, MAX_SESSIONS);
  return rows.filter(row => uuid(row.native) && uuid(row.orca) && typeof row.home === "string" &&
    isAbsolute(row.home) && samePath(row.home, root.accountHome) && Number.isSafeInteger(row.at));
}

export function matchSettlement(root, row, since) {
  if (!uuid(row.native)) return;
  const identified = identify({ conversationId: row.native, transcriptPath: transcriptPath(root.dir, row.native) });
  if (!identified || identified.kind !== root.kind) return;
  const turn = completedTurn(identified);
  if (!turn?.answer.at || turn.answer.at < since) return;
  if (root.kind === "desktop") {
    if (turn.answer.step === undefined || row.steps <= turn.answer.step ||
        (row.userStep >= 0 && row.userStep !== turn.user.step)) return;
  } else if (turn.answer.at > row.at || (turn.user.at && turn.user.at > row.at)) return;
  const state = snapshot(identified);
  if (state?.revision !== turn.revision) return;
  return { identified, state };
}

export function createObserver(rpc, { started = Date.now(), process = processTurn } = {}) {
  const jobs = new Map(), seen = new Map();
  const readCurrent = (root, since) => root.kind === "desktop"
    ? readDesktopSettlements(root, since) : readACPSettlements(root, orcaHome(), since);
  const scan = () => {
    const since = Math.max(started - 1000, Date.now() - REPLAY_WINDOW);
    for (const [key, item] of seen) if (item.at < since) seen.delete(key);
    for (const root of roots()) {
      if (root.kind === "cli") continue;
      try {
        const rows = readCurrent(root, since);
        for (const row of rows) {
          try {
            const key = root.kind + "/" + row.native;
            const change = row.at ?? row.steps;
            if (seen.get(key)?.change === change) continue;
            const match = matchSettlement(root, row, since);
            if (!match) continue;
            const { identified, state } = match;
            const prior = jobs.get(key);
            if (seen.get(key)?.revision === state.revision || prior?.revision === state.revision) continue;
            if (jobs.size >= 4 && !prior) continue;
            prior?.controller.abort();
            const controller = new AbortController();
            const job = { controller, revision: state.revision };
            jobs.set(key, job);
            const canWrite = () => readCurrent(root, since).some(current => current.native === row.native &&
              current.orca === row.orca && (current.at ?? current.steps) === change);
            void process({}, identified, state, rpc, undefined, { expectedOrca: row.orca, signal: controller.signal, canWrite })
              .then(handled => {
                if (handled && !controller.signal.aborted) seen.set(key, { change, revision: state.revision, at: Date.now() });
              })
              .catch(error => { if (!controller.signal.aborted) diagnostic("observer.turn.failed", error); })
              .finally(() => { if (jobs.get(key) === job) jobs.delete(key); });
          } catch (error) { diagnostic("observer.session.invalid", error); }
        }
      } catch (error) { diagnostic(root.kind === "acp" ? "observer.acp.failed" : "observer.desktop.failed", error); }
    }
  };
  return { scan, stop: () => { for (const job of jobs.values()) job.controller.abort(); } };
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
      observer.scan();
      await delay(3000);
    }
  } catch (error) {
    diagnostic("observer.failed", error);
    process.exitCode = 1;
  }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
