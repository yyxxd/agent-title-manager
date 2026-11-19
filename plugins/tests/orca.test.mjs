import assert from "node:assert/strict";
import { join, resolve } from "node:path";
import test from "node:test";
import { bindOrca, resolveRawID } from "../shared/orca.mjs";

const raw = "codex_01aa_bb";
const native = "different-native-id";
const refusal = (ids, key = native) => ({
  ok: false, error: {
    code: "agent_provider_id",
    message: key + " is the agent's own conversation id, not its Orca session ID. Address it as " +
      ids.map(id => "orca_session_id:" + id).join(" or ") + ".",
  },
});
const place = { agent: "codex", orcaSessionId: raw, title: "展示标题" };

test("Orca 使用两个独立证据，项目路径、运行 pane 和原生 ID 不用作 Orca ID", async () => {
  const queries = [];
  const lookup = async id => {
    queries.push(id);
    return id === native ? refusal([raw]) : { ok: true, result: { orcaSessionId: raw, places: [place] } };
  };
  assert.deepEqual(await bindOrca("codex", native, { lookup, inherited: raw }), {
    orcaSessionId: raw, orcaTitle: "展示标题", orcaTitleOrigin: "unknown", orcaNativeHandle: native,
  });
  assert.deepEqual(queries, [native, "orca_session_id:" + raw]);
  assert.deepEqual(await bindOrca("codex", native, { lookup, inherited: "stale-inherited-id" }), {});
  assert.deepEqual(await bindOrca("claude", native, { lookup, inherited: raw }), {});
});

test("重复映射、多个活动位置、身份未知时停止同步", async () => {
  assert.equal(resolveRawID(refusal([raw, "second-id"])), undefined);
  assert.equal(resolveRawID({ ok: false, error: { code: "agent_unknown", message: "orca_session_id:" + raw } }), undefined);
  const lookup = async id => id === native ? refusal([raw])
    : { ok: true, result: { orcaSessionId: raw, places: [place, place] } };
  assert.deepEqual(await bindOrca("codex", native, { lookup, inherited: raw }), {});
});

test("Pi 反查使用会话文件，RPC 保留原生会话 ID；空的 Orca 标题可以同步", async () => {
  const locator = join(resolve("."), "含空格 的路径", "pi-session.jsonl");
  const lookup = async id => id === locator ? refusal([raw], locator)
    : { ok: true, result: { orcaSessionId: raw, places: [{ ...place, agent: "pi", title: null }] } };
  assert.deepEqual(await bindOrca("pi", native, { locator, lookup, inherited: raw }), {
    orcaSessionId: raw, orcaTitle: "", orcaTitleOrigin: "unknown", orcaNativeHandle: locator,
  });
  assert.deepEqual(await bindOrca("codex", native, { locator, lookup, inherited: raw }), {});
});

test("terminal places are accepted only for Pi and Antigravity", async () => {
  const terminal = { agent: "antigravity", kind: "terminal", orcaSessionId: raw, title: null, hostId: "local", paneKey: "tab:leaf" };
  const lookup = async id => id === native ? refusal([raw]) : { ok: true, result: { orcaSessionId: raw, places: [terminal] } };
  assert.ok((await bindOrca("antigravity", native, { lookup, inherited: raw })).orcaSessionId);
  assert.deepEqual(await bindOrca("codex", native, { lookup, inherited: raw }), {});
});
