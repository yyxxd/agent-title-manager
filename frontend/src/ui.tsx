import { useEffect, useState, type ReactNode } from 'react';
import { errorText } from './rpc';

export function Section({ title, children, action }: {
  title: string; children: ReactNode; action?: ReactNode;
}) {
  return <section className="panel">
    <header className="panel-head"><h2>{title}</h2>{action}</header>
    {children}
  </section>;
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

export function useAction() {
  const [status, setStatus] = useState<{ busy: boolean; error?: string; message?: string }>({ busy: false });
  return {
    ...status,
    async run(action: () => Promise<string | void>) {
      setStatus({ busy: true });
      try {
        const message = await action();
        setStatus({ busy: false, message: message || undefined });
      } catch (error) {
        setStatus({ busy: false, error: errorText(error) });
      }
    },
  };
}
