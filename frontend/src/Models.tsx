import { Check } from 'lucide-react';
import { useEffect, useState } from 'react';
import { AgentIcon } from './icons';
import { ModelApiFields } from './ModelApiFields';
import { ModelCombobox } from './ModelCombobox';
import { rpc, type Settings } from './rpc';
import { InlineActionError, StatusNotice, Switch, useAction, useLoad } from './ui';
import { effortFor, nativeChoice, useSubscriptionDraft } from './useSubscription';

const titleLengthOptions = [
  { value: 15, label: '简短（≤ 15 字）', example: '🧩 会话标题｜自动命名' },
  { value: 30, label: '详细（≤ 30 字）', example: '🧩 会话标题｜自动命名并同步到 Orca' },
] as const;

export function Models({
  onSaved,
  onDirtyChange,
}: {
  onSaved: () => void;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const { value, error, loading } = useLoad(() => rpc<Settings>('settings.get'), 'settings');

  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);

  return (
    <main className="main narrow">
      {error && (
        <div style={{ marginBottom: 16 }}>
          <StatusNotice error={error} />
        </div>
      )}
      {value ? (
        <ModelForm initial={value} onSaved={onSaved} onDirtyChange={onDirtyChange} />
      ) : (
        !error && (
          <div className="card">
            <div className="empty-inline">{loading ? '正在读取模型配置…' : '暂无配置'}</div>
          </div>
        )
      )}
    </main>
  );
}

type EditFn = <K extends keyof Settings>(field: K, value: Settings[K]) => void;

function ModelForm({
  initial,
  onSaved,
  onDirtyChange,
}: {
  initial: Settings;
  onSaved: () => void;
  onDirtyChange: (dirty: boolean) => void;
}) {
  const [settings, setSettings] = useState<Settings>(() => ({
    ...initial,
    apiProtocol: initial.apiProtocol ?? 'chat',
  }));
  const [saved, setSaved] = useState<Settings>(settings);
  const [draftKey, setDraftKey] = useState({ address: '', value: '' });
  const [removeKey, setRemoveKey] = useState(false);
  const [credentialRevision, setCredentialRevision] = useState(0);

  const actions = useAction();
  const { catalog, selected, editNative, nativeDraft } = useSubscriptionDraft(settings, setSettings);

  const addressChanged = settings.apiBaseUrl !== saved.apiBaseUrl;
  const keyValue = draftKey.address === settings.apiBaseUrl ? draftKey.value : '';
  const apiKey = removeKey || (addressChanged && !keyValue) ? '' : keyValue || undefined;
  const dirty =
    keyValue.trim() !== '' ||
    removeKey ||
    JSON.stringify(settings) !== JSON.stringify(saved);

  useEffect(() => {
    onDirtyChange(dirty);
  }, [dirty, onDirtyChange]);

  const edit: EditFn = (field, value) => {
    actions.reset();
    setSettings((old) => ({ ...old, [field]: value }));
  };

  function setKey(value: string) {
    actions.reset();
    setDraftKey({ address: settings.apiBaseUrl, value });
    setCredentialRevision((r) => r + 1);
  }

  function handleSetRemoveKey(enabled: boolean) {
    actions.reset();
    setRemoveKey(enabled);
    setCredentialRevision((r) => r + 1);
  }

  function discard() {
    actions.reset();
    setSettings(saved);
    setDraftKey({ address: saved.apiBaseUrl, value: '' });
    setRemoveKey(false);
    setCredentialRevision((r) => r + 1);
  }

  function save() {
    void actions.run(async () => {
      try {
        const { hasApiKey: _, ...payload } = nativeDraft(false);
        if (addressChanged && saved.hasApiKey && !keyValue && !removeKey) {
          throw new InlineActionError('Base URL 已更改，请填写新密钥或点击「清除」移除旧密钥。');
        }
        const persisted = await rpc<Settings>('settings.save', { ...payload, apiKey });
        setSettings(persisted);
        setSaved(persisted);
        setDraftKey({ address: persisted.apiBaseUrl, value: '' });
        if (removeKey) setRemoveKey(false);
        onSaved();
        return '模型配置已保存并生效';
      } catch (error) {
        setSettings((draft) => ({
          ...draft,
          fast: saved.fast,
          fallbackEnabled: saved.fallbackEnabled,
        }));
        if (removeKey) setRemoveKey(false);
        throw error;
      }
    }, { success: 'toast', failure: 'toast' });
  }

  function testConnection() {
    void actions.run(async () => {
      const {
        provider,
        codexModel,
        codexEffort,
        claudeModel,
        claudeEffort,
        fast,
        maxTitleLength,
        apiProtocol,
        apiBaseUrl,
        apiModel,
      } = nativeDraft(true);
      if (provider === 'api' && (!apiBaseUrl.trim() || !apiModel.trim())) {
        throw new InlineActionError('请先填写 Base URL 和 API 模型。');
      }
      const target =
        provider === 'api'
          ? { apiProtocol, apiBaseUrl, apiModel, apiKey }
          : { codexModel, codexEffort, claudeModel, claudeEffort, fast };
      const result = await rpc<{ ok: boolean; model: string }>('providers.test', {
        provider,
        maxTitleLength,
        ...target,
      });
      if (!result.ok) throw new Error('连接测试未通过。');
      return result.model ? `连接成功 · ${result.model}` : '连接成功';
    }, { success: 'toast', failure: 'toast' });
  }

  const provider = settings.provider;
  const choice = provider === 'api' ? undefined : nativeChoice(settings, provider);
  const subCatalog = catalog.value;
  const subLoggedOut = subCatalog?.authRequired === true;
  const subItems = subLoggedOut ? [] : (subCatalog?.items ?? []);
  const subDisabled = catalog.loading || subLoggedOut || !!catalog.error;
  const subMissing = !!subItems.length && !!choice?.model && !selected;

  let subTriggerLabel = selected?.name || choice?.model || '选择模型';
  if (subLoggedOut) subTriggerLabel = `未登录 · 已保留 ${choice?.model || '原设置'}`;
  else if (catalog.loading) subTriggerLabel = '读取中…';
  else if (catalog.error) subTriggerLabel = catalog.error.startsWith('找不到原生 ') ? '未找到 CLI' : '读取失败';
  else if (!subItems.length) subTriggerLabel = '目录为空';
  else if (subMissing) subTriggerLabel = `重新选择模型（原 ${choice?.model}）`;

  return (
    <>
      <div className="section-stack">
        <section className="card">
          <div className="card-header">
            <div>
              <h2 className="card-title">命名来源</h2>
            </div>
            <div className="view-actions">
              <span className="card-sub">选择用于生成会话标题的模型通道</span>
              <button
                type="button"
                className="btn btn-sm"
                disabled={actions.busy}
                onClick={testConnection}
              >
                {actions.busy ? '测试中…' : '测试连接'}
              </button>
            </div>
          </div>

          <div className="card-body form-fields">
            <div className="source-cards" role="radiogroup" aria-label="首选命名来源">
              <SourceCard
                active={provider === 'codex'}
                icon="codex"
                title="Codex 订阅"
                desc="复用本机 Codex CLI 额度"
                disabled={actions.busy}
                onClick={() => edit('provider', 'codex')}
              />
              <SourceCard
                active={provider === 'claude'}
                icon="claude"
                title="Claude Code"
                desc="使用 claude.ai 原生登录"
                disabled={actions.busy}
                onClick={() => edit('provider', 'claude')}
              />
              <SourceCard
                active={provider === 'api'}
                icon="api"
                title="自定义 API"
                desc="自带密钥与第三方中转"
                disabled={actions.busy}
                onClick={() => edit('provider', 'api')}
              />
            </div>

            {provider !== 'api' && choice && (
              <>
                {(subLoggedOut || catalog.error || subMissing) && (
                  <div
                    className={`notice-banner ${catalog.error ? 'error' : 'warn'}`}
                    role="status"
                  >
                    <span className="notice-banner-left">
                      <span className={`status-dot ${catalog.error ? 'danger' : 'warn'}`} />
                      <span>
                        {catalog.error
                          ? catalog.error
                          : subLoggedOut
                            ? subCatalog?.notice || '未检测到订阅登录，请先在本机 CLI 完成登录后点击右侧刷新。'
                            : `原模型 ${choice.model} 已不在 CLI 目录中，请重新选择。`}
                      </span>
                    </span>
                  </div>
                )}

                <div className="field-row-2">
                  <div className="field">
                    <span className="field-label">
                      {provider === 'codex' ? 'Codex 模型' : 'Claude 模型'}
                    </span>
                    <ModelCombobox
                      value={choice.model}
                      placeholder={provider === 'codex' ? '选择 Codex 模型' : '选择 Claude 模型'}
                      triggerLabel={subTriggerLabel}
                      options={subItems.map((item) => ({
                        id: item.id,
                        name: item.name,
                        note: item.resolvedModel && item.resolvedModel !== item.id ? item.resolvedModel : undefined,
                      }))}
                      disabled={actions.busy}
                      dropdownDisabled={subDisabled}
                      busy={catalog.loading}
                      error={catalog.error}
                      emptyMessage="此 CLI 当前没有可见模型。"
                      onSelect={(id) => {
                        const model = subItems.find((m) => m.id === id);
                        if (!model) return;
                        actions.reset();
                        editNative(provider, { model: id, effort: effortFor(model, choice.effort) });
                      }}
                      onRefresh={() => {
                        actions.reset();
                        catalog.refresh();
                      }}
                    />
                  </div>

                  <div className="field">
                    <label className="field-label" htmlFor="native-effort">
                      思考强度
                    </label>
                    <select
                      id="native-effort"
                      className="select"
                      disabled={actions.busy || subDisabled || !selected?.efforts.length}
                      value={selected && choice.effort ? effortFor(selected, choice.effort) : choice.effort}
                      onChange={(e) => {
                        actions.reset();
                        editNative(provider, { ...choice, effort: e.target.value });
                      }}
                    >
                      <option value="">
                        {subDisabled ? '—' : '跟随 CLI'}
                        {selected?.defaultEffort ? `（默认 ${selected.defaultEffort}）` : ''}
                      </option>
                      {!selected && choice.effort && (
                        <option value={choice.effort}>{choice.effort}</option>
                      )}
                      {selected?.efforts.map((effort) => (
                        <option key={effort} value={effort}>
                          {effort}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>

                {provider === 'codex' && (
                  <div>
                    <div className="toggle-row">
                      <div className="toggle-copy">
                        <span className="toggle-title">请求 Codex Fast 档位</span>
                        <span className="toggle-desc">
                          优先请求快速响应档位，实际命中档位可在记录详情中查看
                        </span>
                      </div>
                      <Switch
                        checked={settings.fast}
                        disabled={actions.busy}
                        label="请求 Codex Fast 档位"
                        onChange={(checked) => edit('fast', checked)}
                      />
                    </div>
                    <div className="toggle-row">
                      <div className="toggle-copy">
                        <span className="toggle-title">Codex 确认失败时改用自定义 API</span>
                        <span className="toggle-desc">
                          开启后仅在 Codex 订阅调用确认失败时回退到下方备用 API
                        </span>
                      </div>
                      <Switch
                        checked={settings.fallbackEnabled}
                        disabled={actions.busy}
                        label="Codex 确认失败时改用自定义 API"
                        onChange={(checked) => edit('fallbackEnabled', checked)}
                      />
                    </div>
                  </div>
                )}
              </>
            )}

            {actions.error && <StatusNotice error={actions.error} />}
          </div>
        </section>

        {(provider === 'api' || (provider === 'codex' && settings.fallbackEnabled)) && (
          <ModelApiFields
            title={provider === 'api' ? '自定义 API 配置' : '备用自定义 API（回退时使用）'}
            settings={settings}
            edit={edit}
            keyValue={keyValue}
            apiKey={apiKey}
            removeKey={removeKey}
            addressChanged={addressChanged}
            busy={actions.busy}
            credentialRevision={credentialRevision}
            setKey={setKey}
            setRemoveKey={handleSetRemoveKey}
          />
        )}

        <section className="card">
          <div className="card-header">
            <h2 className="card-title">标题风格</h2>
            <span className="card-sub">控制自动生成标题的简洁程度</span>
          </div>
          <div className="card-body">
            <div className="style-grid" role="radiogroup" aria-label="标题长度风格">
              {titleLengthOptions.map((option) => {
                const active = settings.maxTitleLength === option.value;
                return (
                  <button
                    key={option.value}
                    type="button"
                    role="radio"
                    aria-checked={active}
                    disabled={actions.busy}
                    className={`style-option ${active ? 'active' : ''}`}
                    onClick={() => edit('maxTitleLength', option.value)}
                  >
                    <div className="style-option-head">
                      <span>{option.label}</span>
                      {active && <Check size={14} aria-hidden="true" style={{ color: 'var(--accent)' }} />}
                    </div>
                    <div className="style-example">{option.example}</div>
                  </button>
                );
              })}
            </div>
          </div>
        </section>
      </div>

      {dirty && (
        <div className="sticky-save-bar" role="region" aria-label="未保存的模型设置操作栏">
          <div className="save-status">
            <span className="status-dot warn" />
            <span>有未保存的更改</span>
            <button type="button" className="btn btn-sm btn-ghost" disabled={actions.busy} onClick={discard}>
              放弃
            </button>
          </div>
          <div className="view-actions">
            <button type="button" className="btn btn-sm" disabled={actions.busy} onClick={testConnection}>
              {actions.busy ? '处理中…' : '测试连接'}
            </button>
            <button type="button" className="btn btn-sm btn-primary" disabled={actions.busy} onClick={save}>
              保存设置
            </button>
          </div>
        </div>
      )}
    </>
  );
}

function SourceCard({
  active,
  icon,
  title,
  desc,
  disabled,
  onClick,
}: {
  active: boolean;
  icon: string;
  title: string;
  desc: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={`source-card ${active ? 'active' : ''}`}
      role="radio"
      aria-checked={active}
      disabled={disabled}
      onClick={onClick}
    >
      <span className="source-card-title">
        <span className="source-card-brand">
          <span className="agent-icon-inline">
            <AgentIcon id={icon} size={15} />
          </span>
          {title}
        </span>
        {active && <Check size={14} aria-hidden="true" style={{ color: 'var(--accent)' }} />}
      </span>
      <span className="source-card-desc">{desc}</span>
    </button>
  );
}
