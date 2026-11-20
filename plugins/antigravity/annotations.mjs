import { randomUUID } from "node:crypto";
import { closeSync, fsyncSync, mkdirSync, openSync, readFileSync, renameSync, unlinkSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { diagnostic } from "./diagnostics.mjs";
import { safeID, safeStat } from "./paths.mjs";
import { readStored, updateSummaries } from "./summaries.mjs";

const MAX = 64 * 1024;
const utf8 = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
const annotationPath = (dir, id) => join(dir, "annotations", id + ".pbtxt");
const token = /#[^\n]*|"(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*'|[A-Za-z_][\w]*|[^\s]/g;
const escapeBytes = { a: 7, b: 8, f: 12, n: 10, r: 13, t: 9, v: 11, "\\": 92, "'": 39, '"': 34, "?": 63 };

function decodeString(token) {
  const bytes = [];
  const body = token.slice(1, -1);
  for (const match of body.matchAll(/\\(x[0-9a-f]{1,2}|[0-7]{1,3}|u[0-9a-f]{4}|U[0-9a-f]{8}|.)|[^\\]+|\\/gis)) {
    const escape = match[1];
    if (!escape) {
      if (match[0] === "\\") throw new Error("Invalid Antigravity annotation escape");
      bytes.push(Buffer.from(match[0]));
    } else if (escape in escapeBytes) {
      bytes.push(Buffer.of(escapeBytes[escape]));
    } else if (/^[0-7]+$/.test(escape) && Number.parseInt(escape, 8) <= 255) {
      bytes.push(Buffer.of(Number.parseInt(escape, 8)));
    } else if (escape[0] === "x" && escape.length > 1) {
      bytes.push(Buffer.of(Number.parseInt(escape.slice(1), 16)));
    } else if (/^[uU]/.test(escape) && escape.length > 1) {
      bytes.push(Buffer.from(String.fromCodePoint(Number.parseInt(escape.slice(1), 16))));
    } else {
      throw new Error("Invalid Antigravity annotation escape");
    }
  }
  return Buffer.concat(bytes);
}

function titleField(text) {
  const fields = Array.from(text.matchAll(token)).filter(match => !match[0].startsWith("#"));
  const stack = [];
  let title;
  for (let i = 0; i < fields.length; i++) {
    const value = fields[i][0];
    if ("{[<".includes(value)) stack.push({ "{": "}", "[": "]", "<": ">" }[value]);
    else if ("}]>".includes(value)) {
      if (stack.pop() !== value) throw new Error("Malformed Antigravity annotation");
    } else if (value === '"' || value === "'") {
      throw new Error("Unterminated Antigravity annotation string");
    } else if (!stack.length && value === "title" && fields[i + 1]?.[0] === ":") {
      const strings = [];
      for (let j = i + 2; /^["'].*["']$/s.test(fields[j]?.[0] ?? ""); j++) strings.push(fields[j]);
      if (title || !strings.length) throw new Error("Invalid Antigravity title field");
      title = {
        value: utf8.decode(Buffer.concat(strings.map(match => decodeString(match[0])))), strings,
      };
      i += 1 + strings.length;
    }
  }
  if (stack.length) throw new Error("Malformed Antigravity annotation");
  return title;
}

export function readTitle(dir, id) {
  if (!safeID(id)) throw new Error("Invalid Antigravity session ID");
  const text = readStored(dir, annotationPath(dir, id), MAX);
  return text === undefined ? "" : titleField(utf8.decode(text))?.value ?? "";
}

export function replaceStored(file, bytes, prior) {
  const temp = file + "." + randomUUID() + ".tmp";
  const fd = openSync(temp, "wx", 0o600);
  let installed = false;
  try {
    try { writeFileSync(fd, bytes); fsyncSync(fd); } finally { closeSync(fd); }
    const current = readStored(dirname(file), file, Math.max(64 * 1024, prior?.length ?? 0));
    if (prior === undefined ? current !== undefined : !current?.equals(prior)) return false;
    renameSync(temp, file);
    installed = true;
    return true;
  } finally {
    if (!installed) unlinkSync(temp);
  }
}

export function writeTitle(dir, id, title, expected) {
  if (!safeID(id) || typeof title !== "string" || !title.trim() || title.length > 256 || /[\u0000-\u001f\u007f]/.test(title)) {
    throw new Error("Invalid Antigravity annotation");
  }
  if (!safeStat(dir, dir)?.isDirectory()) throw new Error("Missing Antigravity data root");
  const file = annotationPath(dir, id);
  const prior = readStored(dir, file, MAX);
  const text = prior ? utf8.decode(prior) : "";
  const field = titleField(text);
  if (expected !== undefined && expected !== (field?.value ?? "")) return false;
  const encoded = JSON.stringify(title);
  const newline = text.includes("\r\n") ? "\r\n" : "\n";
  let next = text;
  if (field) {
    // 只替换字符串 token，保留内联字段、注释、换行和原始格式。
    for (const [index, match] of [...field.strings.entries()].reverse()) {
      next = next.slice(0, match.index) + (index === 0 ? encoded : "") + next.slice(match.index + match[0].length);
    }
  } else {
    next += (text && !text.endsWith("\n") ? newline : "") + "title:" + encoded + newline;
  }
  mkdirSync(join(dir, "annotations"), { recursive: true });
  if (!safeStat(dir, join(dir, "annotations"))?.isDirectory() || !replaceStored(file, next, prior)) return false;
  if (readTitle(dir, id) !== title) return false;
  updateSummaries(dir, id, title, replaceStored, diagnostic);
  return true;
}
