import { appendFileSync, lstatSync, renameSync } from "node:fs";

const log = new URL("./antigravity-diagnostics.log", import.meta.url);
const previous = new URL("./antigravity-diagnostics.previous.log", import.meta.url);
const maxSize = 128 * 1024;
const knownCodes = new Set(["ENOENT", "EACCES", "EPERM", "EPIPE", "EBUSY", "ETIMEDOUT", "ERR_SQLITE_ERROR", "ingress_busy"]);

export function diagnostic(stage, error) {
  // 调用方只传固定阶段名，从不打印路径、会话 ID、请求、错误正文或子进程输出。
  const code = knownCodes.has(error?.code) ? " " + error.code : "";
  const line = new Date().toISOString() + " " + stage + code + "\n";
  if (process.env.NODE_TEST_CONTEXT) return process.stderr.write(line);
  try {
    const info = lstatSync(log, { throwIfNoEntry: false });
    if (info && (!info.isFile() || info.nlink !== 1)) throw new Error("Invalid diagnostic log");
    if (info?.size >= maxSize) renameSync(log, previous);
    appendFileSync(log, line, { mode: 0o600 });
  } catch {
    process.stderr.write(line);
  }
}
