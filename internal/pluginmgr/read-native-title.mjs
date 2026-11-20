import { opendirSync, lstatSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [agent, sessionId, pluginDir, hostDir] = process.argv.slice(1);
const importAdapter = (filename) => import(pathToFileURL(join(pluginDir, filename)).href);

async function readCodex() {
  const { findCodex, withAppServer } = await importAdapter("app-server.mjs");
  const runtime = JSON.parse(readFileSync(join(pluginDir, "atm-runtime.json"), "utf8"));
  return withAppServer(findCodex(runtime.codexPath), async (call) => {
    const { thread } = await call("thread/read", { threadId: sessionId, includeTurns: false });
    if (thread?.id === sessionId) {
      if (thread.name === null) return "";
      if (typeof thread.name === "string") return thread.name;
    }
  });
}

function findClaudeTranscript() {
  const root = join(hostDir, "projects");
  const projects = opendirSync(root);
  let found;
  try {
    for (let count = 0; count < 4096; count++) {
      const project = projects.readSync();
      if (!project) return found;
      if (!project.isDirectory()) continue;
      const path = join(root, project.name, sessionId + ".jsonl");
      if (!lstatSync(path, { throwIfNoEntry: false })?.isFile()) continue;
      if (found) return undefined;
      found = path;
    }
  } finally {
    projects.closeSync();
  }
}

async function readClaude() {
  const { identify, readObservedTitle } = await importAdapter("transcript.mjs");
  const path = findClaudeTranscript();
  if (!path) return undefined;
  const session = identify({ session_id: sessionId, transcript_path: path }, hostDir);
  return session && readObservedTitle(session);
}

async function readAntigravity() {
  const { locateNative, readObservedTitle } = await importAdapter("transcript.mjs");
  const session = locateNative(sessionId);
  return session && readObservedTitle(session);
}

try {
  if (/^[A-Za-z0-9_-]{1,120}$/.test(sessionId)) {
    const title = await (agent === "claude" ? readClaude() : agent === "antigravity" ? readAntigravity() : readCodex());
    if (typeof title === "string" && Buffer.byteLength(title) <= 4096) {
      process.stdout.write(JSON.stringify({ title }));
    } else if (agent === "antigravity") {
      process.exitCode = 1;
    }
  }
} catch {
  // The caller treats missing output as a pending read, without writing a title.
  process.exitCode = 1;
}
