import { closeSync, existsSync, fstatSync, fsyncSync, openSync, readFileSync, realpathSync, renameSync, mkdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";

const MAX = 64 * 1024;
const rootDir = () => resolve(process.env.ANTIGRAVITY_DATA_DIR || join(homedir(), ".gemini", "antigravity-cli"));
export function annotationPath(appDataDir, id) { return join(appDataDir, "annotations", id + ".pbtxt"); }
function quote(value) { return JSON.stringify(value).slice(1, -1).replace(/\u0008/g, "\\b").replace(/\u000c/g, "\\f"); }
function unquote(value) { try { return JSON.parse('"' + value.replace(/\\([\s\S])/g, (_, c) => c === '"' ? '\\"' : '\\' + c) + '"'); } catch { return value; } }
function safePath(file, base) {
  const realBase = realpathSync(base);
  const real = realpathSync(file);
  const rel = relative(realBase, real);
  return rel && !rel.startsWith("..") && !isAbsolute(rel) ? real : undefined;
}
export function readTitle(appDataDir, id) {
  if (!/^[A-Za-z0-9_-]{1,120}$/.test(id)) return "";
  const file = annotationPath(appDataDir, id);
  try { if (existsSync(file) && !safePath(file, appDataDir)) throw new Error("annotation file escaped"); } catch (error) { if (error.code !== "ENOENT") throw error; }
  try {
    const real = safePath(file, appDataDir);
    if (!real) return "";
    const fd = openSync(real, "r");
    const stat = fstatSync(fd);
    closeSync(fd);
    if (stat.size > MAX) return "";
    const text = readFileSync(real, "utf8");
    const match = text.match(/^\s*title\s*:\s*"((?:\\.|[^"\\])*)"\s*$/m);
    return match ? unquote(match[1]) : "";
  } catch (error) { if (error.code === "ENOENT") return ""; return ""; }
}
export function writeTitle(appDataDir, id, title) {
  if (!/^[A-Za-z0-9_-]{1,120}$/.test(id) || typeof title !== "string" || title.length > 256 || /[\u0000-\u001f\u007f]/.test(title)) throw new Error("invalid annotation");
  const dir = join(appDataDir, "annotations");
  mkdirSync(dir, { recursive: true });
  try { if (realpathSync(dir) !== resolve(dir)) throw new Error("annotation path escaped"); } catch (error) { if (error.code !== "ENOENT") throw error; }
  const file = annotationPath(appDataDir, id);
  let prior = "";
  try { prior = readFileSync(file, "utf8"); } catch (error) { if (error.code !== "ENOENT") throw error; }
  const lines = prior ? prior.split(/\r?\n/).filter(line => !/^\s*title\s*:/.test(line)) : [];
  while (lines.at(-1) === "") lines.pop();
  lines.push(`title:"${quote(title)}"`, "");
  const temp = file + `.tmp-${process.pid}-${Date.now()}`;
  const fd = openSync(temp, "wx", 0o600);
  try { writeFileSync(fd, lines.join("\n"), "utf8"); fsyncSync(fd); } finally { closeSync(fd); }
  renameSync(temp, file);
  updateSummary(appDataDir, id, title);
  return readTitle(appDataDir, id) === title;
}
function updateSummary(appDataDir, id, title) {
  try {
    const builtin = process.getBuiltinModule?.("node:sqlite");
    if (!builtin?.DatabaseSync) return;
    const db = new builtin.DatabaseSync(join(appDataDir, "conversation_summaries.db"));
    try {
      db.prepare("UPDATE conversation_summaries SET title=?, preview=?, raw_summary=? WHERE conversation_id=?").run(title, title, title, id);
      db.prepare("UPDATE conversations_fts SET title=? WHERE conversation_id=? AND chunk_index=-1").run(title, id);
    } finally { db.close(); }
  } catch { /* pbtxt remains authoritative */ }
}
export { rootDir };
