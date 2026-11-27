import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [dir, home, sessionId] = process.argv.slice(1);
const load = name => import(pathToFileURL(join(dir, name)).href);
const { findCodex, withAppServer } = await load("app-server.mjs");
const { readThread } = await load("history.mjs");
const { readRuntime } = await load("client.mjs");
const runtime = readRuntime(pathToFileURL(join(dir, "index.mjs")));
const title = await withAppServer(findCodex(runtime.codexPath), async call => {
  const thread = await readThread(call, sessionId);
  return thread ? thread.name ?? "" : null;
}, { ...process.env, CODEX_HOME: home });
process.stdout.write(JSON.stringify(title));
