import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { handleCodex } from "../codex/index.mjs";
import { handleClaude } from "../claude/index.mjs";
import { identify, readObservedTitle } from "../claude/transcript.mjs";
import { register } from "../pi/index.js";
import { nameSession } from "../example/index.mjs";
import { ack } from "../shared/client.mjs";

const native = call => action => action(call);
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
  await handleCodex("stop", { session_id: sessionId, turn_id: turnId }, rpc, native(appServer));
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
  await handleCodex("stop", { session_id: sessionId, turn_id: "old" }, async () => assert.fail("旧 Stop 不得提交"), native(reads));
  await handleCodex("stop", { session_id: sessionId, turn_id: turnId }, rpc, native(reads));
  assert.equal(title, "手动更名");
});

test("Codex 打开已命名会话时只发送可验证的原生标题", async () => {
  const calls = [];
  const rpc = async (method, params) => { calls.push({ method, params }); };
  const getThread = async (method, params) => {
    assert.equal(method, "thread/read");
    return { thread: { id: sessionId, name: "原生标题" } };
  };
  await handleCodex("start", { session_id: sessionId }, rpc, native(getThread));
  await handleCodex("start", { session_id: sessionId }, rpc, native(getThread));
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
  await handleClaude("stop", { ...input, hook_event_name: "Stop" }, rpc, background, root);
  const response = await handleClaude("prompt", { ...input, hook_event_name: "UserPromptSubmit" }, rpc, background, root);
  assert.equal(response.hookSpecificOutput.sessionTitle, "验证标题");
  assert.deepEqual(events, ["event.submit", "title.claim"]);
  assert.equal(tasks[0][0], "confirm");
  await writeFile(path, JSON.stringify({ sessionId, type: "custom-title", customTitle: "验证标题" }) + "\n", { flag: "a" });
  await handleClaude(...tasks[0], rpc, background, root);
  assert.deepEqual(events, ["event.submit", "title.claim", "title.ack"]);
});

test("Claude 新一轮或不同的原生标题阻止写回和确认", async t => {
  const { root, input } = await claudeFixture(t, [{ type: "custom-title", customTitle: "手动名称" }]);
  const rpc = async method => assert.fail("不应发送后台请求 " + method);
  assert.deepEqual(await handleClaude("prompt", {
    ...input, hook_event_name: "UserPromptSubmit", session_title: "上一次标题",
  }, rpc, undefined, root), {});
  await handleClaude("confirm", { ...input, id: "claim", title: "候选", baseTitle: "" }, rpc, undefined, root);
});

test("Claude UserPromptSubmit 遇到仍在入队的候选只领取一次", async t => {
  const { root, input } = await claudeFixture(t);
  let claims = 0;
  const result = await handleClaude("prompt", { ...input, hook_event_name: "UserPromptSubmit" }, async method => {
    assert.equal(method, "title.claim");
    claims++;
    return { status: "ingress_pending", candidate: null };
  }, () => assert.fail("没有候选时不启动读回观察"), root);
  assert.deepEqual(result, {});
  assert.equal(claims, 1);
});

test("Claude 确认轮忽略宿主记账，保留附件、工具及额外上下文", async t => {
  const attachment = fields => ({ type: "attachment", attachment: fields });
  const promptSnapshot = attachment({ type: "prompt_snapshot", systemPrompt: ["Fixture system prompt"], tools: [],
    reminderFold: false, echoWireToolInputs: true, contextRendering: "fixture" });
  const hook = { type: "hook_success", hookEvent: "Stop", content: "", stdout: "{}\n", stderr: "", exitCode: 0 };
  const cases = [
    ["纯确认", [], [], true],
    ["确认前的文件", [attachment({ type: "file" })], []],
    ["确认后的图片", [], [attachment({ type: "image" })]],
    ["额外上下文", [], [attachment({ type: "instructions", files: [{ content: "Fixture context" }] })]],
    ["未知附件", [], [attachment({ type: "unknown" })]],
    ["未知快照字段", [], [attachment({ ...promptSnapshot.attachment, content: "Fixture context" })]],
    ["Hook 上下文", [attachment({ ...hook, content: "Fixture context" })], []],
    ["Hook 输出", [], [attachment({ ...hook, stdout: '{"additionalContext":"Fixture context"}' })]],
    ["Hook 错误输出", [], [attachment({ ...hook, stderr: "Fixture error" })]],
    ["工具 Hook", [], [attachment({ ...hook, hookEvent: "PostToolUse" })]],
    ["工具结果", [], [{ type: "user", uuid: "tool-result", message: { content: [{ type: "tool_result" }] } }]],
    ["工具调用", [], [{ type: "assistant", uuid: "tool-call", message: {
      content: [{ type: "text", text: "Checking fixture" }, { type: "tool_use" }],
    } }]],
    ["更换话题", [{ type: "user", uuid: "topic", message: { content: "Switch to another topic" } }], []],
  ];
  for (const [name, before, after, confirmationOnly] of cases) await t.test(name, async subtest => {
    const { root, input } = await claudeFixture(subtest, [
      promptSnapshot, attachment(hook), { type: "custom-title", customTitle: "验证标题" }, ...before,
      { type: "user", uuid: "latest-user", message: { content: [{ type: "text", text: "OK" }] } }, ...after,
      { type: "assistant", uuid: "latest-answer", message: { content: [{ type: "text", text: "Acknowledged" }] } },
      attachment(hook),
    ]);
    let submitted;
    await handleClaude("stop", { ...input, hook_event_name: "Stop" },
      async (method, params) => { assert.equal(method, "event.submit"); submitted = params; }, undefined, root);
    assert.equal(submitted.confirmationOnly, confirmationOnly);
    assert.equal(submitted.currentTitle, "验证标题");
  });
});

test("Claude 长记录从文件开头取目标；尾部确认有原生附件时保留正常处理", async t => {
  const fill = { type: "progress", data: "x".repeat(800 * 1024) };
  const boundary = { type: "assistant", uuid: "previous-answer", message: { content: "之前的完整答复" } };
  for (const attached of [false, true]) {
    const extra = attached ? [{ type: "attachment", attachment: { type: "image" } }] : [];
    const { root, input } = await claudeFixture(t, [fill, boundary, ...extra,
      { type: "user", uuid: "latest-user", message: { content: "继续。" } },
      { type: "assistant", uuid: "latest-answer", message: { content: "已继续处理" } },
    ]);
    let submitted;
    await handleClaude("stop", { ...input, hook_event_name: "Stop", session_title: "" },
      async (method, params) => { assert.equal(method, "event.submit"); submitted = params; }, undefined, root);
    assert.equal(submitted.originalGoal, "实现校验");
    assert.equal(submitted.confirmationOnly, attached ? undefined : true);
    assert.ok(!submitted.turns.some(turn => turn.text === "实现校验"));
  }
});

test("Claude 读不到开头的真实目标时保留空值，两个原生标题来源冲突时不确认", async t => {
  const { root, path, input } = await claudeFixture(t);
  const records = [
    { type: "progress", data: "x".repeat(800 * 1024) },
    { type: "user", uuid: "recent-user", message: { content: "尾部的新请求" } },
    { type: "assistant", uuid: "recent-assistant", message: { content: "尾部的答复" } },
    { type: "custom-title", customTitle: "记录标题" },
  ];
  await writeFile(path, records.map(record => JSON.stringify({ sessionId, ...record })).join("\n") + "\n");
  await handleClaude("stop", { ...input, hook_event_name: "Stop" }, async (_, params) => {
    assert.equal(params.originalGoal, undefined);
    assert.equal(params.turns[0].text, "尾部的新请求");
  }, undefined, root);
  const sidecar = join(dirname(path), sessionId, "custom-title.json");
  await mkdir(dirname(sidecar), { recursive: true });
  await writeFile(sidecar, JSON.stringify({ customTitle: "另一标题" }));
  assert.equal(readObservedTitle(identify(input, root)), undefined);
});

test("Claude 大会话没有标题记录时仍按无标题提交", async t => {
  const { root, path, input } = await claudeFixture(t);
  const records = [
    { type: "progress", data: "x".repeat(800 * 1024) },
    { type: "user", uuid: "recent-user", message: { content: "尾部的新请求" } },
    { type: "assistant", uuid: "recent-assistant", message: { content: "尾部的答复" } },
  ];
  await writeFile(path, records.map(record => JSON.stringify({ sessionId, ...record })).join("\n") + "\n");
  assert.equal(readObservedTitle(identify(input, root)), "");
  let submitted;
  await handleClaude("stop", { ...input, hook_event_name: "Stop" }, async (method, params) => {
    assert.equal(method, "event.submit");
    submitted = params;
  }, undefined, root);
  assert.equal(submitted.currentTitle, "");
  assert.equal(submitted.turns.at(-1).text, "尾部的答复");
});

test("Claude 大会话的标题记录在尾部窗口之前时沿用该标题", async t => {
  const { root, path, input } = await claudeFixture(t);
  const records = [
    { type: "custom-title", customTitle: "早期标题" },
    { type: "progress", data: "x".repeat(800 * 1024) },
    { type: "user", uuid: "recent-user", message: { content: "尾部的新请求" } },
    { type: "assistant", uuid: "recent-assistant", message: { content: "尾部的答复" } },
  ];
  await writeFile(path, records.map(record => JSON.stringify({ sessionId, ...record })).join("\n") + "\n");
  assert.equal(readObservedTitle(identify(input, root)), "早期标题");
  let submitted;
  await handleClaude("stop", { ...input, hook_event_name: "Stop" }, async (method, params) => {
    assert.equal(method, "event.submit");
    submitted = params;
  }, undefined, root);
  assert.equal(submitted.currentTitle, "早期标题");
});

function piFixture(initialBranch = [{ type: "message", message: { role: "user", content: "核对 Pi 会话" } }]) {
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
      getBranch: () => initialBranch,
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
  });
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
  });
  listeners.get("agent_settled")({ aborted: false }, ctx);
  await delay(5);
  assert.equal(pi.getSessionName(), undefined);
  assert.deepEqual(observations, []);
});

test("Pi 对 kept 保持静默，failed 只通知一次并保留原标题", async () => {
  for (const status of ["kept", "failed"]) {
    const { pi, ctx, listeners, observations } = piFixture();
    const methods = [];
    register(pi, async method => {
      methods.push(method);
      return method === "event.submit" ? { accepted: true } : { candidate: null, status };
    });
    listeners.get("agent_settled")({ aborted: false }, ctx);
    await delay(5);
    assert.deepEqual(methods, ["event.submit", "title.claim"]);
    assert.equal(pi.getSessionName(), undefined);
    assert.deepEqual(observations, status === "kept" ? [] : ["自动命名失败，当前标题保留。"]);
  }
});

test("Pi 恢复同一会话时领取迟到候选，无需重新提交", async () => {
  const { pi, ctx, listeners } = piFixture();
  let done;
  const result = new Promise(resolve => { done = resolve; });
  const methods = [];
  register(pi, async (method, params) => {
    methods.push(method);
    if (method === "title.claim") return { candidate: { id: "claim", title: "恢复后领取", baseTitle: "", revision: params.revision } };
    done(params);
  });
  listeners.get("session_start")({ reason: "resume" }, ctx);
  assert.equal((await result).observedTitle, "恢复后领取");
  assert.deepEqual(methods, ["title.claim", "title.ack"]);
});

test("Pi 从完整分支提取初始目标，最新纯文本确认和图片分别处理", async () => {
  const user = (id, content) => ({ id, type: "message", message: { role: "user", content } });
  for (const image of [false, true]) {
    const branch = Array.from({ length: 20 }, (_, index) => user("older-" + index, index === 0 ? "Pi 初始目标" : "中途事项 " + index));
    const { pi, ctx, listeners } = piFixture(branch);
    ctx.sessionManager.getLeafId = () => branch.at(-1).id;
    let submitted;
    register(pi, async (method, params) => {
      if (method === "event.submit") { submitted = params; return { accepted: true }; }
      return { candidate: null, status: "kept" };
    });
    listeners.get("before_agent_start")({}, ctx);
    const blocks = [{ type: "text", text: "继续！" }];
    if (image) blocks.push({ type: "image", data: "fixture" });
    branch.push(user("latest", blocks));
    listeners.get("agent_settled")({ aborted: false }, ctx);
    await delay(5);
    assert.equal(submitted.originalGoal, "Pi 初始目标");
    assert.equal(submitted.confirmationOnly, image ? undefined : true);
    assert.ok(!submitted.turns.some(turn => turn.text === "Pi 初始目标"));
  }
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
  for (const [id, host] of [["codex", "codex"], ["claude", "claude"], ["pi", "pi"], ["antigravity", "antigravity"], ["example", "external"]]) {
    const root = new URL("../" + id + "/", import.meta.url);
    const manifest = JSON.parse(await readFile(new URL("agent-title-plugin.json", root), "utf8"));
    assert.equal(manifest.schemaVersion, 1);
    assert.equal(manifest.id, id);
    assert.equal(manifest.host, host);
    assert.equal(manifest.kind, "agent");
    assert.match(await readFile(new URL(manifest.entry, root), "utf8"), /from "\.\/client\.mjs"/);
    if (host !== "external") {
      assert.equal(manifest.version, id === "antigravity" ? "0.3.0" : "0.2.0");
      for (const helper of ["orca.mjs", "orca-binding.mjs"]) {
        await assert.rejects(readFile(new URL(helper, root)), { code: "ENOENT" });
      }
      assert.doesNotMatch(await readFile(new URL(manifest.entry, root), "utf8"), /orcaSessionId|orcaTitle|orca\.exe/);
    }
  }
});

test("Claude ack leaves delayed binding entirely to the service", async t => {
  for (const [syncStatus, accepted] of [["pending", true], ["unbound", true], [undefined, false]]) {
    const { root, input } = await claudeFixture(t, [{ type: "custom-title", customTitle: "fixture-title" }]);
    const methods = [];
    const rpc = async (method, params) => { methods.push([method, params]); if (method === "title.ack") return { accepted, syncStatus }; return { accepted: true }; };
    await handleClaude("confirm", { ...input, id: "claim", title: "fixture-title", baseTitle: "" }, rpc, undefined, root);
    assert.equal(methods.filter(([method]) => method === "title.ack").length, 1);
    assert.equal(methods.filter(([method, params]) => method === "event.submit" && params.nativeTitleOnly).length, 0);
  }
});
