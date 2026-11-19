import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { identify, snapshot, originalGoal } from "../antigravity/transcript.mjs";
import { readTitle, writeTitle } from "../antigravity/annotations.mjs";
import { handleAntigravity } from "../antigravity/index.mjs";

async function fixture(t, rows) {
  const root = await mkdtemp(join(tmpdir(), "atm-antigravity-"));
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
  assert.equal(state.titleOrigin, "unknown");
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
