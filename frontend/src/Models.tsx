import { useState, type FormEvent } from 'react';
import { rpc, type Preferences, type Settings } from './rpc';
import { Empty, Section, Status, useAction, useLoad } from './ui';

export function Models() {
  const { value, error } = useLoad(() => rpc<Settings>('settings.get'), 'settings');
  return <>
    <header className="page-head"><h1>模型与规则</h1></header>
    <Status error={error} />
    {value ? <ModelForm initial={value} /> : !error && <Empty>正在读取模型设置…</Empty>}
    <DesktopPreferences />
  </>;
}

function ModelForm({ initial }: { initial: Settings }) {
  const [settings, setSettings] = useState(initial);
  const [saved, setSaved] = useState(initial);
  const [key, setKey] = useState('');
  const [removeKey, setRemoveKey] = useState(false);
  const actions = useAction();

  function edit<K extends keyof Settings>(field: K, value: Settings[K]) {
    setSettings((old) => ({ ...old, [field]: value }));
  }

  function save(event: FormEvent) {
    event.preventDefault();
    void actions.run(async () => {
      const { hasApiKey: _, ...payload } = settings;
      const persisted = await rpc<Settings>('settings.save', { ...payload, apiKey: removeKey ? '' : key || undefined });
      setSettings(persisted);
      setSaved(persisted);
      setKey('');
      setRemoveKey(false);
      return '模型设置已保存。';
    });
  }

  function test() {
    return actions.run(async () => {
      const result = await rpc<{ ok: boolean; model: string }>('providers.test', {
        provider: settings.provider,
        ...(settings.provider === 'api' && { apiBaseUrl: settings.apiBaseUrl, apiModel: settings.apiModel, apiKey: removeKey ? '' : key || undefined }),
      });
      if (!result.ok) throw new Error('连接测试未通过。');
      return result.model ? `连接成功 · ${result.model}` : '连接成功。';
    });
  }

  const saveCodexFirst = settings.provider === 'codex' &&
    (saved.codexModel !== settings.codexModel || saved.fast !== settings.fast);
  return <form onSubmit={save} className="settings-stack">
    <fieldset disabled={actions.busy}>
      <ProviderFields settings={settings} edit={edit} />
      <ApiFields settings={settings} edit={edit} apiKey={key} setKey={setKey} removeKey={removeKey} setRemoveKey={setRemoveKey} />
      <Section title="标题规则">
        <div className="settings-grid">
          <label>标题最大长度<input type="number" min="8" max="48" required value={Number.isNaN(settings.maxTitleLength) ? '' : settings.maxTitleLength} onChange={(event) => edit('maxTitleLength', event.target.valueAsNumber)} /></label>
        </div>
      </Section>
      <div className="form-actions">
        <button type="button" disabled={saveCodexFirst} title={saveCodexFirst ? '先保存 Codex 设置' : undefined} onClick={() => void test()}>测试首选来源</button>
        <button type="submit" className="primary">保存设置</button>
      </div>
    </fieldset>
    <Status error={actions.error} message={actions.busy ? '正在处理…' : actions.message} />
  </form>;
}

type Edit = <K extends keyof Settings>(field: K, value: Settings[K]) => void;

function ProviderFields({ settings, edit }: { settings: Settings; edit: Edit }) {
  return <Section title="命名来源">
    <div className="settings-grid">
      <label>首选来源<select value={settings.provider} onChange={(event) => edit('provider', event.target.value as Settings['provider'])}>
        <option value="codex">Codex 订阅</option><option value="api">兼容 OpenAI 的 API</option>
      </select></label>
      <label>Codex 模型<input value={settings.codexModel} onChange={(event) => edit('codexModel', event.target.value)} /></label>
      <label className="check"><input type="checkbox" checked={settings.fast} onChange={(event) => edit('fast', event.target.checked)} /> 请求 Codex Fast 档位</label>
      <label className="check"><input type="checkbox" checked={settings.fallbackEnabled} onChange={(event) => edit('fallbackEnabled', event.target.checked)} /> Codex 确认失败时改用 API</label>
    </div>
  </Section>;
}

function ApiFields({ settings, edit, apiKey, setKey, removeKey, setRemoveKey }: {
  settings: Settings; edit: Edit; apiKey: string; setKey: (key: string) => void;
  removeKey: boolean; setRemoveKey: (enabled: boolean) => void;
}) {
  return <Section title="自定义 API">
    <div className="settings-grid">
      <label>API 地址<input type="url" placeholder="https://…" value={settings.apiBaseUrl} onChange={(event) => edit('apiBaseUrl', event.target.value)} /></label>
      <label>API 模型<input value={settings.apiModel} onChange={(event) => edit('apiModel', event.target.value)} /></label>
      <label>API 密钥<input autoComplete="new-password" type="password" value={apiKey} disabled={removeKey} placeholder={settings.hasApiKey ? '已保存密钥' : '输入密钥'} onChange={(event) => setKey(event.target.value)} /></label>
      {settings.hasApiKey && <label className="check"><input type="checkbox" checked={removeKey} onChange={(event) => setRemoveKey(event.target.checked)} /> 保存时移除已存密钥</label>}
    </div>
  </Section>;
}

function DesktopPreferences() {
  const [refresh, setRefresh] = useState(0);
  const { value, error } = useLoad(() => rpc<Preferences>('desktop.get'), refresh);
  const action = useAction();
  return <Section title="桌面运行">
    <div className="settings-grid">
      {value?.startupAvailable && <label className="check"><input type="checkbox" checked={value.startup} disabled={action.busy}
        onChange={(event) => {
          const enabled = event.target.checked;
          void action.run(async () => {
            await rpc('desktop.startup', { enabled });
            setRefresh(refresh + 1);
            return '登录启动已更新。';
          });
        }} /> 登录 Windows 后在托盘运行</label>}
      <div className="actions"><button type="button" onClick={() => void action.run(() => rpc<void>('desktop.quit'))}>退出桌面应用</button></div>
    </div>
    <Status error={error ?? action.error} message={action.message} />
  </Section>;
}
