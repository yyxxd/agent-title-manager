import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { identify, snapshot, originalGoal, waitSnapshot } from "../antigravity/transcript.mjs";
import { readTitle, writeTitle } from "../antigravity/annotations.mjs";
import { handleAntigravity } from "../antigravity/index.mjs";

async function fixture(t, rows) {
  const root = await mkdtemp(join(tmpdir(), "atm-antigravity-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const id = "conversation_1";
  const path = join(root, "brain", id, ".system_generated", "logs", "transcript.jsonl");
  await mkdir(join(root, "brain", id, ".system_generated", "logs"), { recursive: true });
  await writeFile(path, rows.map(row => JSON.stringify(row)).join("\n"));
  const identified = identify({ conversationId: id, transcriptPath: path }, root);
  return { root, id, path, identified, input: { conversationId: id, transcriptPath: path } };
}

test("Antigravity transcript extracts request and title origin", async t => {
  const f = await fixture(t, [
    { type: "USER_INPUT", source: "USER_EXPLICIT", step_index: 1, content: "<USER_REQUEST>修复标题</USER_REQUEST><ADDITIONAL_METADATA>noise</ADDITIONAL_METADATA>" },
    { type: "PLANNER_RESPONSE", source: "MODEL", step_index: 2, content: "已完成" },
  ]);
  assert.equal(originalGoal(f.identified), "修复标题");
  assert.equal(snapshot(f.identified).titleOrigin, "auto");
  assert.equal(snapshot(f.identified).confirmationOnly, false);
});

test("Antigravity confirmationOnly requires a later pure confirmation", async t => {
  const f = await fixture(t, [
    { type: "USER_INPUT", source: "USER_EXPLICIT", step_index: 1, content: "开始工作" },
    { type: "PLANNER_RESPONSE", source: "MODEL", step_index: 2, content: "请确认" },
    { type: "USER_INPUT", source: "USER_EXPLICIT", step_index: 3, content: "好的" },
    { type: "PLANNER_RESPONSE", source: "MODEL", step_index: 4, content: "继续" },
  ]);
  const state = snapshot(f.identified);
  assert.equal(state.titleOrigin, "auto");
  assert.equal(state.confirmationOnly, true);
});

test("Antigravity annotation write preserves non-title fields and escapes text", async t => {
  const f = await fixture(t, []);
  await mkdir(join(f.root, "annotations"), { recursive: true });
  await writeFile(join(f.root, "annotations", `${f.id}.pbtxt`), "foo:1\ntitle:\"old\"\nbar:\"x\"\n");
  assert.equal(writeTitle(f.root, f.id, '新\\标题"'), true);
  const raw = await readFile(join(f.root, "annotations", `${f.id}.pbtxt`), "utf8");
  assert.match(raw, /foo:1/); assert.match(raw, /bar:\"x\"/);
  assert.equal(readTitle(f.root, f.id), '新\\标题"');
});

test("Antigravity Stop submits, claims, writes, and acks", async t => {
  const f = await fixture(t, [
    { type: "USER_INPUT", source: "USER_EXPLICIT", step_index: 1, content: "开始" },
    { type: "PLANNER_RESPONSE", source: "MODEL", step_index: 2, content: "完成" },
  ]);
  process.env.ANTIGRAVITY_DATA_DIR = f.root;
  t.after(() => delete process.env.ANTIGRAVITY_DATA_DIR);
  const calls = [];
  const candidate = { id: "candidate-1", title: "新标题", baseTitle: "", revision: snapshot(f.identified).revision };
  const rpc = async (method) => { calls.push(method); if (method === "title.claim") return { candidate, status: "ready" }; if (method === "title.ack") return { accepted: true, syncStatus: "unbound" }; return {}; };
  const out = await handleAntigravity("stop", { ...f.input, conversationId: f.id, transcriptPath: f.path, hookEventName: "Stop" }, rpc, () => {}, async () => ({}));
  assert.deepEqual(out, { decision: "" });
  assert.equal(readTitle(f.root, f.id), "新标题");
  assert.ok(calls.includes("event.submit") && calls.includes("title.claim") && calls.includes("title.ack"));
});

test("Antigravity SessionStart claims before native fallback", async t => {
  const f = await fixture(t, [
    { type: "USER_INPUT", source: "USER_EXPLICIT", step_index: 1, content: "开始" },
    { type: "PLANNER_RESPONSE", source: "MODEL", step_index: 2, content: "完成" },
  ]);
  process.env.ANTIGRAVITY_DATA_DIR = f.root;
  t.after(() => delete process.env.ANTIGRAVITY_DATA_DIR);
  writeTitle(f.root, f.id, "引擎初始标题");
  const state = snapshot(f.identified);
  const candidate = { id: "candidate-2", title: "管理器标题", baseTitle: state.currentTitle, revision: state.revision };
  let backgroundPayload;
  const rpc = async method => method === "title.claim" ? { candidate } : {};
  await handleAntigravity("start", { ...f.input, hookEventName: "SessionStart" }, rpc, (_mode, payload) => { backgroundPayload = payload; }, async () => { throw new Error("native fallback should not run"); });
  assert.equal(backgroundPayload.title, "管理器标题");
});

test("Antigravity Stop ignores continuation and failed termination", async t => {
  const f = await fixture(t, [{ type: "USER_INPUT", source: "USER_EXPLICIT", step_index: 1, content: "开始" }, { type: "PLANNER_RESPONSE", source: "MODEL", step_index: 2, content: "完成" }]);
  process.env.ANTIGRAVITY_DATA_DIR = f.root;
  t.after(() => delete process.env.ANTIGRAVITY_DATA_DIR);
  for (const extra of [{ continuationActive: true }, { terminationReason: "ERROR" }, { terminationReason: "USER_CANCELED" }]) {
    let called = false;
    await handleAntigravity("stop", { ...f.input, hookEventName: "Stop", ...extra }, async () => { called = true; return {}; }, () => {}, async () => ({}));
    assert.equal(called, false);
  }
});

test("Antigravity edits a single-line annotation without touching adjacent fields", async t => {
  const f = await fixture(t, []);
  const dir = join(f.root, "annotations");
  const file = join(dir, f.id + ".pbtxt");
  assert.equal(readTitle(f.root, f.id), "");
  await mkdir(dir);
  const suffix = ' last_user_view_time:{seconds:10 nanos:5} permission_preset:3 meta:{title:"other"}\r\n';
  await writeFile(file, 'title:"\\344\\270\\255\\"\\\\"' + suffix);
  assert.equal(readTitle(f.root, f.id), '中"\\');
  assert.equal(writeTitle(f.root, f.id, '藏\\文"', "wrong baseline"), false);
  assert.equal(writeTitle(f.root, f.id, '藏\\文"', '中"\\'), true);
  assert.equal(await readFile(file, "utf8"), 'title:"藏\\\\文\\""' + suffix);
  const missing = 'permission_preset:3\r\n';
  await writeFile(file, missing);
  assert.equal(writeTitle(f.root, f.id, "新增", ""), true);
  assert.equal(await readFile(file, "utf8"), missing + 'title:"新增"\r\n');
  await writeFile(file, 'title:"unterminated');
  assert.throws(() => readTitle(f.root, f.id), /title field/);
  assert.throws(() => writeTitle(f.root, f.id, "ignored"), /title field/);
  await rm(file);
  await mkdir(file);
  assert.throws(() => readTitle(f.root, f.id), /file/);
});

const user = (step, content) => ({ type: "USER_INPUT", source: "USER_EXPLICIT", step_index: step, content });
const answer = (step, content, extras) => ({ type: "PLANNER_RESPONSE", source: "MODEL", step_index: step, content, ...extras });

test("Antigravity waits for a complete response to the most recent explicit input", async t => {
  const prior = [user(1, "first"), answer(2, "old answer"), user(3, "new request"), answer(4, "interim", { tool_calls: [{}] })];
  const f = await fixture(t, prior);
  assert.equal(snapshot(f.identified), undefined);
  const serialized = prior.map(row => JSON.stringify(row)).join("\n");
  const waits = [];
  const result = await waitSnapshot(f.identified, f.input, { pause: async ms => {
    waits.push(ms);
    const next = JSON.stringify(answer(5, "new answer", { status: "DONE" }));
    await writeFile(f.path, serialized + "\n" + (waits.length === 1 ? next.slice(0, -4) : next));
  } });
  assert.deepEqual(waits, [150, 350]);
  assert.deepEqual(result.state.turns.slice(-2), [{ role: "user", text: "new request" }, { role: "assistant", text: "new answer" }]);
  assert.equal(result.state.titleOrigin, "auto");
  writeTitle(f.root, f.id, "manual title");
  assert.equal(snapshot(f.identified).titleOrigin, "unknown");
});

test("Antigravity expands the tail window to keep a long tool turn paired", async t => {
  const f = await fixture(t, [
    user(1, "first"), answer(2, "old answer"), user(3, "request with long tools"),
    { type: "GENERIC", source: "MODEL", data: "x".repeat(1024 * 1024 + 100) },
    answer(5, "paired answer"),
  ]);
  assert.deepEqual(snapshot(f.identified).turns.slice(-2).map(row => row.text), ["request with long tools", "paired answer"]);
});

test("Antigravity rejects an old answer appended after a new user step", async t => {
  const f = await fixture(t, [user(1, "first"), answer(2, "old answer"), user(3, "new request"), answer(2, "old answer")]);
  assert.equal(snapshot(f.identified), undefined);
});
