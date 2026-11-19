import { AlertCircle, CheckCircle2, X } from 'lucide-react';
import { useEffect, useSyncExternalStore } from 'react';

export type ToastKind = 'success' | 'error';
type ToastItem = { id: number; kind: ToastKind; message: string };

const durations: Record<ToastKind, number> = { success: 3000, error: 5000 };
const limit = 3;
const timers = new Map<number, ReturnType<typeof setTimeout>>();
const listeners = new Set<() => void>();
let items: ToastItem[] = [];
let nextId = 0;

function emit() {
  for (const listener of listeners) listener();
}

function clearTimer(id: number) {
  const timer = timers.get(id);
  if (timer === undefined) return;
  clearTimeout(timer);
  timers.delete(id);
}

function remove(id: number) {
  clearTimer(id);
  const next = items.filter((item) => item.id !== id);
  if (next.length === items.length) return;
  items = next;
  emit();
}

function schedule(item: ToastItem) {
  clearTimer(item.id);
  timers.set(item.id, setTimeout(() => remove(item.id), durations[item.kind]));
}

// 相同提示复用同一条，连续触发时只重置计时，避免堆叠重复内容。
function push(kind: ToastKind, message: string) {
  const existing = items.find((item) => item.kind === kind && item.message === message);
  if (existing) {
    schedule(existing);
    return;
  }
  const dropped = items.length >= limit ? items[0] : undefined;
  const item = { id: ++nextId, kind, message };
  items = [...items, item].slice(-limit);
  if (dropped) clearTimer(dropped.id);
  schedule(item);
  emit();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

const getSnapshot = () => items;

export const toast = {
  success: (message: string) => push('success', message),
  error: (message: string) => push('error', message),
};

export function Toaster() {
  const current = useSyncExternalStore(subscribe, getSnapshot);
  useEffect(() => () => {
    for (const id of [...timers.keys()]) clearTimer(id);
  }, []);
  return <div className="toast-region" role="status" aria-live="polite">
    {current.map((item) => <div className={`toast toast-${item.kind}`} key={item.id}>
      {item.kind === 'success'
        ? <CheckCircle2 size={15} aria-hidden="true" />
        : <AlertCircle size={15} aria-hidden="true" />}
      <p>{item.message}</p>
      <button type="button" className="toast-close" aria-label="关闭提示" title="关闭提示"
        onClick={() => remove(item.id)}><X size={14} aria-hidden="true" /></button>
    </div>)}
  </div>;
}
