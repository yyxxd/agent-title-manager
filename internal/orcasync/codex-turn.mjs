import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [dir, home, sessionId, revision] = process.argv.slice(1);
const load = name => import(pathToFileURL(join(dir, name)).href);
const { findCodex, withAppServer } = await load("app-server.mjs");
const { latestTurn, readThread, stopContext } = await load("history.mjs");
const { nativeOrigin, readRuntime } = await load("client.mjs");
const runtime = readRuntime(pathToFileURL(join(dir, "index.mjs")));
const snapshot = await withAppServer(findCodex(runtime.codexPath), async call => {
  const first = await readThread(call, sessionId);
  if (!first) return null;
  const context = await stopContext(call, first, revision);
  if (!context?.turns.length) return null;
  const observed = await readThread(call, sessionId);
  if (!observed || observed.name !== first.name || (await latestTurn(call, observed))?.id !== revision) return null;
  const currentTitle = observed.name ?? "";
  return { ...context, currentTitle, titleOrigin: nativeOrigin(currentTitle), project: observed.cwd };
}, { ...process.env, CODEX_HOME: home });
process.stdout.write(JSON.stringify(snapshot));
