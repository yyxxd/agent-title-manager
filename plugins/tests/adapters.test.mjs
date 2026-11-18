import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { handleCodex } from "../codex/index.mjs";
import { handleClaude } from "../claude/index.mjs";
import { register } from "../pi/index.js";
import { nameSession } from "../example/index.mjs";
import { ack } from "../shared/client.mjs";

const binding = async () => ({});
const turnId = "fixture-turn";
const sessionId = "fixture-session";
const latest = { id: turnId, status: "completed", items: [
  { type: "userMessage", content: [{ type: "text", text: "修复标题同步" }] },
  { type: "agentMessage", phase: "commentary", text: "进展通知" },
  { type: "agentMessage", phase: "final_answer", text: "已修复标题同步" },
] };

test("Codex 按 Stop 的原生 turn 读写，完成原生读回后才确认", async () => {
  let title = "";
  const methods = [];
  const rpc = async (method, params) => {
    methods.push([method, params]);
    if (method === "event.submit") return { accepted: true };
    if (method === "title.claim") return { candidate: { id: "claim", title: "新标题", baseTitle: "", revision: turnId } };
    assert.equal(title, "新标题");
  };
  const appServer = async (method, params) => {
    if (method === "thread/name/set") { title = params.name; return {}; }
    assert.equal(params.threadId, sessionId);
    return { thread: { id: sessionId, name: title, turns: [latest] } };
  };
  await handleCodex("stop", { session_id: sessionId, turn_id: turnId }, rpc, appServer, binding);
  assert.deepEqual(methods.map(([method]) => method), ["event.submit", "title.claim", "title.ack"]);
  assert.deepEqual(methods[0][1].turns.map(turn => turn.text), ["修复标题同步", "已修复标题同步"]);
  assert.equal(methods[2][1].observedTitle, "新标题");
});

test("Codex 拒绝旧 Stop 以及候选到达期间更新的原标题", async () => {
  let title = "";
  const reads = async method => {
    assert.equal(method, "thread/read");
    return { thread: { id: sessionId, name: title, turns: [latest] } };
  };
  const rpc = async method => {
    if (method === "event.submit") return { accepted: true };
    assert.equal(method, "title.claim");
    title = "手动更名";
    return { candidate: { id: "claim", title: "新标题", baseTitle: "", revision: turnId } };
  };
  await handleCodex("stop", { session_id: sessionId, turn_id: "old" }, async () => assert.fail("旧 Stop 不得提交"), reads, binding);
  await handleCodex("stop", { session_id: sessionId, turn_id: turnId }, rpc, reads, binding);
  assert.equal(title, "手动更名");
});

test("Codex 打开已命名会话时只发送可验证的原生标题", async () => {
  const calls = [];
  const rpc = async (method, params) => { calls.push({ method, params }); };
  const getThread = async (method, params) => {
    assert.equal(method, "thread/read");
    assert.equal(params.includeTurns, false);
    return { thread: { id: sessionId, name: "原生标题" } };
  };
  const orca = async () => ({ orcaSessionId: "raw", orcaTitle: "", orcaTitleOrigin: "unknown", orcaNativeHandle: sessionId });
  await handleCodex("start", { session_id: sessionId }, rpc, getThread, orca);
  await handleCodex("start", { session_id: sessionId }, rpc, getThread, orca);
  assert.deepEqual(calls[0], calls[1]);
  assert.equal(calls[0].method, "event.submit");
  assert.equal(calls[0].params.nativeTitleOnly, true);
  assert.deepEqual(calls[0].params.turns, []);
  assert.equal(calls[0].params.currentTitle, "原生标题");
});

async function claudeFixture(t, records = []) {
  const root = await mkdtemp(join(tmpdir(), "atm-claude-fixture-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const project = join(root, "projects", "fixture");
  const path = join(project, sessionId + ".jsonl");
  await mkdir(project, { recursive: true });
  const entries = [
    { type: "user", uuid: "user-1", message: { content: "实现校验" } },
    { type: "assistant", uuid: "assistant-1", message: { content: [{ type: "text", text: "已实现" }] } },
    ...records,
  ];
  await writeFile(path, entries.map(entry => JSON.stringify({ sessionId, ...entry })).join("\n") + "\n");
  return { root, path, input: { session_id: sessionId, transcript_path: path } };
}

test("Claude Stop 提交，Prompt 返回宿主标题，独立观察者读回才 ack", async t => {
  const { root, path, input } = await claudeFixture(t);
  const tasks = [];
  let submitted;
  const events = [];
  const rpc = async (method, params) => {
    events.push(method);
    if (method === "event.submit") { submitted = params; return { accepted: true }; }
    if (method === "title.claim") {
      assert.equal(params.revision, submitted.revision);
      return { candidate: { id: "claim", title: "验证标题", baseTitle: "", revision: params.revision } };
    }
    assert.deepEqual(params, { agent: "claude", sessionId, id: "claim", observedTitle: "验证标题" });
  };
  const background = (mode, payload) => tasks.push([mode, payload]);
  await handleClaude("stop", { ...input, hook_event_name: "Stop" }, rpc, background, binding, root);
  const response = await handleClaude("prompt", { ...input, hook_event_name: "UserPromptSubmit" }, rpc, background, binding, root);
  assert.equal(response.hookSpecificOutput.sessionTitle, "验证标题");
  assert.deepEqual(events, ["event.submit", "title.claim"]);
  assert.equal(tasks[0][0], "confirm");
  await writeFile(path, JSON.stringify({ sessionId, type: "custom-title", customTitle: "验证标题" }) + "\n", { flag: "a" });
  await handleClaude(...tasks[0], rpc, background, binding, root);
  assert.deepEqual(events, ["event.submit", "title.claim", "title.ack"]);
});

test("Claude 新一轮或不同的原生标题阻止写回和确认", async t => {
  const { root, input } = await claudeFixture(t, [{ type: "custom-title", customTitle: "手动名称" }]);
  const rpc = async method => assert.fail("不应发送后台请求 " + method);
  assert.deepEqual(await handleClaude("prompt", {
    ...input, hook_event_name: "UserPromptSubmit", session_title: "上一次标题",
  }, rpc, undefined, binding, root), {});
  await handleClaude("confirm", { ...input, id: "claim", title: "候选", baseTitle: "" }, rpc, undefined, binding, root);
});

function piFixture() {
  let title;
  let leaf = "leaf";
  const listeners = new Map();
  const observations = [];
  const pi = {
    on: (name, callback) => listeners.set(name, callback),
    getSessionName: () => title,
    setSessionName: name => { title = name; },
  };
  const ctx = {
    sessionManager: {
      getSessionId: () => sessionId,
      getLeafId: () => leaf,
      getSessionFile: () => undefined,
      getSessionName: () => title,
      getBranch: () => [{ type: "message", message: { role: "user", content: "核对 Pi 会话" } }],
    },
    isIdle: () => true,
    ui: { notify: message => observations.push(message) },
  };
  return { pi, ctx, listeners, observations, nextLeaf: () => { leaf = "different"; } };
}

test("Pi 只在当前空闲分支应用，读回后确认并通知", async () => {
  const { pi, ctx, listeners, observations } = piFixture();
  let confirmed;
  const completion = new Promise(resolve => { confirmed = resolve; });
  register(pi, async (method, params) => {
    if (method === "event.submit") return { accepted: true };
    if (method === "title.claim") return { candidate: { id: "claim", title: "Pi 新标题", baseTitle: "", revision: params.revision } };
    confirmed(params);
  }, binding);
  assert.equal(listeners.get("agent_settled")({ aborted: false }, ctx), undefined);
  assert.equal((await completion).observedTitle, "Pi 新标题");
  assert.deepEqual(observations, ["会话已命名为 Pi 新标题"]);
});

test("Pi 切换分支后丢弃已返回的候选", async () => {
  const { pi, ctx, listeners, nextLeaf, observations } = piFixture();
  register(pi, async (method, params) => {
    if (method === "event.submit") return { accepted: true };
    assert.equal(method, "title.claim");
    nextLeaf();
    return { candidate: { id: "claim", title: "旧候选", baseTitle: "", revision: params.revision } };
  }, binding);
  listeners.get("agent_settled")({ aborted: false }, ctx);
  await delay(5);
  assert.equal(pi.getSessionName(), undefined);
  assert.deepEqual(observations, []);
});

test("独立插件要求实际条件写入和标题读回", async () => {
  const initial = { sessionId, revision: turnId, currentTitle: "", turns: [{ role: "user", text: "核对" }] };
  let title = "";
  const calls = [];
  const host = {
    read: async () => ({ ...initial, currentTitle: title }),
    applyIfCurrent: async (before, name) => {
      assert.equal(before.revision, initial.revision);
      title = name;
      return true;
    },
  };
  const rpc = async (method, params) => {
    assert.equal(params.agent, "example");
    calls.push(method);
    if (method === "event.submit") return { accepted: true };
    if (method === "title.claim") return { candidate: { id: "claim", title: "外部宿主标题", baseTitle: "", revision: turnId } };
    assert.equal(params.observedTitle, title);
  };
  assert.deepEqual(await nameSession(host, rpc), { status: "applied" });
  assert.deepEqual(calls, ["event.submit", "title.claim", "title.ack"]);
});

test("回执短暂失败时只重试相同 ID 的 title.ack", async () => {
  const methods = [];
  const send = async (method, params) => {
    methods.push([method, params]);
    if (methods.length === 1) throw new Error("桥接暂时离线");
    return { applied: true };
  };
  await ack(send, { agent: "pi", sessionId }, { id: "claim" }, "刚读回的标题");
  assert.equal(methods.length, 2);
  assert.deepEqual(methods[0], methods[1]);
  assert.deepEqual(methods[0], ["title.ack", { agent: "pi", sessionId, id: "claim", observedTitle: "刚读回的标题" }]);
});

test("四个发布包都有独立 manifest 和运行入口", async () => {
  for (const [id, host] of [["codex", "codex"], ["claude", "claude"], ["pi", "pi"], ["example", "external"]]) {
    const root = new URL("../" + id + "/", import.meta.url);
    const manifest = JSON.parse(await readFile(new URL("agent-title-plugin.json", root), "utf8"));
    assert.equal(manifest.schemaVersion, 1);
    assert.equal(manifest.id, id);
    assert.equal(manifest.host, host);
    assert.equal(manifest.kind, "agent");
    assert.match(await readFile(new URL(manifest.entry, root), "utf8"), /from "\.\/client\.mjs"/);
  }
});
