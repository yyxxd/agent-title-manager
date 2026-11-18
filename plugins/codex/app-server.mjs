import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { isAbsolute, join } from "node:path";
import { createInterface } from "node:readline";

export function findCodex(explicit, env = process.env) {
  if (explicit !== undefined) {
    if (typeof explicit !== "string" || !isAbsolute(explicit) ||
        (process.platform === "win32" && !/\.exe$/i.test(explicit))) throw new Error("Codex CLI 路径无效");
    return explicit;
  }
  if (process.platform !== "win32") return "codex";
  const variant = process.arch === "arm64" ? "arm64" : "x64";
  const target = variant === "arm64" ? "aarch64-pc-windows-msvc" : "x86_64-pc-windows-msvc";
  for (const dir of (env.PATH ?? "").split(";").map(part => part.replace(/^"|"$/g, ""))) {
    if (!isAbsolute(dir)) continue;
    const native = join(dir, "codex.exe");
    if (existsSync(native)) return native;
    const scope = join(dir, "node_modules", "@openai");
    const platformPackage = "codex-win32-" + variant;
    const codexPackage = join(scope, "codex");
    for (const base of [codexPackage, join(scope, platformPackage), join(codexPackage, "node_modules", "@openai", platformPackage)]) {
      for (const folder of ["bin", "codex"]) {
        const candidate = join(base, "vendor", target, folder, "codex.exe");
        if (existsSync(candidate)) return candidate;
      }
    }
  }
  throw new Error("找不到原生 Codex CLI");
}

function connection(child) {
  const lines = createInterface({ input: child.stdout });
  const closed = new Promise(resolve => child.once("close", resolve));
  let active;
  let id = 0;
  const fail = error => active?.reject(error);
  child.on("error", fail);
  child.stdin.on("error", fail);
  child.on("exit", () => fail(new Error("Codex App Server 已退出")));
  lines.on("line", line => {
    if (!active) return;
    try {
      const message = JSON.parse(line);
      if (message.id !== active.id || message.method) return;
      if (message.error) fail(new Error("Codex App Server 拒绝了请求"));
      else active.resolve(message.result);
    } catch (error) { fail(error); }
  });

  function write(message) { child.stdin.write(JSON.stringify(message) + "\n"); }
  async function call(method, params) {
    const requestId = ++id;
    let timer;
    try {
      return await new Promise((resolve, reject) => {
        active = { id: requestId, resolve, reject };
        timer = setTimeout(() => reject(new Error("Codex App Server 响应超时")), 2800);
        write({ id: requestId, method, params });
      });
    } finally {
      clearTimeout(timer);
      active = undefined;
    }
  }

  async function close() {
    lines.close();
    child.stdin.destroy();
    child.stdout.destroy();
    child.kill();
    await closed;
  }
  return { call, write, close };
}

export async function withAppServer(binary, action, environment = process.env) {
  const { CODEX_THREAD_ID, CODEX_SESSION_ID, CODEX_APP_TOOLS_PIPE_PATH, ...env } = environment;
  const child = spawn(binary, ["app-server", "--disable", "hooks"], {
    env: { ...env, ATM_CODEX_ADAPTER: "1" },
    stdio: ["pipe", "pipe", "ignore"], windowsHide: true,
  });
  const { call, write, close } = connection(child);
  try {
    await call("initialize", { clientInfo: { name: "agent-title-manager", version: "0.1.0" }, capabilities: { experimentalApi: true } });
    write({ method: "initialized" });
    return await action(call);
  } finally { await close(); }
}
