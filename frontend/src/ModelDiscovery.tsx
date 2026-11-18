import { Check, ChevronDown, RefreshCw, Search, X } from 'lucide-react';
import { useId, useRef, useState, type RefObject } from 'react';
import { rpc, type ApiModel, type ApiTarget } from './rpc';
import { Status, useAction } from './ui';
export function PopupClose({ panelId, label }: { panelId: string; label: string }) {
  return <button type="button" className="icon-button popup-close" popoverTarget={panelId}
    popoverTargetAction="hide" title={label} aria-label={label}><X size={15} aria-hidden="true" /></button>;
}

type DiscoveryProps = {
  target: ApiTarget; apiKey?: string; blocked: boolean; listingUnavailable: boolean;
  selected: string; onSelect: (id: string) => void;
};

export function ModelDiscovery({ target, apiKey, blocked, listingUnavailable, selected, onSelect }: DiscoveryProps) {
  const panelId = useId();
  const panel = useRef<HTMLDivElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const [items, setItems] = useState<ApiModel[]>();
  const [query, setQuery] = useState('');
  const action = useAction();
  const normalized = query.trim().toLowerCase();
  const filtered = items?.filter(({ id, name }) => id.toLowerCase().includes(normalized) || name?.toLowerCase().includes(normalized));

  function fetchModels() {
    setItems(undefined);
    setQuery('');
    void action.run(async () => {
      const result = await rpc<{ items: ApiModel[] }>('providers.models', { ...target, apiKey });
      setItems(result.items ?? []);
    });
  }

  function choose(id: string) {
    panel.current?.hidePopover();
    onSelect(id);
  }

  return <div className="model-discovery">
    <div className="model-discovery-controls">
      <button type="button" className="model-picker-trigger" disabled={blocked} popoverTarget={panelId}
        onClick={() => { if (!items && !action.busy && !panel.current?.matches(':popover-open')) fetchModels(); }}>
        {items ? '选择模型' : '获取模型列表'}<ChevronDown size={14} aria-hidden="true" />
      </button>
      <div id={panelId} popover="auto" ref={panel} className="settings-popup model-picker"
        role="dialog" aria-label="选择 API 模型" aria-busy={action.busy}
        onToggle={(event) => { if (event.currentTarget.matches(':popover-open')) {
          setQuery('');
          searchInput.current?.focus({ preventScroll: true });
        } }}>
        <div className="settings-popup-head">
          <h3>选择模型</h3>
          <div className="popup-actions">
            <button type="button" className="icon-button popup-close" disabled={action.busy}
              onClick={fetchModels} aria-label="刷新模型列表" title="刷新模型列表"><RefreshCw size={14} aria-hidden="true" /></button>
            <PopupClose panelId={panelId} label="关闭模型选择器" />
          </div>
        </div>
        {action.busy && <Status message="正在获取模型列表…" />}
        {action.error && <Status error={`${action.error} 可手动输入模型 ID。`} />}
        <ModelChoices items={items} filtered={filtered} query={query} setQuery={setQuery}
          searchInput={searchInput} selected={selected} choose={choose} />
      </div>
    </div>
    {listingUnavailable && <Status message="此地址不提供模型列表，可手动输入模型 ID。" />}
  </div>;
}

function ModelChoices({ items, filtered, query, setQuery, searchInput, selected, choose }: {
  items?: ApiModel[]; filtered?: ApiModel[]; query: string; setQuery: (query: string) => void;
  searchInput: RefObject<HTMLInputElement | null>; selected: string; choose: (id: string) => void;
}) {
  if (!items) return null;
  if (!items.length) return <Status message="模型列表为空，可手动输入模型 ID。" />;
  return <>
    <div className="model-picker-search">
      <Search size={14} aria-hidden="true" />
      <input ref={searchInput} type="search" aria-label="查找模型" placeholder="查找模型"
        value={query} onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => { if (event.key === 'Enter') event.preventDefault(); }} />
    </div>
    {filtered?.length === 0 && <Status message="没有匹配的模型。" />}
    {!!filtered?.length && <ul className="model-options" aria-label="可用模型">
      {filtered.map(({ id, name }) => <li key={id}><button type="button" aria-pressed={id === selected} onClick={() => choose(id)}>
        <span>{name || id}{name && name !== id && <small className="muted">{id}</small>}</span>
        {id === selected && <Check size={15} aria-hidden="true" />}
      </button></li>)}
    </ul>}
  </>;
}
