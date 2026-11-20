import assert from "node:assert/strict";
import { EventEmitter, getEventListeners } from "node:events";
import test from "node:test";
import { parentRPC } from "../../internal/orcasync/acp-rpc.mjs";

test("ACP parent RPC discards aborted work and releases listeners on every exit", async () => {
  const input = new EventEmitter();
  const writes = [];
  const output = { write: (line, callback) => writes.push({ id: JSON.parse(line).id, callback }) };
  const rpc = parentRPC(input, output, stage => assert.fail(stage));

  const canceled = new AbortController();
  const abandoned = rpc(null, "title.claim", {}, canceled.signal);
  const lateWrite = writes.at(-1);
  assert.equal(getEventListeners(canceled.signal, "abort").length, 1);
  canceled.abort();
  await assert.rejects(abandoned, /Observer stopped/);
  assert.equal(getEventListeners(canceled.signal, "abort").length, 0);
  input.emit("line", JSON.stringify({ id: lateWrite.id, result: "too late" }));
  lateWrite.callback(new Error("late write failure"));

  const finished = new AbortController();
  const reply = rpc(null, "title.claim", {}, finished.signal);
  input.emit("line", JSON.stringify({ id: writes.at(-1).id, result: "candidate" }));
  assert.equal(await reply, "candidate");
  assert.equal(getEventListeners(finished.signal, "abort").length, 0);

  const failed = rpc(null, "title.claim", {}, finished.signal);
  writes.at(-1).callback(new Error("write failed"));
  await assert.rejects(failed, /write failed/);
  assert.equal(getEventListeners(finished.signal, "abort").length, 0);

  const disconnected = rpc(null, "title.claim", {}, finished.signal);
  input.emit("close");
  await assert.rejects(disconnected, /parent disconnected/);
  assert.equal(getEventListeners(finished.signal, "abort").length, 0);
});
