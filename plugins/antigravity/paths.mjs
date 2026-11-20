import { lstatSync, readdirSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, isAbsolute, join, parse, relative, resolve } from "node:path";
import { safeID } from "./client.mjs";

export { safeID };
export const uuid = value => typeof value === "string" && /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(value);
const normalize = path => process.platform === "win32" ? resolve(path).toLowerCase() : resolve(path);
export const samePath = (a, b) => normalize(a) === normalize(b);

export function roots() {
  const override = process.env.ANTIGRAVITY_DATA_DIR;
  if (override && !isAbsolute(override)) throw new Error("Invalid Antigravity data root");
  return [
    { dir: override ? resolve(override) : join(homedir(), ".gemini", "antigravity-cli"), kind: "cli" },
    { dir: join(homedir(), ".gemini", "antigravity"), kind: "desktop" },
  ];
}

// 逐级检查现有路径，连缺失文件上方的 junction 也必须拒绝。
export function safeStat(base, file) {
  if (!isAbsolute(base) || !isAbsolute(file)) throw new Error("Invalid Antigravity path");
  const rel = relative(base, file);
  if (isAbsolute(rel) || rel.split(/[\\/]/).includes("..")) throw new Error("Antigravity path escaped");
  const absolute = resolve(file);
  let path = parse(absolute).root;
  let parent = path;
  for (const part of ["", ...absolute.slice(path.length).split(/[\\/]/).filter(Boolean)]) {
    path = join(path, part);
    const info = lstatSync(path, { throwIfNoEntry: false });
    if (!info) return undefined;
    if (info.isSymbolicLink()) throw new Error("Antigravity path redirected");
    const real = realpathSync.native(path);
    if (!samePath(real, join(parent, part))) {
      // 8.3 别名不出现在目录项列表中，展开后的名称仍须属于刚核验过的父目录。
      const shortAlias = process.platform === "win32" && part &&
        samePath(dirname(real), parent) && sameFileIdentity(info, lstatSync(real)) &&
        !readdirSync(parent).some(name => name.toLowerCase() === part.toLowerCase());
      if (!shortAlias) throw new Error("Antigravity path redirected");
    }
    if (samePath(path, absolute)) return info;
    if (!info.isDirectory()) throw new Error("Invalid Antigravity directory");
    parent = real;
  }
}

export function sameFileIdentity(info, stat) {
  if (!info || !stat || info.ino !== stat.ino) return false;
  // Windows 随附 Node 22.14.0 的 lstat 可能缺少卷序列号，fstat 仍会返回实际值。
  return info.dev === stat.dev || (process.platform === "win32" && (info.dev === 0 || stat.dev === 0));
}

export function transcriptPath(dir, id) {
  if (!safeID(id)) throw new Error("Invalid Antigravity session ID");
  return join(dir, "brain", id, ".system_generated", "logs", "transcript.jsonl");
}
