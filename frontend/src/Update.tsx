import { AlertCircle, Check, Copy, X } from 'lucide-react';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { rpc } from './rpc';
import { toast } from './toast';
import { ConfirmDialog, StatusNotice, copyTextToClipboard, useAction } from './ui';
import { UpdateNotes } from './UpdateNotes';

type Available = { version: string; notes: string; size: number; sha256: string };
type Notice = { ok: boolean; version: string; message: string };
type UpdateState = {
  current: string;
  enabled: boolean;
  available?: Available;
  notice?: Notice;
  error?: string;
  logPath?: string;
  busy: boolean;
};
type Progress = { done: boolean; downloaded: number; total: number };

const listeners = new Set<() => void>();
let state: UpdateState | undefined;
let progress: Progress | undefined;

function emit() {
  for (const listener of listeners) listener();
}
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
function setState(next: UpdateState) {
  state = next;
  emit();
}
function clearStateError() {
  if (!state?.error) return;
  state = { ...state, error: undefined };
  emit();
}
function setProgress(next?: Progress) {
  progress = next;
  emit();
}
const getState = () => state;
const getProgress = () => progress;

const dismissedKey = 'atm.update.dismissedVersion';

function readDismissedVersion(): string | undefined {
  try {
    return localStorage.getItem(dismissedKey) ?? undefined;
  } catch {
    return undefined;
  }
}

function rememberDismissedVersion(version: string) {
  try {
    localStorage.setItem(dismissedKey, version);
  } catch {
    // 忽略本地存储限制，不阻断更新流程。
  }
}

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
  const [copied, setCopied] = useState(false);
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const dismissedVersion = useRef(readDismissedVersion());
  const checking = useAction();
  const installing = useAction();

  useEffect(() => {
    return () => {
      if (copyTimerRef.current !== undefined) clearTimeout(copyTimerRef.current);
    };
  }, []);

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
    installing.reset();
    clearStateError();
    setCopied(false);
    void checking.run(async () => {
      const next = await rpc<UpdateState>('desktop.update.check');
      setState(next);
      return next.available ? `发现新版本 ${next.available.version}` : '当前已是最新稳定版本';
    }, { success: 'toast', failure: 'inline' });
  }

  function install() {
    checking.reset();
    clearStateError();
    setCopied(false);
    setConfirm(false);
    setProgress({ done: false, downloaded: 0, total: view?.available?.size ?? 0 });
    void installing.run(async () => {
      try {
        const result = await rpc<{ started: boolean }>('desktop.update.install', { installDir: '' });
        if (!result.started) throw new Error('更新未能启动，请重试。');
        await rpc<void>('desktop.quit');
        return '';
      } catch (error) {
        setProgress(undefined);
        throw error;
      }
    }, { success: 'silent', failure: 'inline' });
  }

  const available = view?.available;
  const busy = checking.busy || installing.busy || !!view?.busy;
  const percent = downloading && downloading.total > 0
    ? Math.min(100, Math.round((downloading.downloaded / downloading.total) * 100))
    : 0;
  const sizeMB = available?.size ? (available.size / (1024 * 1024)).toFixed(1) : undefined;

  useEffect(() => {
    if (available && available.version !== dismissedVersion.current) setNotesOpen(true);
  }, [available?.version]);

  function later() {
    if (available) {
      dismissedVersion.current = available.version;
      rememberDismissedVersion(available.version);
    }
    setNotesOpen(false);
  }

  const errText = view?.error || checking.error || installing.error || undefined;

  useEffect(() => {
    setCopied(false);
  }, [errText, view?.logPath]);

  async function copyError() {
    if (!errText) return;
    const lines = [
      view?.current ? `当前版本：v${view.current}` : undefined,
      available?.version ? `目标版本：v${available.version}` : undefined,
      `错误信息：${errText}`,
      view?.logPath ? `日志位置：${view.logPath}` : undefined,
    ].filter(Boolean);
    const ok = await copyTextToClipboard(lines.join('\n'));
    if (ok) {
      setCopied(true);
      if (copyTimerRef.current !== undefined) clearTimeout(copyTimerRef.current);
      copyTimerRef.current = setTimeout(() => setCopied(false), 2000);
      toast.success('已复制错误信息');
    } else {
      toast.error('复制失败，请手动选中复制');
    }
  }

  return (
    <section className="card">
      <div className="card-header">
        <h2 className="card-title">软件更新</h2>
        <span className="card-sub mono">{view?.current ? `v${view.current} · Windows` : 'Windows'}</span>
      </div>
      <div className="setting-list">
        <div className="setting-row">
          <div className="setting-copy">
            <span className="setting-title">
              {available ? `发现新版本 v${available.version}` : '当前已是最新稳定版本'}
            </span>
            <span className="setting-desc">
              {available
                ? `${sizeMB ? `安装包约 ${sizeMB} MB · ` : ''}安装前校验 Ed25519 签名与 SHA-256`
                : '应用启动时会自动静默检查 GitHub 稳定版发布'}
            </span>
          </div>
          <div className="view-actions">
            <button
              type="button"
              className="btn btn-sm"
              disabled={busy || view?.enabled === false}
              onClick={check}
            >
              {checking.busy ? '检查中…' : '检查更新'}
            </button>
            {available && (
              <button
                type="button"
                className="btn btn-sm btn-primary"
                disabled={installing.busy || !!downloading}
                onClick={() => setConfirm(true)}
              >
                {downloading ? `下载中 ${percent}%` : '下载并安装'}
              </button>
            )}
          </div>
        </div>
      </div>

      {view && !view.enabled && (
        <div style={{ padding: '0 18px 16px' }}>
          <StatusNotice message="正式签名密钥尚未配置，应用内更新暂不可用。" />
        </div>
      )}

      {(available || downloading) && (
        <div className="update-notes">
          {available && (
            <UpdateNotes version={available.version} notes={available.notes} variant="card" />
          )}
          {downloading && (
            <div className="update-progress" role="status" aria-live="polite">
              <div className="update-progress-head">
                <span>正在下载安装包</span>
                <span className="mono tabular">
                  {downloading.total > 0 ? `${percent}%` : '连接中…'}
                </span>
              </div>
              <div className="progress-bar">
                <div className="progress-fill" style={{ width: `${percent}%` }} />
              </div>
            </div>
          )}
        </div>
      )}

      {errText && (
        <div style={{ padding: '0 18px 16px' }}>
          <div className="notice-banner error notice-banner-rich" role="alert">
            <div className="notice-banner-head">
              <div className="notice-banner-title">
                <AlertCircle size={15} aria-hidden="true" />
                <span className="update-error-text">{errText}</span>
              </div>
            </div>
            <div className="notice-banner-body">
              {view?.logPath && <div className="update-error-log mono">日志位置：{view.logPath}</div>}
              <div className="notice-banner-actions">
                <button type="button" className="btn btn-sm" onClick={() => void copyError()}>
                  {copied ? <Check size={13} aria-hidden="true" /> : <Copy size={13} aria-hidden="true" />}
                  <span>{copied ? '已复制' : '复制错误信息'}</span>
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {available && notesOpen && (
        <UpdateNotesDialog
          available={available}
          onLater={later}
          onInstall={() => {
            setNotesOpen(false);
            setConfirm(true);
          }}
        />
      )}

      {confirm && (
        <ConfirmDialog
          title={`下载并安装 v${available?.version ?? ''}？`}
          confirmLabel="确认安装"
          busy={installing.busy}
          onCancel={() => setConfirm(false)}
          onConfirm={install}
        >
          <p>下载完成后应用将自动退出并启动更新程序，原有配置与记录会完整保留。</p>
          <p style={{ color: 'var(--text-muted)', fontSize: 12.5 }}>下载或校验失败时不会替换当前版本。</p>
        </ConfirmDialog>
      )}
    </section>
  );
}

function UpdateNotesDialog({
  available,
  onLater,
  onInstall,
}: {
  available: Available;
  onLater: () => void;
  onInstall: () => void;
}) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onLater();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onLater]);

  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target === event.currentTarget) onLater();
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="update-notes-title">
        <div className="modal-head">
          <div className="modal-title-group">
            <h2 className="modal-title" id="update-notes-title">
              发现新版本
            </h2>
            <span className="release-notes-ver mono">v{available.version}</span>
          </div>
          <button type="button" className="icon-btn" onClick={onLater} aria-label="关闭">
            <X size={15} aria-hidden="true" />
          </button>
        </div>
        <div className="modal-body">
          <div className="update-notes-dialog-body">
            <UpdateNotes version={available.version} notes={available.notes} variant="dialog" />
          </div>
        </div>
        <div className="modal-foot">
          <button type="button" className="btn btn-sm" onClick={onLater}>
            稍后
          </button>
          <button type="button" className="btn btn-sm btn-primary" onClick={onInstall}>
            下载并安装
          </button>
        </div>
      </div>
    </div>
  );
}
