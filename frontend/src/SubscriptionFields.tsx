import { ChevronDown, RefreshCw } from 'lucide-react';
import { useEffect, useId, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { ModelChoices, PopupClose } from './ModelDiscovery';
import { providerName, rpc, type NativeProvider, type Settings, type SubscriptionCatalog, type SubscriptionModel } from './rpc';
import { Status, useLoad } from './ui';

export type NativeChoice = { model: string; effort: string };

export const nativeChoice = (settings: Settings, provider: NativeProvider): NativeChoice => provider === 'codex'
  ? { model: settings.codexModel, effort: settings.codexEffort }
  : { model: settings.claudeModel, effort: settings.claudeEffort };

export const effortFor = (model: SubscriptionModel, current: string) =>
  model.efforts.includes(current) ? current : model.defaultEffort ?? '';

function useSubscription(provider: Settings['provider']) {
  const [revision, setRevision] = useState(0);
  const { value, error } = useLoad(() => provider === 'api'
    ? Promise.resolve(undefined)
    : rpc<SubscriptionCatalog>('providers.subscriptionModels', { provider }), `${provider}:${revision}`);
  return { value, error, refresh: () => setRevision((old) => old + 1) };
}

export function useSubscriptionDraft(settings: Settings, setSettings: Dispatch<SetStateAction<Settings>>) {
  const provider = settings.provider;
  const catalog = useSubscription(provider);
  const choice = provider === 'api' ? undefined : nativeChoice(settings, provider);
  const selected = catalog.value?.items.find(({ id }) => id === choice?.model);
  useEffect(() => {
    if (provider === 'api' || !selected) return;
    setSettings((draft) => {
      const current = nativeChoice(draft, provider);
      if (draft.provider !== provider || current.model !== selected.id || !current.effort) return draft;
      const effort = effortFor(selected, current.effort);
      return effort === current.effort ? draft : withNativeChoice(draft, provider, { ...current, effort });
    });
  }, [provider, selected, setSettings]);

  function editNative(source: NativeProvider, next: NativeChoice) {
    setSettings((old) => old.provider !== source ? old : withNativeChoice(old, source, next));
  }

  function nativeDraft() {
    if (provider === 'api' || !choice) return settings;
    if (!choice.model) throw new Error('请先选择命名模型。');
    if (catalog.value && !selected) throw new Error('当前模型已不在 CLI 目录中，请重新选择。');
    if (!selected || !choice.effort) return settings;
    return withNativeChoice(settings, provider, { ...choice, effort: effortFor(selected, choice.effort) });
  }
  return { catalog, selected, editNative, nativeDraft };
}

function withNativeChoice(settings: Settings, provider: NativeProvider, choice: NativeChoice): Settings {
  return provider === 'codex' ? { ...settings, codexModel: choice.model, codexEffort: choice.effort }
    : { ...settings, claudeModel: choice.model, claudeEffort: choice.effort };
}

export function SubscriptionFields({ provider, choice, catalog, selected, onChange }: {
  provider: NativeProvider; choice: NativeChoice; catalog: ReturnType<typeof useSubscription>;
  selected?: SubscriptionModel; onChange: (choice: NativeChoice) => void;
}) {
  const { value, error, refresh } = catalog;
  const panelId = useId();
  const labelId = useId();
  const popup = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const items = value?.items;
  const missing = !!items && !!choice.model && !selected;
  const busy = !value && !error;
  const emptyMessage = '此 CLI 当前没有可见模型。';
  const normalized = query.trim().toLowerCase();
  const filtered = items?.filter(({ id, name, resolvedModel }) =>
    [id, name, resolvedModel].some((part) => part?.toLowerCase().includes(normalized)));
  const label = `${providerName(provider)} 模型`;
  useEffect(() => {
    if (items?.length && popup.current?.matches(':popover-open')) search.current?.focus({ preventScroll: true });
  }, [items]);

  function choose(id: string) {
    const model = items?.find((item) => item.id === id);
    if (!model) return;
    popup.current?.hidePopover();
    if (id !== choice.model) onChange({ model: id, effort: effortFor(model, choice.effort) });
  }

  return <>
    <div className="setting-row subscription-row">
      <span id={labelId}>{label}</span>
      <div className="subscription-controls">
        <button type="button" className="model-picker-trigger" popoverTarget={panelId} aria-labelledby={`${labelId} ${panelId}-selection`}>
          <span id={`${panelId}-selection`} title={selected?.resolvedModel || choice.model}>{selected?.name || choice.model || '选择模型'}</span>
          <ChevronDown size={14} aria-hidden="true" />
        </button>
        <div id={panelId} ref={popup} popover="auto" className="settings-popup model-picker" role="dialog"
          aria-label={`选择${label}`} aria-busy={busy} onToggle={(event) => {
            if (event.currentTarget.matches(':popover-open')) {
              setQuery('');
              search.current?.focus({ preventScroll: true });
            }
          }}>
          <div className="settings-popup-head"><h3>选择模型</h3><PopupClose panelId={panelId} label="关闭模型选择器" /></div>
          {busy ? <Status message="正在读取 CLI 模型目录…" /> : <Status error={error} />}
          <ModelChoices items={items} filtered={filtered} query={query} setQuery={setQuery} searchInput={search}
            selected={choice.model} choose={choose} emptyMessage={emptyMessage} />
        </div>
        <button type="button" className="icon-button" title="刷新模型目录" aria-label="刷新模型目录" onClick={refresh} disabled={busy}>
          <RefreshCw size={14} aria-hidden="true" />
        </button>
      </div>
    </div>
    <label>思考强度<select value={selected && choice.effort ? effortFor(selected, choice.effort) : choice.effort}
      disabled={!selected?.efforts.length} onChange={(event) => onChange({ ...choice, effort: event.target.value })}>
      <option value="">跟随 CLI{selected?.defaultEffort ? `（默认 ${selected.defaultEffort}）` : ''}</option>
      {!selected && choice.effort && <option value={choice.effort}>{choice.effort}</option>}
      {selected?.efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}
    </select></label>
    <div className="subscription-status">
      <Status error={error || (missing ? '当前模型已不在 CLI 目录中，请重新选择。' : undefined)}
        message={busy ? '正在读取 CLI 模型目录…' : !items?.length ? emptyMessage : undefined} />
      <Status message={value?.notice} />
      <small className="muted">目录显示 CLI 可见的模型。订阅资格与额度以手动连接测试为准。</small>
    </div>
  </>;
}
