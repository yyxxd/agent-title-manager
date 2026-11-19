import { Check, ChevronDown, RefreshCw } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { StatusNotice } from './ui';

export type ComboOption = {
  id: string;
  name?: string;
  note?: string;
};

export function ModelCombobox({
  value,
  placeholder,
  options,
  allowCustomInput,
  disabled,
  dropdownDisabled,
  busy,
  error,
  emptyMessage,
  triggerLabel,
  onInputChange,
  onSelect,
  onOpenFetch,
  onRefresh,
}: {
  value: string;
  placeholder: string;
  options: ComboOption[];
  allowCustomInput?: boolean;
  disabled?: boolean;
  dropdownDisabled?: boolean;
  busy?: boolean;
  error?: string;
  emptyMessage?: string;
  triggerLabel?: string;
  onInputChange?: (next: string) => void;
  onSelect: (id: string) => void;
  onOpenFetch?: () => void;
  onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const wrapRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onPointerDown);
    window.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  useEffect(() => {
    if (open) searchRef.current?.focus({ preventScroll: true });
  }, [open, options.length]);

  const normalized = query.trim().toLowerCase();
  const filtered = options.filter(
    (item) =>
      item.id.toLowerCase().includes(normalized) ||
      (item.name || '').toLowerCase().includes(normalized) ||
      (item.note || '').toLowerCase().includes(normalized),
  );

  function toggleDropdown() {
    if (disabled || dropdownDisabled) return;
    const next = !open;
    setOpen(next);
    if (next) {
      setQuery('');
      onOpenFetch?.();
    }
  }

  return (
    <div className="input-group" ref={wrapRef}>
      <div className="combo-wrap">
        {allowCustomInput ? (
          <input
            type="text"
            className="input mono"
            value={value}
            disabled={disabled}
            placeholder={placeholder}
            autoComplete="off"
            onChange={(e) => onInputChange?.(e.target.value)}
          />
        ) : (
          <button
            type="button"
            className="input combo-trigger"
            disabled={disabled}
            aria-expanded={open}
            onClick={toggleDropdown}
          >
            <span className="mono combo-trigger-text">{triggerLabel ?? (value || placeholder)}</span>
            <ChevronDown size={14} aria-hidden="true" style={{ color: 'var(--text-muted)', flexShrink: 0 }} />
          </button>
        )}

        {open && (
          <div className="combo-dropdown" role="listbox" aria-label="模型选项">
            <div className="combo-search-wrap">
              <input
                ref={searchRef}
                type="text"
                className="combo-search"
                placeholder="筛选模型…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') e.preventDefault();
                }}
              />
            </div>

            {busy && <div className="combo-empty">正在拉取模型列表…</div>}
            {!busy && error && (
              <div style={{ padding: 4 }}>
                <StatusNotice error={error} />
              </div>
            )}
            {!busy && !error && filtered.length > 0 && (
              filtered.map((item) => {
                const sub = item.note || (item.name && item.name !== item.id ? item.name : '');
                const selected = item.id === value;
                return (
                  <button
                    key={item.id}
                    type="button"
                    className={`combo-option ${selected ? 'selected' : ''}`}
                    onClick={() => {
                      onSelect(item.id);
                      setOpen(false);
                      setQuery('');
                    }}
                  >
                    <span className="mono" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {item.id}
                    </span>
                    <span className="combo-option-meta">
                      {sub && <span>{sub}</span>}
                      {selected && <Check size={14} aria-hidden="true" style={{ color: 'var(--accent)' }} />}
                    </span>
                  </button>
                );
              })
            )}
            {!busy && !error && filtered.length === 0 && (
              <div className="combo-empty">
                {options.length === 0
                  ? emptyMessage ?? '暂无可用模型'
                  : `无匹配模型${allowCustomInput ? '，可直接使用当前输入 ID' : ''}`}
              </div>
            )}
          </div>
        )}
      </div>

      {allowCustomInput && (
        <button
          type="button"
          className="btn"
          disabled={disabled || dropdownDisabled}
          title="展开或拉取模型列表"
          onClick={toggleDropdown}
        >
          <span>模型列表</span>
          <ChevronDown size={14} aria-hidden="true" />
        </button>
      )}

      <button
        type="button"
        className="icon-btn bordered"
        style={{ width: 36, height: 36, flexShrink: 0 }}
        disabled={disabled || dropdownDisabled || busy}
        title="刷新模型列表"
        aria-label="刷新模型列表"
        onClick={() => {
          setQuery('');
          setOpen(true);
          onRefresh();
        }}
      >
        <RefreshCw size={14} aria-hidden="true" />
      </button>
    </div>
  );
}
