import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { AppearanceRow, type AppearanceControl } from './Appearance';
import { ApiFields } from './ModelApiFields';
import { ModelSwitch } from './ModelSwitch';
import { nativeChoice, SubscriptionFields, useSubscriptionDraft } from './SubscriptionFields';
import { rpc, type Preferences, type Settings } from './rpc';
import { Empty, InlineActionError, Section, Status, useAction, useLoad } from './ui';
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
  const { settings, edit, editNative, save, test, actions, credentials, catalog, selected } = useModelDraft(initial);
  const { provider, fallbackEnabled } = settings;
  const apiDetails = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    if ((provider === 'api' || fallbackEnabled) && apiDetails.current) apiDetails.current.open = true;
  }, [provider, fallbackEnabled]);

  return <form onSubmit={save}>
    <fieldset disabled={actions.busy} aria-busy={actions.busy}>
      <ProviderFields settings={settings} edit={edit} busy={actions.busy}
        native={provider !== 'api' && <SubscriptionFields key={provider} provider={provider}
          choice={nativeChoice(settings, provider)} catalog={catalog} selected={selected}
          onRefresh={() => {
            actions.reset();
            catalog.refresh();
          }}
          onChange={(choice) => {
            actions.reset();
            editNative(provider, choice);
          }} />} />
      <details className="settings-disclosure" ref={apiDetails} onInvalidCapture={() => {
        if (apiDetails.current) apiDetails.current.open = true;
      }}>
        <summary>自定义 API</summary>
        <ApiFields settings={settings} edit={edit} {...credentials} busy={actions.busy} />
      </details>
      <Section title="标题规则">
        <TitleLengthOptions value={settings.maxTitleLength} onChange={(length) => edit('maxTitleLength', length)} />
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

const titleLengthOptions = [
  { value: 15, label: '简短', example: '🧩 会话标题｜自动命名' },
  { value: 30, label: '详细', example: '🧩 会话标题｜自动命名并同步到 Orca' },
];

function TitleLengthOptions({ value, onChange }: { value: number; onChange: (length: number) => void }) {
  return <fieldset className="title-length-options" aria-label="标题长度">
    {titleLengthOptions.map((option) => <label className="title-length-option" key={option.value}>
      <input type="radio" name="title-length" value={option.value} checked={value === option.value}
        onChange={() => onChange(option.value)} />
      <span><strong>{option.label}</strong><span className="title-length-example">{option.example}</span></span>
    </label>)}
  </fieldset>;
}

type Edit = <K extends keyof Settings>(field: K, value: Settings[K]) => void;

function useModelDraft(initial: Settings) {
  const [settings, setSettings] = useState(() => ({ ...initial, apiProtocol: initial.apiProtocol ?? 'chat' }));
  const [saved, setSaved] = useState(settings);
  const { catalog, selected, editNative, nativeDraft } = useSubscriptionDraft(settings, setSettings);
  const actions = useAction();
  const credentials = useCredentialDraft(settings.apiBaseUrl, saved.apiBaseUrl);
  const { addressChanged, keyValue, removeKey, apiKey, setKey, setRemoveKey } = credentials;

  const edit: Edit = (field, value) => {
    actions.reset();
    if (field === 'provider' && value !== settings.provider) catalog.refresh();
    setSettings((old) => ({ ...old, [field]: value }));
  };
  function save(event: FormEvent) {
    event.preventDefault();
    void actions.run(async () => {
      try {
        const { hasApiKey: _, ...payload } = nativeDraft();
        if (addressChanged && saved.hasApiKey && !keyValue && !removeKey) {
          throw new InlineActionError('API 地址已更改，请输入新密钥或开启「保存时移除旧密钥」。');
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
    }, { success: 'toast', failure: 'toast' });
  }
  function test() {
    return actions.run(async () => {
      const { provider, codexModel, codexEffort, claudeModel, claudeEffort, fast, maxTitleLength, apiProtocol, apiBaseUrl, apiModel } = nativeDraft(true);
      if (provider === 'api' && (!apiBaseUrl.trim() || !apiModel.trim())) throw new InlineActionError('请先填写 API 地址和模型。');
      const target = provider === 'api' ? { apiProtocol, apiBaseUrl, apiModel, apiKey } : { codexModel, codexEffort, claudeModel, claudeEffort, fast };
      const result = await rpc<{ ok: boolean; model: string }>('providers.test', { provider, maxTitleLength, ...target });
      if (!result.ok) throw new Error('连接测试未通过。');
      return result.model ? `连接成功 · ${result.model}` : '连接成功。';
    }, { success: 'toast', failure: 'toast' });
  }
  return { settings, edit, editNative, save, test, actions, credentials, catalog, selected };
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

function ProviderFields({ settings, edit, busy, native }: { settings: Settings; edit: Edit; busy: boolean; native: ReactNode }) {
  return <Section title="命名来源">
    <div className="settings-rows">
      <label>首选来源<select value={settings.provider} onChange={(event) => edit('provider', event.target.value as Settings['provider'])}>
        <option value="codex">Codex 订阅</option><option value="claude">Claude Code</option><option value="api">自定义 API</option>
      </select></label>
      {native}
      {settings.provider === "codex" && <ModelSwitch checked={settings.fast} busy={busy} onChange={(checked) => edit('fast', checked)}>请求 Codex Fast 档位</ModelSwitch>}
      {settings.provider === "codex" && <ModelSwitch checked={settings.fallbackEnabled} busy={busy} onChange={(checked) => edit('fallbackEnabled', checked)}>Codex 确认失败时改用 API</ModelSwitch>}
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
      } catch (error) {
        setStartup(enabled);
        throw error;
      }
    }, { success: 'silent', failure: 'toast' });
  }

  return <Section title="桌面">
    <div className="settings-rows">
      <AppearanceRow {...appearance} />
      {value?.startupAvailable && <ModelSwitch checked={enabled} busy={action.busy} onChange={toggle}>开机自启</ModelSwitch>}
      <div className="setting-row"><span>后台运行</span><button type="button" disabled={quit.busy} onClick={() => void quit.run(() => rpc<void>('desktop.quit'), { success: 'silent', failure: 'toast' })}>退出桌面应用</button></div>
      <Status error={error ?? action.error ?? quit.error} />
    </div>
  </Section>;
}
