import { closeSync, existsSync, fstatSync, fsyncSync, lstatSync, openSync, readFileSync, realpathSync, renameSync, mkdirSync, writeFileSync } from "node:fs";
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
  if (!safePath(dir, appDataDir)) throw new Error("annotation path escaped");
  const file = annotationPath(appDataDir, id);
  try { if (lstatSync(file).isSymbolicLink()) throw new Error("annotation file symlink"); } catch (error) { if (error.code !== "ENOENT") throw error; }
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
    const dbPath = join(appDataDir, "conversation_summaries.db");
    if (!existsSync(dbPath) || lstatSync(dbPath).isSymbolicLink()) return;
    const builtin = process.getBuiltinModule?.("node:sqlite");
    if (!builtin?.DatabaseSync) return;
    const db = new builtin.DatabaseSync(dbPath);
    try {
      db.prepare("UPDATE conversation_summaries SET title=?, preview=? WHERE conversation_id=?").run(title, title, id);
      const row = db.prepare("SELECT raw_summary FROM conversation_summaries WHERE conversation_id=?").get(id);
      const raw = row?.raw_summary;
      const updated = raw && replaceSummary(raw, title);
      if (updated) db.prepare("UPDATE conversation_summaries SET raw_summary=? WHERE conversation_id=?").run(updated, id);
      db.prepare("UPDATE conversations_fts SET title=? WHERE conversation_id=? AND chunk_index=-1").run(title, id);
    } finally { db.close(); }
  } catch { /* pbtxt remains authoritative */ }
}

function varint(bytes, offset) { let value = 0, shift = 0; for (let i = offset; i < bytes.length && shift < 64; i++, shift += 7) { const b = bytes[i]; value += (b & 0x7f) * 2 ** shift; if (!(b & 0x80)) return [value, i + 1]; } return undefined; }
function encodeVarint(value) { const out = []; do { let b = value % 128; value = Math.floor(value / 128); if (value) b |= 128; out.push(b); } while (value); return Uint8Array.from(out); }
function replaceMessage(bytes, title) {
  const out = [], encoder = new TextEncoder(); let i = 0, found = false;
  while (i < bytes.length) { const start = i, tag = varint(bytes, i); if (!tag) return undefined; i = tag[1]; const field = tag[0] >> 3, wire = tag[0] & 7; if (wire !== 2) { if (wire === 0) { const v = varint(bytes, i); if (!v) return undefined; i = v[1]; } else return undefined; out.push(bytes.slice(start, i)); continue; } const len = varint(bytes, i); if (!len || i + len[0] > bytes.length) return undefined; i = len[1]; const payload = bytes.slice(i, i + len[0]); i += len[0]; if (field === 1) { const value = encoder.encode(title); out.push(Uint8Array.from([0x0a]), encodeVarint(value.length), value); found = true; } else out.push(bytes.slice(start, i)); }
  return found ? Uint8Array.from(out.flatMap(part => [...part])) : undefined;
}
function replaceSummary(raw, title) {
  const bytes = raw instanceof Uint8Array ? raw : Buffer.from(raw); const out = [], encoder = new TextEncoder(); let i = 0, changed = false;
  while (i < bytes.length) { const start = i, tag = varint(bytes, i); if (!tag) return undefined; i = tag[1]; const field = tag[0] >> 3, wire = tag[0] & 7; if (wire !== 2) { if (wire === 0) { const v = varint(bytes, i); if (!v) return undefined; i = v[1]; } else return undefined; out.push(bytes.slice(start, i)); continue; } const len = varint(bytes, i); if (!len || i + len[0] > bytes.length) return undefined; i = len[1]; const payload = bytes.slice(i, i + len[0]); i += len[0]; let replacement = payload; if (field === 1) { replacement = encoder.encode(title); changed = true; } else if (field === 15) { replacement = replaceMessage(payload, title) || payload; changed = changed || replacement !== payload; } const changedField = field === 1 || (field === 15 && replacement !== payload); const prefix = changedField ? Uint8Array.from([((field << 3) | 2), ...encodeVarint(replacement.length)]) : bytes.slice(start, i - payload.length); out.push(prefix, replacement); }
  return changed ? Uint8Array.from(out.flatMap(part => [...part])) : undefined;
}
export { rootDir };
