import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import test from "node:test";
import { withAppServer } from "../codex/app-server.mjs";

async function isolated(t) {
  const root = await mkdtemp(join(tmpdir(), "atm-native-test-"));
  t.after(() => rm(root, { recursive: true, force: true, maxRetries: 8, retryDelay: 200 }));
  const cwd = join(root, "project");
  await mkdir(cwd);
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    /^(?:PATH|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|SYSTEMDRIVE)$/i.test(key)));
  for (const key of ["HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP"]) env[key] = root;
  return { cwd, env, root };
}

const codex = process.env.ATM_TEST_CODEX_EXE;
test("真实 Codex App Server 在隔离目录设置并读回原生标题", { skip: !codex, timeout: 15_000 }, async t => {
  const { cwd, env, root } = await isolated(t);
  env.CODEX_HOME = join(root, "codex");
  await mkdir(env.CODEX_HOME);
  await withAppServer(codex, async call => {
    const { thread } = await call("thread/start", { cwd, ephemeral: false, approvalPolicy: "never" });
    assert.equal(typeof thread.id, "string");
    await call("thread/name/set", { threadId: thread.id, name: "隔离的 Codex 标题 🧪" });
    const read = await call("thread/read", { threadId: thread.id, includeTurns: false });
    assert.equal(read.thread.id, thread.id);
    assert.equal(read.thread.name, "隔离的 Codex 标题 🧪");
  }, env);
});

function piProcess(cli, extension, { cwd, env }) {
  const child = spawn(process.execPath, [cli, "--mode", "rpc", "--no-session", "--no-extensions", "--extension", extension], {
    cwd, env, windowsHide: true, stdio: ["pipe", "pipe", "ignore"],
  });
  const closed = new Promise(resolve => child.once("close", resolve));
  const lines = createInterface({ input: child.stdout });
  const notifyEvents = [];
  const calls = new Map();
  let sequence = 0;
  lines.on("line", line => {
    const message = JSON.parse(line);
    if (message.type === "extension_ui_request" && message.method === "notify") notifyEvents.push(message);
    if (message.id && calls.has(message.id)) {
      calls.get(message.id)(message);
      calls.delete(message.id);
    }
  });
  const request = async (type, params) => {
    const id = "atm-test-" + (++sequence);
    const response = new Promise(resolve => calls.set(id, resolve));
    child.stdin.write(JSON.stringify({ id, type, ...params }) + "\n");
    return Promise.race([response, closed.then(() => { throw new Error("Pi RPC 已退出"); })]);
  };
  return { request, notifyEvents, close: async () => { child.kill(); await closed; } };
}

const pi = process.env.ATM_TEST_PI_CLI;
test("真实 Pi 加载扩展并通过 RPC 写回原生标题", { skip: !pi, timeout: 15_000 }, async t => {
  const paths = await isolated(t);
  paths.env.PI_CODING_AGENT_DIR = join(paths.root, "pi-home");
  const packagePath = join(paths.root, "pi-package");
  await cp(new URL("../pi/", import.meta.url), packagePath, { recursive: true });
  // 用错配运行配置触发实际扩展的本地通知，以区分已加载和只启动了 Pi。
  await writeFile(join(packagePath, "atm-runtime.json"), "{}");
  const { request, notifyEvents, close } = piProcess(pi, join(packagePath, "index.js"), paths);
  try {
    const before = await request("get_state", {});
    assert.equal(before.success, true);
    assert.equal(typeof before.data.sessionId, "string");
    assert.ok(notifyEvents.some(event => event.message === "标题管理器缺少桥接程序配置。"));
    assert.equal((await request("set_session_name", { name: "隔离的 Pi 标题 🧪" })).success, true);
    assert.equal((await request("get_state", {})).data.sessionName, "隔离的 Pi 标题 🧪");
  } finally { await close(); }
});
