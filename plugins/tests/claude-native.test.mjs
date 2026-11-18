import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";
import { promisify } from "node:util";

const exec = promisify(execFile);
const claude = process.env.ATM_TEST_CLAUDE_EXE;
const fixtureTitle = "Claude readback fixture title";
const fixtureOutput = "Offline test response.";

async function endpoint(t) {
  const server = createServer(async (request, response) => {
    const chunks = [];
    for await (const part of request) chunks.push(part);
    if (!request.url?.startsWith("/v1/messages")) { response.writeHead(404).end(); return; }
    const model = JSON.parse(Buffer.concat(chunks)).model;
    response.writeHead(200, { "content-type": "text/event-stream" });
    const emit = event => response.write("event: " + event.type + "\ndata: " + JSON.stringify(event) + "\n\n");
    emit({ type: "message_start", message: { id: "msg_fixture", type: "message", role: "assistant", model,
      content: [], stop_reason: null, stop_sequence: null, usage: { input_tokens: 5, output_tokens: 0 } } });
    emit({ type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
    emit({ type: "content_block_delta", index: 0, delta: { type: "text_delta", text: fixtureOutput } });
    emit({ type: "content_block_stop", index: 0 });
    emit({ type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 2 } });
    emit({ type: "message_stop" });
    response.end();
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return "http://127.0.0.1:" + server.address().port;
}

async function setupClaude(t) {
  const root = await mkdtemp(join(tmpdir(), "atm-claude-native-"));
  t.after(() => rm(root, { recursive: true, force: true, maxRetries: 8, retryDelay: 200 }));
  const cwd = join(root, "project");
  await mkdir(cwd);
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    /^(?:PATH|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|SYSTEMDRIVE)$/i.test(key)));
  for (const key of ["HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "CLAUDE_CONFIG_DIR"]) env[key] = root;
  env.ANTHROPIC_API_KEY = randomUUID();
  env.ANTHROPIC_BASE_URL = await endpoint(t);
  env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = "1";
  const settings = join(root, "test-settings.json");
  const run = args => exec(claude, ["--print", "--setting-sources", "", "--settings", settings, ...args],
    { env, cwd, timeout: 12_000, windowsHide: true });
  const setHook = (event, path, args) => writeFile(settings, JSON.stringify({
    hooks: { [event]: [{ hooks: [{ type: "command", command: '"' + process.execPath + '" "' + path + '" ' + args, timeout: 5 }] }] },
  }));
  return { root, cwd, run, setHook };
}

async function seedSession({ root, run, setHook }) {
  const record = join(root, "hook-input.json");
  const hook = join(root, "record.mjs");
  await writeFile(hook, [
    'import { readFileSync, writeFileSync } from "node:fs";',
    "writeFileSync(" + JSON.stringify(record) + ", readFileSync(0));",
    'process.stdout.write("{}");',
  ].join("\n"));
  const id = randomUUID();
  await setHook("SessionStart", hook, "");
  const result = await run(["--session-id", id, "Create a temporary test conversation."]);
  assert.match(result.stdout, /Offline test response/);
  const input = JSON.parse(await readFile(record, "utf8"));
  assert.equal(input.session_id, id);
  assert.equal(dirname(input.transcript_path).startsWith(root), true);
  return { id, transcript: input.transcript_path };
}

async function addBridgeFixture(root, cwd) {
  const packagePath = join(root, "claude-package");
  await cp(new URL("../claude/", import.meta.url), packagePath, { recursive: true });
  await writeFile(join(packagePath, "atm-runtime.json"), JSON.stringify({ bridgePath: process.execPath }));
  await writeFile(join(cwd, "rpc"), [
    'const fs = require("node:fs");',
    'const input = JSON.parse(fs.readFileSync(0, "utf8"));',
    'fs.appendFileSync("bridge-methods", JSON.stringify(input) + "\\n");',
    'const candidate = { id: "isolated-claim", title: ' + JSON.stringify(fixtureTitle) + ', baseTitle: "", revision: input.params.revision };',
    'console.log(JSON.stringify({ ok: true, result: input.method === "title.claim" ? { candidate } : {} }));',
  ].join("\n"));
  return join(packagePath, "index.mjs");
}

test("真实 Claude Code 处理 UserPromptSubmit 并经原生读回发出 ack", { skip: !claude, timeout: 30_000 }, async t => {
  const context = await setupClaude(t);
  const { id, transcript } = await seedSession(context);
  const adapter = await addBridgeFixture(context.root, context.cwd);
  await context.setHook("UserPromptSubmit", adapter, "prompt");
  await context.run(["--resume", id, "Check the title in this temporary conversation."]);
  const log = join(context.cwd, "bridge-methods");
  const deadline = Date.now() + 3500;
  let bridgeEvents = [];
  do {
    bridgeEvents = (await readFile(log, "utf8").catch(() => "")).trim().split("\n").filter(Boolean).map(line => JSON.parse(line));
    if (bridgeEvents.some(event => event.method === "title.ack")) break;
    await delay(150);
  } while (Date.now() < deadline);
  assert.deepEqual(bridgeEvents.map(event => event.method), ["title.claim", "title.ack"]);
  assert.equal(bridgeEvents[1].params.observedTitle, fixtureTitle);
  assert.ok((await readFile(transcript, "utf8")).includes(fixtureTitle));
});
