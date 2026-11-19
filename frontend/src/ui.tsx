import { useEffect, useRef, useState, type ReactNode } from 'react';
import { errorText } from './rpc';
import { toast } from './toast';

export function Section({ title, children, action }: {
  title: string; children: ReactNode; action?: ReactNode;
}) {
  return <section className="panel">
    <header className="panel-head"><h2>{title}</h2>{action}</header>
    <div className="panel-body">{children}</div>
  </section>;
}

export function ConfirmDialog({ title, children, confirmLabel, busy, error, onCancel, onConfirm }: {
  title: string; children: ReactNode; confirmLabel: string; busy: boolean; error?: string;
  onCancel: () => void; onConfirm: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = dialog.current;
    const trigger = document.activeElement;
    element?.showModal();
    return () => {
      element?.close();
      if (trigger instanceof HTMLElement && trigger.isConnected) trigger.focus();
    };
  }, []);
  return <dialog ref={dialog} aria-label={title} onCancel={(event) => { event.preventDefault(); if (!busy) onCancel(); }}>
    <h2>{title}</h2>
    <div className="dialog-copy">{children}</div>
    <Status error={error} />
    <div className="actions">
      <button autoFocus disabled={busy} onClick={onCancel}>取消</button>
      <button className="primary" disabled={busy} onClick={onConfirm}>{confirmLabel}</button>
    </div>
  </dialog>;
}

export function Status({ error, message }: { error?: string; message?: string }) {
  if (error) return <p className="notice error" role="alert">{error}</p>;
  if (message) return <p className="notice" role="status">{message}</p>;
  return null;
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

export function useLoad<T>(load: () => Promise<T>, key: string | number): { value?: T; error?: string } {
  const [state, setState] = useState<{ key: string | number; value?: T; error?: string }>();
  useEffect(() => {
    let cancelled = false;
    load().then(
      (value) => { if (!cancelled) setState({ key, value }); },
      (error) => { if (!cancelled) setState({ key, error: errorText(error) }); },
    );
    return () => { cancelled = true; };
  }, [key]);
  return state?.key === key ? { value: state.value, error: state.error } : {};
}

// 需要用户就地修正的错误，仍留在按钮或对话框附近，不转成 Toast。
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
