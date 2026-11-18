import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createInterface as lines } from 'node:readline';
import { createInterface } from 'node:readline/promises';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const pluginId = 'release-acceptance-external';
const pluginName = 'Release Acceptance External';
const titles = { '1.0.0': '🧩 日历｜修复', '2.0.0': '🧩 日历｜时区修复' };
const sha256 = file => createHash('sha256').update(readFileSync(file)).digest('hex');

function fixture(root, version) {
  const dir = join(root, `plugin-${version}`);
  mkdirSync(dir);
  copyFileSync(new URL('./external-plugin.mjs', import.meta.url), join(dir, 'index.mjs'));
  writeFileSync(join(dir, 'agent-title-plugin.json'), JSON.stringify({
    schemaVersion: 1, id: pluginId, name: pluginName, version, kind: 'agent', host: 'external', entry: 'index.mjs',
  }));
  return dir;
}

function modelFixture() {
  const calls = [];
  const server = createServer(async (req, res) => {
    const index = calls.push(null) - 1;
    let body = '';
    for await (const chunk of req) body += chunk;
    try {
      assert.equal(req.method, 'POST');
      assert.equal(req.url, '/v1/chat/completions');
      assert.equal(req.headers.authorization, undefined);
      const request = JSON.parse(body);
      assert.equal(request.model, 'acceptance-local');
      const turn = request.messages.at(-1).content;
      assert.match(turn, /日历导出/);
      const version = turn.match(/验收版本 (1\.0\.0|2\.0\.0)/)?.[1];
      assert.ok(version, 'Plugin version marker is missing');
      calls[index] = version;
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({
        choices: [{ message: { content: JSON.stringify({ action: 'rename', title: titles[version], reason: 'acceptance' }) } }],
        usage: { prompt_tokens: 19, completion_tokens: 5, prompt_tokens_details: { cached_tokens: 4 } },
      }));
    } catch {
      res.writeHead(400).end();
    }
  });
  return { server, calls };
}

export async function openNative(codex, cwd, env) {
  const child = spawn(codex, ['app-server', '--disable', 'hooks'], {
    cwd, env, windowsHide: true, stdio: ['pipe', 'pipe', 'ignore'],
  });
  const stopped = new Promise(done => child.once('close', done));
  const output = lines({ input: child.stdout });
  const input = message => child.stdin.write(JSON.stringify(message) + '\n');
  let id = 0;
  let pending;
  const fail = error => pending?.reject(error);
  child.on('error', fail);
  child.stdin.on('error', fail);
  child.on('close', () => fail(new Error('Codex App Server exited')));
  output.on('line', line => {
    try {
      const event = JSON.parse(line);
      if (!pending || event.id !== pending.id || event.method) return;
      if (event.error) return fail(new Error(`Codex App Server RPC failed (${event.error.code})`));
      pending.resolve(event.result);
    } catch (error) { fail(error); }
  });
  async function call(method, params) {
    let timer;
    try {
      return await new Promise((resolve, reject) => {
        timer = setTimeout(() => reject(new Error(`${method} timed out`)), 10000);
        pending = { id: ++id, resolve, reject };
        input({ id, method, params });
      });
    } finally { clearTimeout(timer); pending = undefined; }
  }
  const close = async () => { output.close(); child.kill(); await stopped; };
  try {
    await call('initialize', { clientInfo: { name: pluginId, version: '1.0.0' } });
    input({ method: 'initialized' });
    return { call, close };
  } catch (error) { await close(); throw error; }
}

function nativeSession(native, id) {
  return {
    read: async () => (await native.call('thread/read', { threadId: id, includeTurns: false })).thread,
    rename: name => native.call('thread/name/set', { threadId: id, name }),
  };
}

async function installedPlugin(data, bridge, version) {
  const packages = join(data, 'plugin-manager', 'packages');
  for (const entry of readdirSync(packages, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const dir = join(packages, entry.name);
    const manifest = JSON.parse(readFileSync(join(dir, 'agent-title-plugin.json'), 'utf8'));
    if (manifest.id !== pluginId || manifest.version !== version) continue;
    const runtime = JSON.parse(readFileSync(join(dir, 'atm-runtime.json'), 'utf8'));
    assert.equal(resolve(runtime.bridgePath).toLowerCase(), bridge.toLowerCase());
    return import(pathToFileURL(join(dir, manifest.entry)).href);
  }
  throw new Error(`Installed ${pluginId} ${version} was not found`);
}

function install(setup, expectedHash, destination, env) {
  assert.match(expectedHash, /^[a-f0-9]{64}$/i);
  const actualHash = sha256(setup);
  assert.equal(actualHash, expectedHash.toLowerCase());
  console.log(`Candidate SHA256 ${actualHash}`);
  const result = spawnSync(setup, ['/S', '/NoIntegration', `/D=${destination}`], {
    env, windowsHide: true, windowsVerbatimArguments: true, timeout: 90000,
  });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, 'Installer failed');
  assert.equal(sha256(setup), actualHash, 'Installer changed on disk');
}

function configure(root) {
  const isolated = { ATM_DATA_DIR: join(root, 'data'), CODEX_HOME: join(root, 'codex'),
    CLAUDE_CONFIG_DIR: join(root, 'claude'), PI_CODING_AGENT_DIR: join(root, 'pi') };
  for (const path of Object.values(isolated)) mkdirSync(path);
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) =>
    /^(?:PATH|SYSTEMROOT|WINDIR|COMSPEC|PATHEXT|SYSTEMDRIVE|USERPROFILE|APPDATA|LOCALAPPDATA|TEMP|TMP)$/i.test(key)));
  return { ...env, ...isolated };
}

async function exercise({ app, bridge, codex, env, root, fixtureModel, ui, v1, v2 }) {
  const hook = join(env.CODEX_HOME, 'hooks.json');
  await ui.question(`\n首次打开 Agent 插件，确认内置 Codex 未启用、直接显示“启用”，社区列表为空。完成后回车。`);
  assert.deepEqual(readdirSync(env.CODEX_HOME), [], 'Fresh Codex profile was changed before activation');
  await ui.question('点击内置 Codex 的“启用”，确认直接注册 Hook。完成后回车。');
  const hooks = JSON.parse(readFileSync(hook, 'utf8')).hooks;
  for (const name of ['SessionStart', 'Stop']) assert.equal(hooks[name].flatMap(group => group.hooks).length, 1);
  await ui.question(`停用内置 Codex。在模型设置选择自定义 API，地址 http://127.0.0.1:${fixtureModel.server.address().port}/v1，模型 acceptance-local，密钥留空，保存。安装本地插件 ${v1} 并确认 v1.0.0，点击启用。完成后回车。`);
  assert.equal(existsSync(hook), false, 'Clean hook file was not restored');
  const nativeEnv = { ...env };
  for (const key of ['HOME', 'USERPROFILE', 'APPDATA', 'LOCALAPPDATA', 'TEMP', 'TMP']) nativeEnv[key] = root;
  const native = await openNative(codex, root, nativeEnv);
  let originalId;
  let upgradedId;
  try {
    const start = async () => (await native.call('thread/start', { cwd: root, ephemeral: false })).thread.id;
    originalId = await start();
    const original = nativeSession(native, originalId);
    await (await installedPlugin(env.ATM_DATA_DIR, bridge, '1.0.0')).nameSession(original, env, titles['1.0.0'], false);
    assert.deepEqual(fixtureModel.calls, ['1.0.0']);
    await ui.question(`刷新统计，确认模型请求 1、标题应用 1，Token 输入 19、输出 5、缓存 4。记录页应有一条 ${pluginId} 的已应用记录。停用 v1，更新到 ${v2}，确认只有一条 v2.0.0，启用 v2。完成后回车。`);
    const upgrade = await installedPlugin(env.ATM_DATA_DIR, bridge, '2.0.0');
    upgradedId = await start();
    assert.notEqual(upgradedId, originalId);
    const upgraded = nativeSession(native, upgradedId);
    await upgrade.nameSession(upgraded, env, titles['2.0.0'], false);
    assert.deepEqual(fixtureModel.calls, ['1.0.0', '2.0.0']);
    assert.equal((await original.read()).name, titles['1.0.0']);
    await ui.question('刷新统计，确认模型请求和标题应用均为 2，且记录中有两个不同标题。停用 v2，确认界面显示已停用。完成后回车。');
    await upgrade.nameSession(nativeSession(native, await start()), env, undefined, true);
    await delay(1500);
    assert.deepEqual(fixtureModel.calls, ['1.0.0', '2.0.0']);
    await ui.question('再次刷新统计及记录，确认模型请求和标题应用均仍为 2，Token 仍为 38/10/8，两个记录及标题仍在。确认后回车。');
  } finally { await native.close(); }
  const reopened = await openNative(codex, root, nativeEnv);
  try {
    assert.equal((await nativeSession(reopened, originalId).read()).name, titles['1.0.0']);
    assert.equal((await nativeSession(reopened, upgradedId).read()).name, titles['2.0.0']);
  } finally { await reopened.close(); }
  assert.equal(app.exitCode, null, 'Desktop unexpectedly exited');
  console.log('PASS: clean profile, native title and ACK, same-ID upgrade, disabled ingress, retained title and counts.');
}

async function main() {
  assert.equal(process.platform, 'win32');
  assert.ok(process.stdin.isTTY, 'Use an interactive Windows terminal');
  const [setup, expectedHash, codex] = process.argv.slice(2);
  assert.ok(setup && expectedHash && codex, 'Usage: node windows-installed.mjs <setup.exe> <SHA256> <native-codex.exe>');
  const root = mkdtempSync(join(tmpdir(), 'atm-release-acceptance-'));
  const installDir = join(root, 'install');
  const env = configure(root);
  install(resolve(setup), expectedHash, installDir, env);
  const bridge = join(installDir, 'atm-bridge.exe');
  const v1 = fixture(root, '1.0.0');
  const v2 = fixture(root, '2.0.0');
  const fixtureModel = modelFixture();
  await new Promise((ready, reject) => fixtureModel.server.once('error', reject).listen(0, '127.0.0.1', ready));
  const ui = createInterface({ input: process.stdin, output: process.stdout });
  const app = spawn(join(installDir, 'AgentTitleManager.exe'), [], { env, stdio: 'ignore' });
  const ended = new Promise(done => app.once('close', done));
  console.log(`Isolated data and install directory: ${root}`);
  try {
    await Promise.race([
      exercise({ app, bridge, codex: resolve(codex), env, root, fixtureModel, ui, v1, v2 }),
      new Promise((_, reject) => {
        app.once('error', reject);
        app.once('exit', code => reject(new Error(`Desktop exited (${code})`)));
      }),
    ]);
  } finally {
    ui.close();
    app.kill();
    await ended;
    fixtureModel.server.close();
  }
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  await main();
}
