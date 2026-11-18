import { useEffect, useRef, useState, type FormEvent } from 'react';
import { AppearanceRow, type AppearanceControl } from './Appearance';
import { ApiFields } from './ModelApiFields';
import { ModelSwitch } from './ModelSwitch';
import { rpc, type Preferences, type Settings } from './rpc';
import { Empty, Section, Status, useAction, useLoad } from './ui';
import './models.css';

export function Models({ appearance }: { appearance: AppearanceControl }) {
  const { value, error } = useLoad(() => rpc<Settings>('settings.get'), 'settings');
  return <div className="model-settings">
    <header className="page-head"><h1>模型与规则</h1></header>
    <DesktopPreferences appearance={appearance} />
    <Status error={error} />
    {value ? <ModelForm initial={value} /> : !error && <Empty>正在读取模型配置。</Empty>}
  </div>;
}

function ModelForm({ initial }: { initial: Settings }) {
  const { settings, edit, save, test, actions, credentials } = useModelDraft(initial);
  const { provider, fallbackEnabled } = settings;
  const apiDetails = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    if ((provider === 'api' || fallbackEnabled) && apiDetails.current) apiDetails.current.open = true;
  }, [provider, fallbackEnabled]);

  return <form onSubmit={save}>
    <fieldset disabled={actions.busy} aria-busy={actions.busy}>
      <ProviderFields settings={settings} edit={edit} busy={actions.busy} />
      <details className="settings-disclosure" ref={apiDetails} onInvalidCapture={() => {
        if (apiDetails.current) apiDetails.current.open = true;
      }}>
        <summary>自定义 API</summary>
        <ApiFields settings={settings} edit={edit} {...credentials} busy={actions.busy} />
      </details>
      <Section title="标题规则">
        <div className="settings-rows">
          <label>标题最大长度<input type="number" min="8" max="48" required value={Number.isNaN(settings.maxTitleLength) ? '' : settings.maxTitleLength} onChange={(event) => edit('maxTitleLength', event.target.valueAsNumber)} /></label>
        </div>
      </Section>
      <div className="form-actions">
        <button type="button" onClick={() => {
          if (settings.provider === 'api' && apiDetails.current) apiDetails.current.open = true;
          void test();
        }}>测试首选来源</button>
        <button type="submit" className="primary">保存设置</button>
      </div>
    </fieldset>
    <Status error={actions.error} message={actions.busy ? '正在处理…' : actions.message} />
  </form>;
}

type Edit = <K extends keyof Settings>(field: K, value: Settings[K]) => void;

function useModelDraft(initial: Settings) {
  const [settings, setSettings] = useState(() => ({ ...initial, apiProtocol: initial.apiProtocol ?? 'chat' }));
  const [saved, setSaved] = useState(settings);
  const actions = useAction();
  const credentials = useCredentialDraft(settings.apiBaseUrl, saved.apiBaseUrl);
  const { addressChanged, keyValue, removeKey, apiKey, setKey, setRemoveKey } = credentials;

  const edit: Edit = (field, value) => setSettings((old) => ({ ...old, [field]: value }));
  function save(event: FormEvent) {
    event.preventDefault();
    void actions.run(async () => {
      try {
        const { hasApiKey: _, ...payload } = settings;
        if (addressChanged && saved.hasApiKey && !keyValue && !removeKey) {
          throw new Error('API 地址已更改，请输入新密钥或开启「保存时移除旧密钥」。');
        }
        const persisted = await rpc<Settings>('settings.save', { ...payload, apiKey });
        setSettings(persisted);
        setSaved(persisted);
        setKey('');
        if (removeKey) setRemoveKey(false);
        return '模型设置已保存。';
      } catch (error) {
        setSettings((draft) => ({ ...draft, fast: saved.fast, fallbackEnabled: saved.fallbackEnabled }));
        if (removeKey) setRemoveKey(false);
        throw error;
      }
    });
  }
  function test() {
    return actions.run(async () => {
      const { provider, codexModel, fast, apiProtocol, apiBaseUrl, apiModel } = settings;
      if (provider === 'api' && (!apiBaseUrl.trim() || !apiModel.trim())) throw new Error('请先填写 API 地址和模型。');
      const target = provider === 'api' ? { apiProtocol, apiBaseUrl, apiModel, apiKey } : { codexModel, fast };
      const result = await rpc<{ ok: boolean; model: string }>('providers.test', { provider, ...target });
      if (!result.ok) throw new Error('连接测试未通过。');
      return result.model ? `连接成功 · ${result.model}` : '连接成功。';
    });
  }
  return { settings, edit, save, test, actions, credentials };
}

function useCredentialDraft(address: string, savedAddress: string) {
  const [draftKey, setDraftKey] = useState({ address: '', value: '' });
  const [removeKey, setRemoveKey] = useState(false);
  const [credentialRevision, setCredentialRevision] = useState(0);
  const addressChanged = address !== savedAddress;
  const keyValue = draftKey.address === address ? draftKey.value : '';
  const apiKey = removeKey || (addressChanged && !keyValue) ? '' : keyValue || undefined;
  return {
    keyValue, addressChanged, removeKey, apiKey, credentialRevision,
    setKey: (value: string) => {
      setDraftKey({ address, value });
      setCredentialRevision((revision) => revision + 1);
    },
    setRemoveKey: (enabled: boolean) => {
      setRemoveKey(enabled);
      setCredentialRevision((revision) => revision + 1);
    },
  };
}

function ProviderFields({ settings, edit, busy }: { settings: Settings; edit: Edit; busy: boolean }) {
  return <Section title="命名来源">
    <div className="settings-rows">
      <label>首选来源<select value={settings.provider} onChange={(event) => edit('provider', event.target.value as Settings['provider'])}>
        <option value="codex">Codex 订阅</option><option value="api">自定义 API</option>
      </select></label>
      <label>Codex 模型<input value={settings.codexModel} onChange={(event) => edit('codexModel', event.target.value)} /></label>
      <ModelSwitch checked={settings.fast} busy={busy} onChange={(checked) => edit('fast', checked)}>请求 Codex Fast 档位</ModelSwitch>
      <ModelSwitch checked={settings.fallbackEnabled} busy={busy} onChange={(checked) => edit('fallbackEnabled', checked)}>Codex 确认失败时改用 API</ModelSwitch>
    </div>
  </Section>;
}

function DesktopPreferences({ appearance }: { appearance: AppearanceControl }) {
  const { value, error } = useLoad(() => rpc<Preferences>('desktop.get'), 'desktop');
  const [startup, setStartup] = useState<boolean>();
  const action = useAction();
  const quit = useAction();
  const enabled = startup ?? value?.startup ?? false;

  function toggle(next: boolean) {
    setStartup(next);
    void action.run(async () => {
      try {
        const persisted = await rpc<Preferences>('desktop.startup', { enabled: next });
        setStartup(persisted.startup);
        return '开机启动设置已更新。';
      } catch (error) {
        setStartup(enabled);
        throw error;
      }
    });
  }

  return <Section title="桌面">
    <div className="settings-rows">
      <AppearanceRow {...appearance} />
      {value?.startupAvailable && <ModelSwitch checked={enabled} busy={action.busy} onChange={toggle}>登录 Windows 后在托盘运行</ModelSwitch>}
      <div className="setting-row"><span>后台运行</span><button type="button" disabled={quit.busy} onClick={() => void quit.run(() => rpc<void>('desktop.quit'))}>退出桌面应用</button></div>
      <Status error={error ?? action.error ?? quit.error} message={action.message} />
    </div>
  </Section>;
}
