import { AlertCircle, CheckCircle2, Plus, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { AgentIcon } from './icons';
import {
  dismissFailedBanner,
  dismissUpgradedBanner,
  formatPluginVersionRange,
  syncPluginAutoUpdate,
  usePluginAutoUpdate,
} from './pluginAutoUpdate';
import { PluginModal, type PluginModalState } from './PluginModal';
import { rpc, type Plugin, type PluginListResponse } from './rpc';
import { toast } from './toast';
import { StatusNotice, Switch, useAction, useLoad } from './ui';

const builtinDescriptions: Record<string, string> = {
  codex: '监听 Codex 会话自动生成标题，并同步至 Orca。',
  claude: '首轮对话后在后台生成候选标题，下次交互时应用。',
  pi: '通过 Pi 扩展协议接入自动会话命名。',
  orca: '将各 Agent 生成的标题同步写回 Orca 会话列表。',
};

const statusPillMeta: Record<string, { className: string; dot: string; label: string }> = {
  enabled: { className: 'pill-success', dot: 'ok', label: '已启用' },
  disabled: { className: 'pill-neutral', dot: 'off', label: '未启用' },
  action_required: { className: 'pill-warning', dot: 'warn', label: '需操作' },
  unavailable: { className: 'pill-danger', dot: 'danger', label: '不可用' },
  recovery_required: { className: 'pill-danger', dot: 'danger', label: '待回滚' },
};

export function Agents({ onChanged }: { onChanged: () => void }) {
  const [refresh, setRefresh] = useState(0);
  const [modal, setModal] = useState<PluginModalState | null>(null);
  const { value, error, loading } = useLoad(() => rpc<PluginListResponse>('plugins.list'), refresh);
  const action = useAction();
  const { result: autoUpdate, upgradedDismissed, failedDismissed } = usePluginAutoUpdate();

  useEffect(() => {
    if (value?.autoUpdate) syncPluginAutoUpdate(value.autoUpdate);
  }, [value?.autoUpdate]);

  const items = value?.items ?? [];
  const builtin = items.filter((item) => item.kind === 'builtin');
  const community = items.filter((item) => item.kind !== 'builtin');

  const unresolvedFailed = (autoUpdate?.failed ?? []).filter((item) => {
    if (!item.id) return true;
    const plugin = items.find((p) => p.id === item.id);
    if (!plugin) return true;
    const resolvedByNoAvailable = !plugin.availableVersion;
    const resolvedByVersion = Boolean(item.targetVersion && plugin.version === item.targetVersion);
    return !(resolvedByNoAvailable || resolvedByVersion);
  });
  const upgradedItems = autoUpdate?.upgraded ?? [];
  const showFailedBanner = unresolvedFailed.length > 0 && !failedDismissed;
  const showUpgradedBanner = upgradedItems.length > 0 && !upgradedDismissed;

  function reload(successMessage?: string) {
    setRefresh((n) => n + 1);
    onChanged();
    if (successMessage) toast.success(successMessage);
  }

  function togglePlugin(plugin: Plugin, next: boolean) {
    const method = next ? 'plugins.enable' : 'plugins.disable';
    void action.run(async () => {
      await rpc(method, { id: plugin.id });
      reload(
        plugin.status === 'recovery_required' && !next
          ? `${plugin.name} 已回滚未完成变更`
          : `${plugin.name} 已${next ? '启用' : '停用'}`,
      );
    }, { success: 'silent', failure: 'toast' });
  }

  function updateBuiltin(plugin: Plugin) {
    void action.run(async () => {
      await rpc('plugins.update', { id: plugin.id, source: 'bundled' });
      reload(`${plugin.name} 已更新至 v${plugin.availableVersion ?? plugin.version}`);
    }, { success: 'silent', failure: 'toast' });
  }

  return (
    <main className="main narrow">
      <div className="view-bar">
        <div className="view-meta">
          <h1 className="view-title-inline">支持的 Agent</h1>
          <span>开启后即自动接管该 Agent 的会话命名</span>
        </div>
        <button type="button" className="btn btn-sm" onClick={() => setModal({ mode: 'install' })}>
          <Plus size={14} aria-hidden="true" />
          <span>安装社区插件</span>
        </button>
      </div>

      {error && (
        <div style={{ marginBottom: 16 }}>
          <StatusNotice error={error} />
        </div>
      )}

      {(showFailedBanner || showUpgradedBanner) && (
        <div className="section-stack" style={{ marginBottom: 16, gap: 12 }}>
          {showFailedBanner && (
            <div className="notice-banner error notice-banner-rich" role="alert">
              <div className="notice-banner-head">
                <div className="notice-banner-title">
                  <AlertCircle size={15} aria-hidden="true" />
                  <span>内置插件启动自动更新失败</span>
                </div>
                <button
                  type="button"
                  className="notice-banner-dismiss"
                  aria-label="关闭提示"
                  title="关闭提示"
                  onClick={dismissFailedBanner}
                >
                  <X size={14} aria-hidden="true" />
                </button>
              </div>
              <div className="notice-banner-body">
                <ul className="notice-banner-list">
                  {unresolvedFailed.map((item, index) => {
                    const label = item.name || item.id || '内置插件';
                    const ver = formatPluginVersionRange(item.currentVersion, item.targetVersion);
                    return (
                      <li key={`${item.id}:${item.name}:${index}`}>
                        <strong>{label}</strong>
                        {ver ? <span className="mono"> {ver}</span> : null}
                        {item.error ? `：${item.error}` : ''}
                      </li>
                    );
                  })}
                </ul>
                <p className="notice-banner-hint">
                  {unresolvedFailed.some((item) => Boolean(item.id))
                    ? '旧版本仍可继续使用，可点击下方对应插件的「更新至 v...」手动重试。'
                    : '旧版本仍可继续使用，请检查内置插件状态后重试。'}
                </p>
              </div>
            </div>
          )}

          {showUpgradedBanner && (
            <div className="notice-banner ok notice-banner-rich" role="status">
              <div className="notice-banner-head">
                <div className="notice-banner-title">
                  <CheckCircle2 size={15} aria-hidden="true" />
                  <span>
                    已自动更新内置插件：
                    {upgradedItems
                      .map((item) => {
                        const label = item.name || item.id || '内置插件';
                        const ver = formatPluginVersionRange(item.fromVersion, item.toVersion);
                        return ver ? `${label} ${ver}` : label;
                      })
                      .join('、')}
                  </span>
                </div>
                <button
                  type="button"
                  className="notice-banner-dismiss"
                  aria-label="关闭提示"
                  title="关闭提示"
                  onClick={dismissUpgradedBanner}
                >
                  <X size={14} aria-hidden="true" />
                </button>
              </div>
              {upgradedItems.some((item) => item.enabled) && (
                <div className="notice-banner-body">
                  <p className="notice-banner-hint">已打开的 Agent 会话将在下次重启后应用新版本。</p>
                </div>
              )}
            </div>
          )}
        </div>
      )}

      <div className="section-stack">
        <section className="card">
          {builtin.length > 0 ? (
            <div className="agent-list">
              {builtin.map((plugin) => (
                <PluginRow
                  key={plugin.id}
                  plugin={plugin}
                  busy={action.busy}
                  onToggle={(next) => togglePlugin(plugin, next)}
                  onRollback={() => togglePlugin(plugin, false)}
                  onUpdateBuiltin={() => updateBuiltin(plugin)}
                />
              ))}
            </div>
          ) : (
            <div className="empty-inline">
              <span>{loading ? '正在读取内置适配器…' : '未读取到内置适配器'}</span>
            </div>
          )}
        </section>

        <section className="card">
          <div className="card-header">
            <h2 className="card-title">社区插件</h2>
            <span className="card-sub">{community.length} 个已安装</span>
          </div>
          {community.length > 0 ? (
            <div className="agent-list">
              {community.map((plugin) => (
                <PluginRow
                  key={plugin.id}
                  plugin={plugin}
                  busy={action.busy}
                  onToggle={(next) => togglePlugin(plugin, next)}
                  onRollback={() => togglePlugin(plugin, false)}
                  onUpdateCommunity={() => setModal({ mode: 'update', plugin })}
                />
              ))}
            </div>
          ) : (
            <div className="empty-inline">
              <span>尚未安装社区插件，可从本地目录或 GitHub 仓库扩展更多 Agent。</span>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => setModal({ mode: 'install' })}>
                去安装
              </button>
            </div>
          )}
        </section>
      </div>

      {modal && (
        <PluginModal
          modal={modal}
          onClose={() => setModal(null)}
          onSuccess={(msg) => {
            setModal(null);
            reload(msg);
          }}
        />
      )}
    </main>
  );
}

function PluginRow({
  plugin,
  busy,
  onToggle,
  onRollback,
  onUpdateBuiltin,
  onUpdateCommunity,
}: {
  plugin: Plugin;
  busy: boolean;
  onToggle: (next: boolean) => void;
  onRollback: () => void;
  onUpdateBuiltin?: () => void;
  onUpdateCommunity?: () => void;
}) {
  const pill =
    statusPillMeta[plugin.status] ??
    (plugin.enabled ? statusPillMeta.enabled : statusPillMeta.disabled);
  const desc = builtinDescriptions[plugin.id] ?? (plugin.kind === 'builtin' ? '' : '社区扩展 Agent 适配器');
  const needsRollback = plugin.status === 'recovery_required';

  return (
    <div className="agent-item">
      <div className="agent-item-left">
        <div className="agent-avatar">
          <AgentIcon id={plugin.id} size={28} />
        </div>
        <div className="agent-copy">
          <div className="agent-title-row">
            <span className="agent-title">{plugin.name}</span>
            {plugin.version && <span className="agent-ver mono">v{plugin.version}</span>}
            <span className={`pill ${pill.className}`}>
              <span className={`status-dot ${pill.dot}`} />
              {pill.label}
            </span>
          </div>
          {desc && <p className="agent-desc">{desc}</p>}
          {plugin.detail && (
            <p
              className="agent-desc"
              style={{
                marginTop: desc ? 4 : 0,
                color:
                  plugin.status === 'recovery_required' || plugin.status === 'unavailable'
                    ? 'var(--danger)'
                    : plugin.status === 'action_required'
                      ? 'var(--warning)'
                      : 'var(--text-muted)',
                fontSize: 12.5,
              }}
            >
              {plugin.detail}
            </p>
          )}
          {plugin.entryPath && (
            <details className="agent-entry-details">
              <summary>插件入口路径</summary>
              <code>{plugin.entryPath}</code>
            </details>
          )}
        </div>
      </div>

      <div className="agent-item-right">
        {needsRollback && (
          <button type="button" className="btn btn-sm btn-danger-soft" disabled={busy} onClick={onRollback}>
            停用并回滚
          </button>
        )}
        {plugin.kind === 'builtin' && plugin.availableVersion && onUpdateBuiltin && (
          <button type="button" className="btn btn-sm" disabled={busy} onClick={onUpdateBuiltin}>
            更新至 v{plugin.availableVersion}
          </button>
        )}
        {plugin.kind !== 'builtin' && onUpdateCommunity && (
          <button
            type="button"
            className="btn btn-sm"
            disabled={busy || plugin.enabled}
            title={plugin.enabled ? '请先关闭插件后再更新' : undefined}
            onClick={onUpdateCommunity}
          >
            更新
          </button>
        )}
        <Switch
          checked={plugin.enabled}
          disabled={busy || needsRollback}
          label={`${plugin.enabled ? '停用' : '启用'} ${plugin.name}`}
          onChange={onToggle}
        />
      </div>
    </div>
  );
}
