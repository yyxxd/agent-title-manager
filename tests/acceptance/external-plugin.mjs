import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { setTimeout as delay } from 'node:timers/promises';

const manifest = JSON.parse(readFileSync(new URL('./agent-title-plugin.json', import.meta.url), 'utf8'));
const runtime = JSON.parse(readFileSync(new URL('./atm-runtime.json', import.meta.url), 'utf8'));

function bridge(env, method, params) {
  const response = spawnSync(runtime.bridgePath, ['rpc'], {
    input: JSON.stringify({ v: 1, method, params: { agent: manifest.id, ...params } }),
    env, encoding: 'utf8', windowsHide: true, timeout: 5000,
  });
  if (response.error) throw response.error;
  const envelope = JSON.parse(response.stdout);
  assert.equal(typeof envelope.ok, 'boolean');
  return envelope;
}

export async function nameSession(host, env, expectedTitle, disabled) {
  const before = await host.read();
  const fields = { sessionId: before.id, revision: 'synthetic-turn-1', currentTitle: before.name ?? '' };
  const submit = bridge(env, 'event.submit', {
    ...fields, titleOrigin: 'auto', project: 'acceptance',
    turns: [{ role: 'user', text: `请修复日历导出中的时区偏差。验收版本 ${manifest.version}。` }],
  });
  if (disabled) {
    assert.equal(submit.ok, false);
    assert.match(submit.error, /插件.*(?:未启用|不可用)/);
    assert.equal((await host.read()).name ?? '', fields.currentTitle);
    return;
  }
  assert.equal(submit.ok, true, submit.error);
  assert.equal(submit.result.accepted, true);
  let candidate;
  for (let tries = 0; tries < 40 && !candidate; tries++) {
    const claim = bridge(env, 'title.claim', fields);
    assert.equal(claim.ok, true, claim.error);
    candidate = claim.result.candidate;
    if (!candidate) await delay(200);
  }
  assert.equal(candidate?.title, expectedTitle);
  assert.equal(candidate.baseTitle, fields.currentTitle);
  assert.equal(candidate.revision, fields.revision);
  const current = await host.read();
  assert.equal(current.id, fields.sessionId);
  assert.equal(current.name ?? '', fields.currentTitle);
  await host.rename(candidate.title);
  const observedTitle = (await host.read()).name;
  assert.equal(observedTitle, candidate.title);
  const ack = bridge(env, 'title.ack', { sessionId: fields.sessionId, id: candidate.id, observedTitle });
  assert.equal(ack.ok, true, ack.error);
  assert.equal(ack.result.accepted, true);
}
