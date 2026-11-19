import { ChevronDown, RefreshCw } from 'lucide-react';
import { useEffect, useId, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { ModelChoices, PopupClose } from './ModelDiscovery';
import { providerName, rpc, type NativeProvider, type Settings, type SubscriptionCatalog, type SubscriptionModel } from './rpc';
import { InlineActionError, Status, useLoad } from './ui';

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

  function nativeDraft(testing = false) {
    if (provider === 'api' || !choice) return settings;
    if (catalog.value?.authRequired) {
      if (testing) throw new InlineActionError(catalog.value.notice ?? '未登录');
      return settings;
    }
    if (!choice.model) throw new InlineActionError('请先选择命名模型。');
    if (catalog.value && !selected) throw new InlineActionError('当前模型已不在 CLI 目录中，请重新选择。');
    if (!selected || !choice.effort) return settings;
    return withNativeChoice(settings, provider, { ...choice, effort: effortFor(selected, choice.effort) });
  }
  return { catalog, selected, editNative, nativeDraft };
}

function withNativeChoice(settings: Settings, provider: NativeProvider, choice: NativeChoice): Settings {
  return provider === 'codex' ? { ...settings, codexModel: choice.model, codexEffort: choice.effort }
    : { ...settings, claudeModel: choice.model, claudeEffort: choice.effort };
}

export function SubscriptionFields({ provider, choice, catalog, selected, onRefresh, onChange }: {
  provider: NativeProvider; choice: NativeChoice; catalog: ReturnType<typeof useSubscription>;
  selected?: SubscriptionModel; onRefresh: () => void; onChange: (choice: NativeChoice) => void;
}) {
  const { value, error } = catalog;
  const panelId = useId();
  const labelId = useId();
  const popup = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState('');
  const loggedOut = value?.authRequired === true;
  const items = loggedOut ? undefined : value?.items;
  const missing = !!items?.length && !!choice.model && !selected;
  const busy = !value && !error;
  const disabled = busy || loggedOut;
  const visibleChoice = disabled ? undefined : choice;
  const emptyMessage = '此 CLI 当前没有可见模型。';
  const normalized = query.trim().toLowerCase();
  const filtered = items?.filter(({ id, name, resolvedModel }) =>
    [id, name, resolvedModel].some((part) => part?.toLowerCase().includes(normalized)));
  const label = `${providerName(provider)} 模型`;
  let modelText = selected?.name || choice.model || '选择模型';
  if (loggedOut) modelText = '未登录';
  else if (busy) modelText = '读取中…';
  else if (error) modelText = error.startsWith('找不到原生 ') ? '未找到 CLI' : '读取失败';
  else if (!items?.length) modelText = '目录为空';
  else if (missing) modelText = '重新选择模型';
  useEffect(() => {
    if (!popup.current?.matches(':popover-open')) return;
    if (loggedOut) popup.current.hidePopover();
    else if (items?.length) search.current?.focus({ preventScroll: true });
  }, [items, loggedOut]);

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
        <button type="button" className="model-picker-trigger" popoverTarget={panelId} disabled={disabled}
          aria-labelledby={`${labelId} ${panelId}-selection`}>
          <span id={`${panelId}-selection`} aria-live="polite" title={error || selected?.resolvedModel || visibleChoice?.model}>
            {modelText}
          </span>
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
          <Status error={error} message={missing ? `原模型 ${choice.model} 已不在 CLI 目录中，请重新选择。` : undefined} />
          <ModelChoices items={items} filtered={filtered} query={query} setQuery={setQuery} searchInput={search}
            selected={choice.model} choose={choose} emptyMessage={emptyMessage} />
        </div>
        <button type="button" className="icon-button" title="刷新模型目录" aria-label="刷新模型目录" onClick={onRefresh} disabled={busy}>
          <RefreshCw size={14} aria-hidden="true" />
        </button>
      </div>
    </div>
    <label>思考强度<select value={selected && choice.effort ? effortFor(selected, choice.effort) : visibleChoice?.effort ?? ''}
      disabled={!selected?.efforts.length} onChange={(event) => onChange({ ...choice, effort: event.target.value })}>
      <option value="">{disabled ? '—' : '跟随 CLI'}{selected?.defaultEffort ? `（默认 ${selected.defaultEffort}）` : ''}</option>
      {!selected && visibleChoice?.effort && <option value={visibleChoice.effort}>{visibleChoice.effort}</option>}
      {selected?.efforts.map((effort) => <option key={effort} value={effort}>{effort}</option>)}
    </select></label>
  </>;
}
