import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import test from "node:test";
import { readTitle, writeTitle } from "../antigravity/annotations.mjs";
import { processTurn } from "../antigravity/index.mjs";
import { createObserver, readACPSettlements, readDesktopSettlements } from "../antigravity/observe.mjs";
import { bindOrca } from "../antigravity/orca-binding.mjs";
import { roots, transcriptPath } from "../antigravity/paths.mjs";
import { identify, locateNative } from "../antigravity/transcript.mjs";

const desktopId = "a68b5391-bfed-45ae-948e-259bb4abf011";
const acpId = "b68b5391-bfed-45ae-948e-259bb4abf012";
const manualId = "b68b5391-bfed-45ae-948e-259bb4abf013";
const chatId = "e139bb8d-a50a-4de6-8bef-6fa674f005c1";
const manualChat = "e139bb8d-a50a-4de6-8bef-6fa674f005c2";

async function isolated(t) {
  const temp = await mkdtemp(join(tmpdir(), "atm-antigravity-observe-"));
  t.after(() => rm(temp, { recursive: true, force: true }));
  const orca = join(temp, "orca");
  await mkdir(orca);
  const environment = { USERPROFILE: temp, HOME: temp, ORCA_USER_DATA_PATH: orca, ANTIGRAVITY_DATA_DIR: undefined };
  const before = Object.fromEntries(Object.keys(environment).map(key => [key, process.env[key]]));
  for (const [key, value] of Object.entries(environment)) {
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
  t.after(() => {
    for (const [key, value] of Object.entries(before)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });
  return { temp, orca, desktop: roots().find(r => r.kind === "desktop"), acp: roots().find(r => r.kind === "acp") };
}

async function transcript(root, id, time) {
  const path = transcriptPath(root.dir, id);
  await mkdir(dirname(path), { recursive: true });
  const created_at = new Date(time).toISOString();
  const rows = [
    { type: "USER_INPUT", source: "USER_EXPLICIT", status: "DONE", step_index: 3, content: "request", created_at },
    { type: "PLANNER_RESPONSE", source: "MODEL", status: "DONE", step_index: 6, content: "answer", created_at },
  ];
  await writeFile(path, rows.map(row => JSON.stringify(row) + "\n").join(""));
  return path;
}

function summaries(path) {
  const db = new DatabaseSync(path);
  db.exec(`CREATE TABLE conversation_summaries (
    conversation_id TEXT PRIMARY KEY, title TEXT, preview TEXT, raw_summary BLOB, status TEXT,
    not_fully_idle INTEGER, step_count INTEGER, last_modified_time TEXT, last_user_input_step_index INTEGER
  );`);
  return db;
}

function seedSearchIndex(db, title) {
  const declaration = "CREATE VIRTUAL TABLE conversations_fts USING fts5(conversation_id UNINDEXED, chunk_index UNINDEXED, title)";
  try {
    db.exec(declaration);
    db.prepare("INSERT INTO conversations_fts VALUES (?,?,?)").run(desktopId, -1, title);
    return true;
  } catch (error) {
    if (error?.code !== "ERR_SQLITE_ERROR" || error.message !== "no such module: fts5") throw error;
    // 模拟由 Antigravity 创建的数据库，由缺少 FTS5 的随附 Node 打开。
    db.exec("PRAGMA writable_schema=ON");
    db.prepare("INSERT INTO sqlite_schema VALUES ('table','conversations_fts','conversations_fts',0,?)").run(declaration);
    db.exec("PRAGMA writable_schema=OFF");
    return false;
  }
}

const stringField = (field, value) => {
  const bytes = Buffer.from(value);
  assert.ok(bytes.length < 128);
  return Buffer.concat([Buffer.of(field * 8 + 2, bytes.length), bytes]);
};

test("summary writes preserve preview, unrelated protobuf bytes and other conversations", async t => {
  const { desktop } = await isolated(t);
  await mkdir(desktop.dir, { recursive: true });
  const file = join(desktop.dir, "conversation_summaries.db");
  const cache = join(desktop.dir, "agyhub_summaries_proto.pb");
  const preview = "keep this preview";
  const title = '改名 "新"';
  const pack = name => Buffer.concat([stringField(1, preview), stringField(9, "keep summary field"),
    stringField(15, Buffer.concat([stringField(1, name), stringField(2, "keep nested")]))]);
  const entry = (id, data) => stringField(1, Buffer.concat([stringField(1, id), stringField(2, data)]));
  const untouched = entry(acpId, pack("keep other"));
  const untitled = stringField(1, preview);
  const newlyTitled = Buffer.concat([untitled, stringField(15, stringField(1, title))]);
  const db = summaries(file);
  db.prepare("INSERT INTO conversation_summaries (conversation_id,title,preview,raw_summary) VALUES (?,?,?,?)")
    .run(desktopId, "old", preview, pack("old"));
  db.prepare("INSERT INTO conversation_summaries (conversation_id,title,preview,raw_summary) VALUES (?,?,?,?)")
    .run(manualId, "", preview, untitled);
  const hasFTS = seedSearchIndex(db, "old");
  db.close();
  await writeFile(cache, Buffer.concat([entry(desktopId, pack("old")), untouched, entry(manualId, untitled)]));
  assert.equal(writeTitle(desktop.dir, desktopId, title), true);
  assert.equal(writeTitle(desktop.dir, manualId, title, ""), true);
  const verify = new DatabaseSync(file, { readOnly: true });
  const query = verify.prepare("SELECT title,preview,raw_summary FROM conversation_summaries WHERE conversation_id = ?");
  const record = query.get(desktopId);
  assert.equal(record.title, title);
  assert.equal(record.preview, preview);
  assert.deepEqual(Buffer.from(record.raw_summary), pack(title));
  assert.equal(query.get(manualId).preview, preview);
  assert.deepEqual(Buffer.from(query.get(manualId).raw_summary), newlyTitled);
  if (hasFTS) assert.equal(verify.prepare("SELECT title FROM conversations_fts").get().title, title);
  else assert.throws(() => verify.prepare("SELECT title FROM conversations_fts").get(), /no such module: fts5/);
  verify.close();
  assert.deepEqual(await readFile(cache), Buffer.concat([entry(desktopId, pack(title)), untouched, entry(manualId, newlyTitled)]));
});

test("ACP rejects traversal, junctions and ambiguous native IDs", async t => {
  const { temp, acp, desktop } = await isolated(t);
  const path = await transcript(acp, acpId, Date.now());
  const input = { conversationId: acpId, transcriptPath: path };
  assert.equal(identify(input).kind, "acp");
  assert.equal(locateNative(acpId).appDataDir, acp.dir);
  assert.equal(identify({ ...input, transcriptPath: dirname(path) + "/../logs/transcript.jsonl" }), undefined);
  await transcript(desktop, acpId, Date.now());
  assert.equal(locateNative(acpId), undefined);
  const escaped = await transcript({ dir: join(temp, "other") }, manualId, Date.now());
  const nativeDir = join(acp.dir, "brain", manualId);
  await symlink(dirname(dirname(dirname(escaped))), nativeDir, process.platform === "win32" ? "junction" : "dir");
  assert.throws(() => identify({ conversationId: manualId, transcriptPath: transcriptPath(acp.dir, manualId) }), /redirected/);
});

test("observer processes only locally settled desktop and ACP turns, skipping manual Orca names", async t => {
  const { desktop, acp, orca } = await isolated(t);
  const now = Date.now(), at = now - 100;
  await transcript(desktop, desktopId, at);
  await transcript(acp, acpId, at);
  await transcript(acp, manualId, at);
  const file = join(desktop.dir, "conversation_summaries.db");
  const desktopDB = summaries(file);
  desktopDB.prepare("INSERT INTO conversation_summaries VALUES (?,?,?,?,?,?,?,?,?)")
    .run(desktopId, "", "preview", null, "CASCADE_RUN_STATUS_RUNNING", 0, 7, new Date(now).toISOString(), 3);
  assert.equal(readDesktopSettlements(desktop, now - 1000).length, 0);

  const orcaDB = new DatabaseSync(join(orca, "agent-session-domain.db"));
  orcaDB.exec("CREATE TABLE agent_session_records (session_id TEXT PRIMARY KEY, record_json TEXT)");
  const insert = (sessionId, nativeId, extra) => orcaDB.prepare("INSERT INTO agent_session_records VALUES (?,?)")
    .run(sessionId, JSON.stringify({
      schemaVersion: 2, sessionId, provider: "antigravity", location: { executionHostId: "local" },
      accountHome: { variable: "GEMINI_HOME", path: acp.accountHome },
      providerHandleChain: [{ handle: { transport: "acp", agent: "antigravity", nativeId } }],
      lastTurn: { state: "settled", outcome: "success", at: now }, ...extra,
    }));
  insert(chatId, acpId);
  insert(manualChat, manualId, { conversationNameSource: "user", conversationName: "Antigravity Chat" });
  insert("f139bb8d-a50a-4de6-8bef-6fa674f005c1", acpId, { accountHome: { variable: "GEMINI_HOME", path: desktop.dir } });
  orcaDB.close();
  assert.deepEqual(readACPSettlements(acp, orca, now - 1000).map(row => row.native), [acpId]);

  desktopDB.exec("UPDATE conversation_summaries SET status = 'CASCADE_RUN_STATUS_IDLE'");
  desktopDB.close();
  const events = new Map(), jobs = [], submitted = [];
  const rpc = async (method, params) => {
    if (method === "event.submit") {
      events.set(params.sessionId, params);
      submitted.push(params);
      return { accepted: true };
    }
    if (method === "title.claim") {
      const { revision } = events.get(params.sessionId);
      return { candidate: { id: "fixture", title: "测试标题", baseTitle: "", revision } };
    }
    return { accepted: true, syncStatus: params.sessionId === acpId ? "pending" : "unbound" };
  };
  const observer = createObserver(rpc, { started: now, process: (...args) => {
    const pending = processTurn(...args.slice(0, 4), async () => ({
      orcaSessionId: chatId, orcaTitle: "Antigravity Chat", orcaNativeHandle: acpId,
    }), args[5]);
    jobs.push(pending);
    return pending;
  } });
  t.after(observer.stop);
  observer.scan();
  await Promise.all(jobs);
  await Promise.resolve();
  observer.scan();
  assert.deepEqual(submitted.map(event => [event.sessionId, event.orcaSessionId]), [[desktopId, undefined], [acpId, chatId]]);
  assert.equal(readTitle(desktop.dir, desktopId), "测试标题");
  assert.equal(readTitle(acp.dir, acpId), "测试标题");
  assert.equal(readTitle(acp.dir, manualId), "");
});

test("Antigravity Orca terminal retries delayed mapping and refuses ambiguous panes", async () => {
  const waits = [], timeouts = [];
  const place = { kind: "terminal", hostId: "local", agent: "antigravity", orcaSessionId: chatId, title: "agy" };
  const diagnostic = { ok: false, error: { code: "agent_provider_id", message: "Provider ID. Address it as orca_session_id:" + chatId + "." } };
  const lookup = async (id, _signal, timeout) => {
    timeouts.push(timeout);
    if (id === acpId) return waits.length ? diagnostic : {};
    return { ok: true, result: { orcaSessionId: chatId, places: [place] } };
  };
  const options = { lookup, kind: "terminal", inherited: chatId, retry: true, pause: async ms => waits.push(ms) };
  assert.equal((await bindOrca("antigravity", acpId, options)).orcaSessionId, chatId);
  assert.deepEqual(waits, [250]);
  assert.deepEqual(timeouts, [900, 1500, 1500]);
  const twoPanes = async id => id === acpId ? diagnostic : { ok: true, result: { orcaSessionId: chatId, places: [place, place] } };
  assert.deepEqual(await bindOrca("antigravity", acpId, { lookup: twoPanes, kind: "terminal" }), {});
  assert.deepEqual(await bindOrca("antigravity", acpId, { ...options, inherited: manualChat }), {});
});
