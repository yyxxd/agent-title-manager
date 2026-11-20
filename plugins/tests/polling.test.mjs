import assert from "node:assert/strict";
import test from "node:test";
import { submitEvent, waitCandidate, isTextConfirmation } from "../shared/client.mjs";
import { handleCodex } from "../codex/index.mjs";

const state = { agent: "codex", sessionId: "fixture-session", revision: "last", currentTitle: "" };
const candidate = { id: "claim", title: "新的标题", baseTitle: "", revision: state.revision };

function clock() {
  let elapsed = 0;
  const waits = [];
  return { waits, now: () => elapsed, pause: async ms => { waits.push(ms); elapsed += ms; } };
}

test("所有已知终态立即停止轮询，恢复时没有同版本记录也立即返回", async () => {
  for (const status of ["kept", "failed", "interrupted", "stale", "applied", "disabled", "none"]) {
    const time = clock();
    let claims = 0;
    const result = await waitCandidate(async () => { claims++; return { candidate: null, status }; }, state,
      { ...time, resume: status === "none" });
    assert.deepEqual(result, { status });
    assert.equal(claims, 1);
    assert.deepEqual(time.waits, []);
  }
});

test("入口验证、排队和超过 65 秒的生成仍能领取候选", async () => {
  const time = clock();
  let claims = 0;
  const result = await waitCandidate(async (_, params) => {
    assert.deepEqual(params, state);
    claims++;
    if (time.now() < 160_000) return { candidate: null, status: time.now() < 12_000 ? "ingress_pending" : "running" };
    return { candidate, status: "claimed" };
  }, state, time);
  assert.deepEqual(result, { candidate, status: "claimed" });
  assert.ok(claims < 16);
  assert.ok(Math.max(...time.waits) <= 20_000);
});

test("后台等待有上限，后来可用同一 revision 继续领取", async () => {
  const time = clock();
  const untouched = await waitCandidate(async () => ({ candidate: null, status: "queued" }), state, time);
  assert.deepEqual(untouched, { status: "pending" });
  assert.equal(time.now(), 8 * 60_000);
  assert.ok(time.waits.length < 35);
  const recovered = await waitCandidate(async (_, params) => {
    assert.deepEqual(params, state);
    return { candidate, status: "claimed" };
  }, state, { resume: true });
  assert.equal(recovered.candidate.id, "claim");
});

test("只重试桥接明确拒绝的 ingress_busy，重试复用原提交", async () => {
  const time = clock();
  const payload = { ...state, originalGoal: "原始目标", confirmationOnly: true };
  const tries = [];
  const rpc = async (method, params) => {
    assert.equal(method, "event.submit");
    tries.push(params);
    if (tries.length < 3) throw Object.assign(new Error("队列满"), { code: "ingress_busy" });
    return { accepted: true, status: "ingress_pending" };
  };
  assert.equal((await submitEvent(rpc, payload, time)).accepted, true);
  assert.deepEqual(time.waits, [250, 750]);
  assert.ok(tries.every(params => params === payload));

  const undetermined = new Error("桥接断开");
  await assert.rejects(submitEvent(async () => { throw undetermined; }, payload, {
    pause: () => assert.fail("未确定是否接收时不能自动重试"),
  }), error => error === undetermined);
});

test("短文本外的图片、引用占位、工具结果和请求正文不能标记为确认", () => {
  assert.equal(isTextConfirmation([{ type: "text", text: "好的！" }]), true);
  assert.equal(isTextConfirmation([{ type: "text", text: "continue." }]), true);
  assert.equal(isTextConfirmation([{ type: "text", text: "OK" }, { type: "image" }]), false);
  assert.equal(isTextConfirmation([{ type: "text", text: "OK", text_elements: [{ start: 0 }] }]), false);
  assert.equal(isTextConfirmation([{ type: "tool_result", content: "OK" }, { type: "text", text: "继续" }]), false);
  assert.equal(isTextConfirmation("可以，请再修改计划"), false);
});

const user = (id, content) => ({ id, type: "userMessage", content });
const textBlock = text => ({ type: "text", text });
const turn = (id, userItems, itemsView = "full") => ({
  id, status: "completed", itemsView, items: [...userItems, { type: "agentMessage", text: "已处理" }],
});

test("Codex 分页提取原始目标，排队时关闭 App Server，候选到达后只读最新轮", async () => {
  const recent = turn(state.revision, [user("latest", [textBlock("收到。")])]);
  const original = turn("older", [user("first", [textBlock("完整会话的初始目标")])]);
  const nativeReads = [];
  let actives = 0;
  let title = "";
  const api = async (method, params) => {
    assert.equal(actives, 1);
    nativeReads.push([method, params]);
    if (method === "thread/read") {
      assert.equal(params.includeTurns, false);
      return { thread: { id: state.sessionId, name: title, historyMode: "paginated" } };
    }
    if (method === "thread/name/set") { title = params.name; return {}; }
    assert.equal(method, "thread/turns/list");
    return { data: params.sortDirection === "asc" ? [original] : [recent] };
  };
  const withNative = async action => {
    actives++;
    try { return await action(api); } finally { actives--; }
  };
  const time = clock();
  const rpc = async (method, params) => {
    if (method === "event.submit") {
      assert.equal(params.originalGoal, "完整会话的初始目标");
      assert.equal(params.confirmationOnly, true);
      assert.equal(params.turns[0].text, "收到。");
      return { accepted: true };
    }
    if (method === "title.claim") {
      assert.equal(actives, 0);
      return time.now() > 110_000 ? { candidate, status: "claimed" } : { candidate: null, status: "queued" };
    }
    assert.equal(params.observedTitle, candidate.title);
  };
  await handleCodex("stop", { session_id: state.sessionId, turn_id: state.revision }, rpc, withNative, time);
  assert.equal(title, candidate.title);
  const views = nativeReads.filter(([method]) => method === "thread/turns/list").map(([, { itemsView }]) => itemsView);
  assert.deepEqual(views, ["full", "full", "notLoaded", "notLoaded"]);
});

test("Codex 重开时按实际最新已完成轮次恢复候选，无需重提事件", async () => {
  let title = "";
  const methods = [];
  const current = turn(state.revision, [user("latest", [textBlock("目前目标")])]);
  const api = async (method, params) => {
    if (method === "thread/name/set") { title = params.name; return {}; }
    return { thread: { id: state.sessionId, name: title, turns: [current] } };
  };
  const rpc = async (method, params) => {
    methods.push(method);
    if (method === "title.claim") {
      assert.equal(params.revision, state.revision);
      return { candidate, status: "claimed" };
    }
    assert.equal(params.observedTitle, candidate.title);
  };
  await handleCodex("start", { session_id: state.sessionId }, rpc, action => action(api));
  assert.deepEqual(methods, ["title.claim", "title.ack"]);
  assert.equal(title, candidate.title);
});
