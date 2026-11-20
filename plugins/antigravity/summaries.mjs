import { closeSync, fstatSync, openSync, readSync } from "node:fs";
import { basename, join } from "node:path";
import { safeStat } from "./paths.mjs";

export function readStored(root, file, limit) {
  const info = safeStat(root, file);
  if (!info) return undefined;
  if (!info.isFile() || info.nlink !== 1) throw new Error("Invalid Antigravity file");
  const fd = openSync(file, "r");
  try {
    const stat = fstatSync(fd);
    if (!stat.isFile() || stat.nlink !== 1 || stat.size > limit || stat.ino !== info.ino || stat.dev !== info.dev) {
      throw new Error("Antigravity file changed or exceeded its size limit");
    }
    const buffer = Buffer.alloc(stat.size + 1);
    let read = 0;
    while (read < buffer.length) {
      const count = readSync(fd, buffer, read, buffer.length - read, read);
      if (!count) break;
      read += count;
    }
    if (read !== stat.size) throw new Error("Antigravity file changed while reading");
    return buffer.subarray(0, read);
  } finally { closeSync(fd); }
}

export function openDatabase(root, name, readOnly = true) {
  const file = join(root, name);
  const info = safeStat(root, file);
  if (!info) return undefined;
  if (!info.isFile() || info.nlink !== 1) throw new Error("Invalid Antigravity database");
  for (const suffix of ["-wal", "-shm", "-journal"]) {
    const sidecar = safeStat(root, file + suffix);
    if (sidecar && (!sidecar.isFile() || sidecar.nlink !== 1)) throw new Error("Invalid database sidecar");
  }
  const Database = process.getBuiltinModule?.("node:sqlite")?.DatabaseSync;
  if (!Database) throw new Error("Antigravity requires Node SQLite support");
  return new Database(file, { readOnly, timeout: 200 });
}

function updateDatabase(dir, id, title) {
  const db = openDatabase(dir, "conversation_summaries.db", false);
  if (!db) return;
  try {
    db.exec("BEGIN IMMEDIATE");
    const row = db.prepare("SELECT raw_summary FROM conversation_summaries WHERE conversation_id = ?").get(id);
    if (row) {
      const raw = row.raw_summary && replaceSummary(Buffer.from(row.raw_summary), Buffer.from(title));
      if (raw) db.prepare("UPDATE conversation_summaries SET title = ?, raw_summary = ? WHERE conversation_id = ?").run(title, raw, id);
      else db.prepare("UPDATE conversation_summaries SET title = ? WHERE conversation_id = ?").run(title, id);
      if (db.prepare("SELECT 1 FROM sqlite_master WHERE name = 'conversations_fts'").get()) {
        db.prepare("UPDATE conversations_fts SET title = ? WHERE conversation_id = ? AND chunk_index = -1").run(title, id);
      }
    }
    db.exec("COMMIT");
  } finally { db.close(); } // 关闭连接会回滚未提交事务。
}

function readVarint(bytes, start) {
  let value = 0n;
  for (let i = start; i < bytes.length && i < start + 10; i++) {
    const byte = bytes[i];
    value |= BigInt(byte & 127) << BigInt(7 * (i - start));
    if (byte < 128 && (i < start + 9 || byte <= 1)) return [value, i + 1];
  }
  throw new Error("Invalid Antigravity protobuf varint");
}

function lengthBytes(length) {
  const bytes = [];
  do {
    bytes.push((length % 128) | (length >= 128 ? 128 : 0));
    length = Math.floor(length / 128);
  } while (length);
  return Buffer.from(bytes);
}

function* fields(bytes) {
  let end = 0;
  while (end < bytes.length) {
    const start = end;
    const [tag, tagEnd] = readVarint(bytes, start);
    const number = tag >> 3n;
    const wire = Number(tag & 7n);
    let valueStart = tagEnd;
    if (wire === 2) {
      const [length, next] = readVarint(bytes, tagEnd);
      if (length > BigInt(bytes.length - next)) throw new Error("Invalid Antigravity protobuf length");
      valueStart = next;
      end = next + Number(length);
    } else if (wire === 0) {
      [, end] = readVarint(bytes, tagEnd);
    } else if (wire === 1 || wire === 5) {
      end = tagEnd + (wire === 1 ? 8 : 4);
    } else {
      throw new Error("Unsupported Antigravity protobuf wire type");
    }
    if (number === 0n || end > bytes.length) throw new Error("Invalid Antigravity protobuf field");
    yield { number, wire, tag: bytes.subarray(start, tagEnd), value: bytes.subarray(valueStart, end), raw: bytes.subarray(start, end) };
  }
}

function mapMessage(bytes, transform, missing) {
  const parts = [];
  let updated = false;
  for (const field of fields(bytes)) {
    const next = field.wire === 2 ? transform(field) : undefined;
    if (next && !field.value.equals(next)) {
      updated = true;
      parts.push(field.tag, lengthBytes(next.length), next);
    } else parts.push(field.raw);
  }
  const extra = missing?.();
  if (extra) {
    updated = true;
    parts.push(extra);
  }
  return updated ? Buffer.concat(parts) : undefined;
}

function stringField(number, value) {
  return Buffer.concat([lengthBytes(number * 8 + 2), lengthBytes(value.length), value]);
}

function replaceAnnotationTitle(bytes, title) {
  let found = false;
  return mapMessage(bytes, ({ number }) => {
    if (number !== 1n) return;
    found = true;
    return title;
  }, () => found ? undefined : stringField(1, title));
}

function replaceSummary(bytes, title) {
  // 真实摘要的字段 1 是预览，字段 15.1 才是注释标题。
  let found = false;
  return mapMessage(bytes, ({ number, value }) => {
    if (number !== 15n) return;
    found = true;
    return replaceAnnotationTitle(value, title);
  }, () => found ? undefined : stringField(15, stringField(1, title)));
}

function updateCache(dir, name, id, title, replaceStored) {
  const file = join(dir, name);
  const prior = readStored(dir, file, 16 * 1024 * 1024);
  if (!prior) return;
  let matches = 0;
  const next = mapMessage(prior, ({ number, value }) => {
    if (number !== 1n) return;
    const entry = [...fields(value)];
    if (!entry.some(field => field.number === 1n && field.wire === 2 && field.value.toString("utf8") === id)) return;
    if (++matches !== 1) throw new Error("Ambiguous Antigravity summary cache");
    return mapMessage(value, field => field.number === 2n ? replaceSummary(field.value, Buffer.from(title)) : undefined);
  });
  if (next && !replaceStored(file, next, prior)) throw new Error("Antigravity summary cache changed concurrently");
}

export function updateSummaries(dir, id, title, replaceStored, diagnostic) {
  try { updateDatabase(dir, id, title); }
  catch (error) { diagnostic("summary.sqlite.failed", error); }
  const name = { "antigravity": "agyhub", "antigravity-cli": "jetbox" }[basename(dir)];
  if (!name) return;
  try { updateCache(dir, name + "_summaries_proto.pb", id, title, replaceStored); }
  catch (error) { diagnostic("summary.protobuf.failed", error); }
}
