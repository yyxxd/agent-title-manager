import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { identify, snapshot, originalGoal } from "../antigravity/transcript.mjs";
import { readTitle, writeTitle } from "../antigravity/annotations.mjs";

async function fixture(t, rows) {
  const root = await mkdtemp(join(tmpdir(), "atm-antigravity-"));
  const id = "conversation_1";
  const path = join(root, "brain", id, ".system_generated", "logs", "transcript.jsonl");
  await mkdir(join(root, "brain", id, ".system_generated", "logs"), { recursive: true });
  await writeFile(path, rows.map(row => JSON.stringify(row)).join("\n"));
  const identified = identify({ conversationId: id, transcriptPath: path }, root);
  return { root, id, path, identified };
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
