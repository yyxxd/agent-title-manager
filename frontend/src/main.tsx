import { Moon, Settings as SettingsIcon, Sun } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Agents } from './Agents';
import { useAppearance } from './Appearance';
import { ZheerLogo } from './icons';
import { Models } from './Models';
import { Overview } from './Overview';
import { syncPluginAutoUpdate, usePluginAutoUpdateEvents } from './pluginAutoUpdate';
import { Records, type RecordFilter } from './Records';
import {
  rpc,
  recordCounts,
  type Plugin,
  type PluginListResponse,
  type RecordCounts,
  type RecordList,
  type Settings as SettingsData,
} from './rpc';
import { Settings } from './Settings';
import { Toaster } from './toast';
import { ConfirmDialog } from './ui';
import { useUpdateEvents } from './Update';
import './tokens.css';
import './components.css';
import './overlays.css';
import './pages.css';
import './views.css';
import './records-settings.css';

type Page = 'overview' | 'agents' | 'models' | 'records' | 'settings';

function App() {
  useUpdateEvents();
  const appearance = useAppearance();
  const [page, setPage] = useState<Page>('overview');
  const [revision, setRevision] = useState(0);
  const [modelsDirty, setModelsDirty] = useState(false);
  const [pendingNav, setPendingNav] = useState<{ page: Page; recordFilter?: RecordFilter } | null>(null);
  const [recordFilter, setRecordFilter] = useState<{ filter: RecordFilter; key: number }>({
    filter: 'all',
    key: 0,
  });

  const [shell, setShell] = useState<{
    plugins: Plugin[];
    settings?: SettingsData;
    counts: RecordCounts;
  }>({
    plugins: [],
    counts: recordCounts([]),
  });

  const refreshShell = useCallback(() => {
    void Promise.allSettled([
      rpc<PluginListResponse>('plugins.list'),
      rpc<SettingsData>('settings.get'),
      rpc<RecordList>('records.list', { limit: 50 }),
    ]).then(([pluginsRes, settingsRes, recordsRes]) => {
      if (pluginsRes.status === 'fulfilled') {
        syncPluginAutoUpdate(pluginsRes.value.autoUpdate);
      }
      setShell((prev) => {
        const plugins = pluginsRes.status === 'fulfilled' ? pluginsRes.value.items ?? [] : prev.plugins;
        const settings = settingsRes.status === 'fulfilled' ? settingsRes.value : prev.settings;
        const counts = recordsRes.status === 'fulfilled'
          ? recordsRes.value.counts ?? recordCounts(recordsRes.value.items)
          : prev.counts;
        return { plugins, settings, counts };
      });
    });
  }, []);

  usePluginAutoUpdateEvents(refreshShell);

  useEffect(() => {
    refreshShell();
  }, [refreshShell, revision]);

  function commitNav(next: Page, filter?: RecordFilter) {
    if (next === 'records' && filter) {
      setRecordFilter((prev) => ({ filter, key: prev.key + 1 }));
    }
    setPage(next);
    setRevision((n) => n + 1);
  }

  function navigateTo(next: Page, filter?: RecordFilter) {
    if (next === page) {
      if (next === 'records' && filter) {
        setRecordFilter((prev) => ({ filter, key: prev.key + 1 }));
      }
      return;
    }
    if (page === 'models' && modelsDirty) {
      setPendingNav({ page: next, recordFilter: filter });
      return;
    }
    commitNav(next, filter);
  }

  const attnCount = shell.counts.attention;
  const agents = shell.plugins.filter((p) => p.kind !== 'integration' && p.id !== 'orca');
  const enabledCount = agents.filter((p) => p.enabled).length;
  const totalCount = agents.length;
  const isDark = appearance.state?.theme === 'dark';

  return (
    <>
      <header className="topbar">
        <div className="topbar-inner">
          <div className="brand">
            <span className="brand-mark">
              <ZheerLogo size={24} />
            </span>
            <span className="brand-name">Agent Title Manager</span>
          </div>

          <nav className="topnav" aria-label="主导航">
            <button
              type="button"
              className={`nav-btn ${page === 'overview' ? 'active' : ''}`}
              aria-current={page === 'overview' ? 'page' : undefined}
              onClick={() => navigateTo('overview')}
            >
              概览
            </button>
            <button
              type="button"
              className={`nav-btn ${page === 'agents' ? 'active' : ''}`}
              aria-current={page === 'agents' ? 'page' : undefined}
              onClick={() => navigateTo('agents')}
            >
              Agent
            </button>
            <button
              type="button"
              className={`nav-btn ${page === 'models' ? 'active' : ''}`}
              aria-current={page === 'models' ? 'page' : undefined}
              onClick={() => navigateTo('models')}
            >
              模型
            </button>
            <button
              type="button"
              className={`nav-btn ${page === 'records' ? 'active' : ''}`}
              aria-current={page === 'records' ? 'page' : undefined}
              onClick={() => navigateTo('records')}
            >
              <span>记录</span>
              {attnCount > 0 && <span className="nav-count">{attnCount}</span>}
            </button>
          </nav>

          <div className="topbar-right">
            {attnCount > 0 ? (
              <button
                type="button"
                className="status-chip warn"
                title="查看需要处理的记录"
                onClick={() => navigateTo('records', 'attention')}
              >
                <span className="status-dot warn" />
                <span>{attnCount} 条待处理</span>
              </button>
            ) : (
              <button
                type="button"
                className="status-chip"
                title="查看已启用的 Agent"
                onClick={() => navigateTo('agents')}
              >
                <span className={`status-dot ${enabledCount > 0 ? 'ok' : 'off'}`} />
                <span>
                  {totalCount > 0
                    ? `Agent ${enabledCount}/${totalCount}`
                    : '读取中'}
                </span>
              </button>
            )}

            <button
              type="button"
              className="icon-btn"
              title="切换浅色/深色外观"
              aria-label="切换浅色或深色外观"
              onClick={() => void appearance.toggleLightDark()}
            >
              {isDark ? <Sun size={16} aria-hidden="true" /> : <Moon size={16} aria-hidden="true" />}
            </button>

            <button
              type="button"
              className={`settings-trigger ${page === 'settings' ? 'active' : ''}`}
              aria-current={page === 'settings' ? 'page' : undefined}
              aria-label="应用设置"
              onClick={() => navigateTo('settings')}
            >
              <SettingsIcon size={15} aria-hidden="true" />
              <span>设置</span>
            </button>
          </div>
        </div>
      </header>

      {page === 'overview' && (
        <Overview
          revision={revision}
          plugins={shell.plugins}
          settings={shell.settings}
          counts={shell.counts}
          onNavigate={navigateTo}
          onRefreshShell={refreshShell}
        />
      )}
      {page === 'agents' && <Agents onChanged={refreshShell} />}
      {page === 'models' && <Models onSaved={refreshShell} onDirtyChange={setModelsDirty} />}
      {page === 'records' && (
        <Records
          initialFilter={recordFilter.filter}
          filterKey={recordFilter.key}
          onChanged={refreshShell}
        />
      )}
      {page === 'settings' && <Settings appearance={appearance} />}

      <Toaster />

      {pendingNav && (
        <ConfirmDialog
          title="放弃未保存的模型修改？"
          confirmLabel="放弃并离开"
          danger
          onCancel={() => setPendingNav(null)}
          onConfirm={() => {
            const target = pendingNav;
            setPendingNav(null);
            setModelsDirty(false);
            commitNav(target.page, target.recordFilter);
          }}
        >
          <p>当前页面的模型配置尚未保存，离开后修改将被还原。</p>
        </ConfirmDialog>
      )}
    </>
  );
}

const runtimePath = '/wails/runtime.js';
void import(/* @vite-ignore */ runtimePath)
  .catch(() => undefined)
  .finally(() => {
    createRoot(document.getElementById('root')!).render(<App />);
  });
