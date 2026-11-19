import { useEffect, useRef, useSyncExternalStore, useState } from 'react';
import { rpc } from './rpc';
import { toast } from './toast';
import { ConfirmDialog, Section, Status, useAction } from './ui';
import './update.css';

type Available = { version: string; notes: string; size: number; sha256: string };
type Notice = { ok: boolean; version: string; message: string };
type UpdateState = {
  current: string; enabled: boolean; available?: Available; notice?: Notice;
  error?: string; busy: boolean;
};
type Progress = { done: boolean; downloaded: number; total: number };

// Module store lets the App subscribe once, so background checks and restart
// results reach the UI from any page, not only while settings is mounted.
const listeners = new Set<() => void>();
let state: UpdateState | undefined;
let progress: Progress | undefined;

function emit() { for (const listener of listeners) listener(); }
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
function setState(next: UpdateState) { state = next; emit(); }
function setProgress(next?: Progress) { progress = next; emit(); }
const getState = () => state;
const getProgress = () => progress;

// Called once at app root. Keeps subscriptions alive regardless of page.
export function useUpdateEvents() {
  useEffect(() => {
    const events = window.wails?.Events;
    if (!events) return;
    const stops = [
      events.On('desktop:update-state', ({ data }) => setState(data as UpdateState)),
      events.On('desktop:update-progress', ({ data }) => {
        const event = data as Progress;
        setProgress(event.done ? undefined : event);
      }),
      events.On('desktop:update-status', () => {
        // Fetch consumes the helper result and returns it as notice.
        void rpc<UpdateState>('desktop.update.get').then((next) => {
          setState(next);
          if (next.notice) {
            if (next.notice.ok) toast.success(next.notice.message);
            else toast.error(next.notice.message);
          }
        }, () => undefined);
      }),
    ];
    return () => {
      for (const stop of stops) stop?.();
    };
  }, []);
}

export function UpdateSection() {
  const view = useSyncExternalStore(subscribe, getState);
  const downloading = useSyncExternalStore(subscribe, getProgress);
  const [loaded, setLoaded] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [notesOpen, setNotesOpen] = useState(false);
  const checking = useAction();
  const installing = useAction();

  // Initial fetch also collects a pending helper result if the event was missed.
  useEffect(() => {
    if (loaded) return;
    setLoaded(true);
    void rpc<UpdateState>('desktop.update.get').then((next) => {
      setState(next);
      if (next.notice) {
        if (next.notice.ok) toast.success(next.notice.message);
        else toast.error(next.notice.message);
      }
    }, () => undefined);
  }, [loaded]);

  function check() {
    void checking.run(async () => {
      const next = await rpc<UpdateState>('desktop.update.check');
      setState(next);
      return next.available ? `发现新版本 ${next.available.version}` : '当前已是最新版本';
    }, { success: 'toast', failure: 'toast' });
  }

  function install() {
    setConfirm(false);
    setProgress({ done: false, downloaded: 0, total: view?.available?.size ?? 0 });
    void installing.run(async () => {
      try {
        const result = await rpc<{ started: boolean }>('desktop.update.install', { installDir: '' });
        if (!result.started) throw new Error('更新未能启动，请重试。');
        // The helper waits for this process to exit before installing.
        await rpc<void>('desktop.quit');
        return '';
      } catch (error) {
        setProgress(undefined);
        throw error;
      }
    }, { success: 'silent', failure: 'toast' });
  }

  const busy = checking.busy || installing.busy;
  const available = view?.available;

  useEffect(() => {
    if (available) setNotesOpen(true);
  }, [available?.version]);

  return <Section title="软件更新">
    <div className="settings-rows update-rows">
      <div className="setting-row">
        <span>当前版本</span>
        <strong className="update-version">{view?.current ?? '—'}</strong>
      </div>
      <div className="setting-row">
        <span>{available ? `发现新版本 ${available.version}` : '更新状态'}</span>
        <div className="update-actions">
          <button type="button" disabled={busy || view?.enabled === false} onClick={check}>
            {checking.busy ? '检查中…' : '检查更新'}
          </button>
          {available && <button type="button" className="primary" disabled={installing.busy}
            onClick={() => setConfirm(true)}>下载并安装</button>}
        </div>
      </div>
      {view && !view.enabled && <p className="notice">正式签名密钥尚未配置，应用内更新暂不可用。</p>}
      {downloading && <div className="update-progress" role="status" aria-live="polite">
        <div className="update-progress-track">
          <div className="update-progress-fill" style={{
            width: downloading.total > 0 ? `${Math.min(100, downloading.downloaded / downloading.total * 100)}%` : '0%',
          }} />
        </div>
        <span>正在下载安装包{downloading.total > 0 ? ` ${Math.round(downloading.downloaded / downloading.total * 100)}%` : '…'}</span>
      </div>}
      <Status error={view?.error ?? checking.error ?? installing.error} />
    </div>
    {available && notesOpen && <UpdateNotesDialog available={available}
      onLater={() => setNotesOpen(false)} onInstall={() => { setNotesOpen(false); setConfirm(true); }} />}
    {confirm && <ConfirmDialog title="下载并安装更新" confirmLabel="确认安装" busy={installing.busy}
      error={installing.error} onCancel={() => setConfirm(false)} onConfirm={install}>
      <p>将下载并安装 <strong>{available?.version}</strong>。安装会在应用退出后自动进行，完成后重新打开。</p>
      <p className="muted">下载或校验失败时不会替换当前版本。</p>
    </ConfirmDialog>}
  </Section>;
}

function UpdateNotesDialog({ available, onLater, onInstall }: {
  available: Available; onLater: () => void; onInstall: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const element = dialog.current;
    element?.showModal();
    return () => element?.close();
  }, []);
  return <dialog ref={dialog} className="update-notes-dialog" aria-label={`更新到 v${available.version}`}
    onCancel={(event) => { event.preventDefault(); onLater(); }}>
    <h2>更新到 v{available.version}</h2>
    <p className="muted update-notes-summary">版本 {available.version}</p>
    <pre className="update-notes">{available.notes || '本次更新未提供说明。'}</pre>
    <div className="actions">
      <button autoFocus onClick={onLater}>稍后</button>
      <button className="primary" onClick={onInstall}>下载并安装</button>
    </div>
  </dialog>;
}
