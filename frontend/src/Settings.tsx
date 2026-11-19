import { useState } from 'react';
import { AppearanceRow, type AppearanceControl } from './Appearance';
import { rpc, type Preferences } from './rpc';
import { UpdateSection } from './Update';
import { ConfirmDialog, StatusNotice, Switch, useAction, useLoad } from './ui';

export function Settings({ appearance }: { appearance: AppearanceControl }) {
  const { value, error } = useLoad(() => rpc<Preferences>('desktop.get'), 'desktop');
  const [startup, setStartup] = useState<boolean>();
  const [quitOpen, setQuitOpen] = useState(false);
  const action = useAction();
  const quit = useAction();
  const enabled = startup ?? value?.startup ?? false;

  function toggleStartup(next: boolean) {
    setStartup(next);
    void action.run(async () => {
      try {
        const persisted = await rpc<Preferences>('desktop.startup', { enabled: next });
        setStartup(persisted.startup);
        return persisted.startup ? '已开启登录自动运行' : '已关闭登录自动运行';
      } catch (err) {
        setStartup(enabled);
        throw err;
      }
    }, { success: 'toast', failure: 'toast' });
  }

  function confirmQuit() {
    void quit.run(async () => {
      await rpc<void>('desktop.quit');
    }, { success: 'silent', failure: 'inline' });
  }

  return (
    <main className="main narrow">
      <div className="view-bar">
        <div className="view-meta">
          <h1 className="view-title-inline">应用设置</h1>
          <span>桌面外观、启动项与软件更新</span>
        </div>
      </div>

      <div className="section-stack">
        <section className="card">
          <div className="card-header">
            <h2 className="card-title">通用与外观</h2>
          </div>
          <div className="setting-list">
            <AppearanceRow {...appearance} />
            {value?.startupAvailable && (
              <div className="setting-row">
                <div className="setting-copy">
                  <span className="setting-title">登录 Windows 后自动运行</span>
                  <span className="setting-desc">开机后静默驻留系统托盘，自动监听已启用的 Agent</span>
                </div>
                <Switch
                  checked={enabled}
                  disabled={action.busy}
                  label="登录 Windows 后自动运行"
                  onChange={toggleStartup}
                />
              </div>
            )}
          </div>
          {(error || action.error) && (
            <div style={{ padding: '0 18px 14px' }}>
              <StatusNotice error={error ?? action.error} />
            </div>
          )}
        </section>

        <UpdateSection />

        <section className="card">
          <div className="setting-list">
            <div className="setting-row">
              <div className="setting-copy">
                <span className="setting-title">后台运行</span>
                <span className="setting-desc">关闭窗口仅最小化到系统托盘；如需彻底停止后台服务请点击退出</span>
              </div>
              <button
                type="button"
                className="btn btn-sm btn-danger-soft"
                disabled={quit.busy}
                onClick={() => {
                  quit.reset();
                  setQuitOpen(true);
                }}
              >
                退出桌面应用
              </button>
            </div>
          </div>
        </section>
      </div>

      {quitOpen && (
        <ConfirmDialog
          title="退出 Agent Title Manager？"
          confirmLabel="确认退出"
          danger
          busy={quit.busy}
          error={quit.error}
          onCancel={() => setQuitOpen(false)}
          onConfirm={confirmQuit}
        >
          <p>退出后将停止托盘后台服务，各 Agent 将暂停自动生成标题，直到再次打开本应用。</p>
        </ConfirmDialog>
      )}
    </main>
  );
}
