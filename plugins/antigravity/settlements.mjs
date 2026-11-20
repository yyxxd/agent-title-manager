import { diagnostic } from "./diagnostics.mjs";

const REPLAY_WINDOW = 5 * 60_000;

export function observeSettlements({ started, read, match, change, process }) {
  const jobs = new Map(), seen = new Map();
  let stopped = false;
  const scan = async () => {
    if (stopped) return;
    const since = Math.max(started - 1000, Date.now() - REPLAY_WINDOW);
    for (const [key, item] of seen) if (item.at < since) seen.delete(key);
    try {
      for (const row of await read(since)) {
        if (stopped) return;
        try {
          const key = row.native;
          const version = change(row);
          if (seen.get(key)?.change === version) continue;
          const ready = match(row, since);
          if (!ready) continue;
          const { identified, state } = ready;
          const prior = jobs.get(key);
          if (seen.get(key)?.revision === state.revision || prior?.revision === state.revision) continue;
          if (jobs.size >= 4 && !prior) continue;
          prior?.controller.abort();
          const controller = new AbortController();
          const job = { controller, revision: state.revision };
          jobs.set(key, job);
          const canWrite = async () => {
            const current = await read(since);
            return !stopped && !controller.signal.aborted &&
              current.some(item => item.native === key && change(item) === version);
          };
          void process(row, identified, state, { signal: controller.signal, canWrite })
            .then(handled => {
              if (handled && !controller.signal.aborted) seen.set(key, { change: version, revision: state.revision, at: Date.now() });
            })
            .catch(error => { if (!controller.signal.aborted) diagnostic("observer.turn.failed", error); })
            .finally(() => { if (jobs.get(key) === job) jobs.delete(key); });
        } catch (error) { diagnostic("observer.session.invalid", error); }
      }
    } catch (error) { diagnostic("observer.scan.failed", error); }
  };
  return { scan, stop: () => {
    stopped = true;
    for (const job of jobs.values()) job.controller.abort();
  } };
}
