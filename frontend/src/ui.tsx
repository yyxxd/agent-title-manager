import { AlertCircle, X } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { errorText } from './rpc';
import { toast } from './toast';

export function Switch({
  checked,
  disabled,
  label,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  label: string;
  onChange: (next: boolean) => void;
}) {
  return (
    <button
      type="button"
      className="switch"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="switch-thumb" />
    </button>
  );
}

export function StatusNotice({ error, message }: { error?: string; message?: string }) {
  if (error) {
    return (
      <div className="notice-banner error" role="alert">
        <span className="notice-banner-left">
          <AlertCircle size={15} aria-hidden="true" />
          <span>{error}</span>
        </span>
      </div>
    );
  }
  if (message) {
    return (
      <div className="notice-banner" role="status">
        <span className="notice-banner-left">{message}</span>
      </div>
    );
  }
  return null;
}

export function ConfirmDialog({
  title,
  children,
  confirmLabel,
  danger,
  busy,
  error,
  onCancel,
  onConfirm,
}: {
  title: string;
  children: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  busy?: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  useEffect(() => {
    const trigger = document.activeElement;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) onCancel();
    };
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('keydown', onKey);
      if (trigger instanceof HTMLElement && trigger.isConnected) trigger.focus();
    };
  }, [busy, onCancel]);

  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target === event.currentTarget && !busy) onCancel();
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="confirm-modal-title">
        <div className="modal-head">
          <h2 className="modal-title" id="confirm-modal-title">
            {title}
          </h2>
          <button
            type="button"
            className="icon-btn"
            disabled={busy}
            onClick={onCancel}
            aria-label="关闭"
          >
            <X size={15} aria-hidden="true" />
          </button>
        </div>
        <div className="modal-body">
          {children}
          {error && (
            <div style={{ marginTop: 12 }}>
              <StatusNotice error={error} />
            </div>
          )}
        </div>
        <div className="modal-foot">
          <button type="button" className="btn btn-sm" disabled={busy} onClick={onCancel}>
            取消
          </button>
          <button
            type="button"
            className={`btn btn-sm ${danger ? 'btn-danger' : 'btn-primary'}`}
            disabled={busy}
            onClick={onConfirm}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

export function useLoad<T>(load: () => Promise<T>, key: string | number): { value?: T; error?: string; loading: boolean } {
  const [state, setState] = useState<{ key: string | number; value?: T; error?: string }>();
  useEffect(() => {
    let cancelled = false;
    load().then(
      (value) => {
        if (!cancelled) setState({ key, value });
      },
      (error) => {
        if (!cancelled) setState({ key, error: errorText(error) });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [key]);
  const settled = state?.key === key;
  return settled ? { value: state.value, error: state.error, loading: false } : { loading: true };
}

export class InlineActionError extends Error {}

type ActionFeedback = {
  success?: 'toast' | 'inline' | 'silent';
  failure?: 'toast' | 'inline';
};

export function useAction() {
  const [status, setStatus] = useState<{ busy: boolean; error?: string; message?: string }>({ busy: false });
  return {
    ...status,
    reset: () => setStatus({ busy: false }),
    async run(action: () => Promise<string | void>, feedback: ActionFeedback = {}) {
      setStatus({ busy: true });
      try {
        const message = await action();
        const mode = feedback.success ?? 'inline';
        setStatus({ busy: false, message: mode === 'inline' ? message || undefined : undefined });
        if (mode === 'toast' && message) toast.success(message);
      } catch (error) {
        const text = errorText(error);
        const mode = error instanceof InlineActionError ? 'inline' : feedback.failure ?? 'inline';
        setStatus({ busy: false, error: mode === 'inline' ? text : undefined });
        if (mode === 'toast') toast.error(text);
      }
    },
  };
}
