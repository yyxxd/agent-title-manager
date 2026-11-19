import { useEffect, useState } from 'react';
import { errorText, rpc } from './rpc';
import { toast } from './toast';
import { ConfirmDialog, Section, Status, useAction, useLoad } from './ui';
import './update.css';

type Available = { version: string; notes: string; size: number; sha256: string };
type UpdateState = { current: string; enabled: boolean; available?: Available; error?: string; busy: boolean };

export function UpdateSection() {
  const { value, error } = useLoad(() => rpc<UpdateState>('desktop.update.get'), 'update');
  const [state, setState] = useState<UpdateState>();
  const [confirm, setConfirm] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number }>();
  const checking = useAction();
  const installing = useAction();
  const view = state ?? value;

  useEffect(() => {
    const stop = window.wails?.Events?.On('desktop:update-progress', ({ data }) => {
      const event = data as { downloaded: number; total: number };
      setProgress({ done: event.downloaded, total: event.total });
    });
    return stop;
  }, []);
  useEffect(() => {
    const stop = window.wails?.Events?.On('desktop:update-status', ({ data }) => {
      const status = data as { ok: boolean; version: string; message: string };
      if (status.ok) toast.success(status.message);
      else toast.error(status.message);
    });
    return stop;
  }, []);

  function check() {
    void checking.run(async () => {
      const next = await rpc<UpdateState>('desktop.update.check');
      setState(next);
      return next.available
        ? `发现新版本 ${next.available.version}`
        : '当前已是最新版本';
    }, { success: 'toast', failure: 'toast' });
  }

  function install() {
    setConfirm(false);
    setProgress({ done: 0, total: view?.available?.size ?? 0 });
    void installing.run(async () => {
      const result = await rpc<{ started: boolean }>('desktop.update.install', {
        installDir: '',
      });
      if (!result.started) throw new Error('更新未能启动，请重试。');
      // The helper waits for this process to exit before installing.
      await rpc<void>('desktop.quit');
      return '';
    }, { success: 'silent', failure: 'toast' });
  }

  const busy = checking.busy || installing.busy;
  const available = view?.available;

  return <Section title="软件更新">
    <div className="settings-rows update-rows">
      <div className="setting-row">
        <span>当前版本</span>
        <strong className="update-version">{view?.current ?? '—'}</strong>
      </div>
      <div className="setting-row">
        <span>{available ? `发现新版本 ${available.version}` : '更新状态'}</span>
        <div className="update-actions">
          <button type="button" disabled={busy || !view?.enabled} onClick={check}>
            {checking.busy ? '检查中…' : '检查更新'}
          </button>
          {available && <button type="button" className="primary" disabled={installing.busy}
            onClick={() => setConfirm(true)}>下载并安装</button>}
        </div>
      </div>
      {view && !view.enabled && <p className="notice">正式签名密钥尚未配置，应用内更新暂不可用。</p>}
      {available && <>
        <div className="setting-row update-notes-row">
          <span>更新说明</span>
          <pre className="update-notes">{available.notes || '本次更新未提供说明。'}</pre>
        </div>
        {progress && <div className="update-progress" role="status" aria-live="polite">
          <div className="update-progress-track">
            <div className="update-progress-fill" style={{
              width: progress.total > 0 ? `${Math.min(100, progress.done / progress.total * 100)}%` : '0%',
            }} />
          </div>
          <span>正在下载安装包{progress.total > 0 ? ` ${Math.round(progress.done / progress.total * 100)}%` : '…'}</span>
        </div>}
      </>}
      <Status error={error ?? checking.error ?? installing.error ?? view?.error} />
    </div>
    {confirm && <ConfirmDialog title="下载并安装更新" confirmLabel="确认安装" busy={installing.busy}
      error={installing.error} onCancel={() => setConfirm(false)} onConfirm={install}>
      <p>将下载并安装 <strong>{available?.version}</strong>。安装会在应用退出后自动进行，完成后重新打开。</p>
      <p className="muted">下载或校验失败时不会替换当前版本。</p>
    </ConfirmDialog>}
  </Section>;
}
