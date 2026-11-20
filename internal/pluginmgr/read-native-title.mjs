import { opendirSync, lstatSync, readFileSync, existsSync } from "node:fs";
import { homedir } from "node:os";
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
  const { readTitle, rootDir } = await importAdapter("annotations.mjs");
  if (process.env.ANTIGRAVITY_DATA_DIR) return readTitle(process.env.ANTIGRAVITY_DATA_DIR, sessionId);
  const gemini = join(homedir(), ".gemini");
  const preferred = rootDir();
  if (existsSync(join(preferred, "annotations", sessionId + ".pbtxt"))) return readTitle(preferred, sessionId);
  const found = [];
  const dir = opendirSync(gemini);
  try { for (let n = 0; n < 4096; n++) { const item = dir.readSync(); if (!item) break; if (!item.isDirectory() || !item.name.startsWith("antigravity")) continue; const data = join(gemini, item.name); if (existsSync(join(data, "annotations", sessionId + ".pbtxt"))) found.push(data); } }
  finally { dir.closeSync(); }
  return found.length === 1 ? readTitle(found[0], sessionId) : "";
}

try {
  if (/^[A-Za-z0-9_-]{1,120}$/.test(sessionId)) {
    const title = await (agent === "claude" ? readClaude() : agent === "antigravity" ? readAntigravity() : readCodex());
    if (typeof title === "string" && Buffer.byteLength(title) <= 4096) {
      process.stdout.write(JSON.stringify({ title }));
    }
  }
} catch {
  // The caller treats missing output as a pending read, without writing a title.
  process.exitCode = 1;
}
