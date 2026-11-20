import { AlertCircle, Calendar, RefreshCw } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { DatePopover } from './DatePopover';
import { ALL_TIME_START, formatRangeLabel, rangeDays, type DateRange } from './dates';
import { AgentIcon } from './icons';
import {
  computeScopeTokens,
  KpiGrid,
  ModelUsageSection,
  TokenFieldText,
  type AgentUsage,
} from './OverviewSections';
import {
  agentName,
  localDate,
  num,
  rpc,
  type Daily,
  type Plugin,
  type RecordCounts,
  type Settings,
} from './rpc';
import { toast } from './toast';
import { StatusNotice, useLoad } from './ui';

type DatePreset = 'today' | '7d' | 'all' | 'custom';

const agentColors: Record<string, string> = {
  codex: '#2563eb',
  claude: '#d97706',
  antigravity: '#06b6d4',
  pi: '#10b981',
  orca: '#6366f1',
};

const agentDescriptions: Record<string, string> = {
  codex: 'Codex',
  claude: 'Claude Code',
  antigravity: 'Antigravity',
  pi: 'Pi',
};

export function Overview({
  revision,
  plugins,
  settings,
  counts,
  onNavigate,
  onRefreshShell,
}: {
  revision: number;
  plugins: Plugin[];
  settings?: Settings;
  counts: RecordCounts;
  onNavigate: (page: 'agents' | 'models' | 'records', recordFilter?: 'attention') => void;
  onRefreshShell: () => void;
}) {
  const today = localDate();
  const [preset, setPreset] = useState<DatePreset>('today');
  const [customRange, setCustomRange] = useState<DateRange>({ start: today, end: today });
  const [popoverOpen, setPopoverOpen] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [agentFilter, setAgentFilter] = useState<string>('all');
  const popoverAnchorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!popoverOpen) return;
    const onPointerDown = (event: MouseEvent) => {
      if (popoverAnchorRef.current && !popoverAnchorRef.current.contains(event.target as Node)) {
        setPopoverOpen(false);
      }
    };
    document.addEventListener('mousedown', onPointerDown);
    return () => document.removeEventListener('mousedown', onPointerDown);
  }, [popoverOpen]);

  const activeRange: DateRange =
    preset === 'today'
      ? { start: today, end: today }
      : preset === '7d'
        ? rangeDays(today, 7)
        : preset === 'all'
          ? { start: ALL_TIME_START, end: today }
          : customRange;

  const { value, error, loading } = useLoad(
    () =>
      activeRange.start === activeRange.end
        ? rpc<Daily>('stats.daily', { date: activeRange.start })
        : rpc<Daily>('stats.range', activeRange),
    `${activeRange.start}:${activeRange.end}:${refresh}:${revision}`,
  );

  const rows = value?.byAgent ?? [];
  const agents: AgentUsage[] = [...rows.filter((row) => row.agent !== 'test')].sort(
    (a, b) => b.renames - a.renames || b.attempts - a.attempts || a.agent.localeCompare(b.agent),
  );
  const selectedAgent = agentFilter === 'all' ? undefined : agents.find((row) => row.agent === agentFilter);
  const models = value?.byModel ?? [];

  const agentPlugins = plugins.filter((p) => p.kind !== 'integration' && p.id !== 'orca');
  const enabledPlugins = agentPlugins.filter((p) => p.enabled);
  const enabledNames = enabledPlugins.map((p) => agentDescriptions[p.id] ?? p.name);
  const sourceLabel = settings
    ? settings.provider === 'codex'
      ? `Codex · ${settings.codexModel || '未选模型'}`
      : settings.provider === 'claude'
        ? `Claude · ${settings.claudeModel || '未选模型'}`
        : `自定义 API · ${settings.apiModel || '未填模型'}`
    : '读取中…';

  const attnParts: string[] = [];
  if (counts.failed > 0) attnParts.push(`${counts.failed} 条命名失败`);
  if (counts.syncFailed > 0) attnParts.push(`${counts.syncFailed} 条同步至 Orca 失败`);
  if (counts.protected > 0) attnParts.push(`${counts.protected} 条标题受保护`);

  const scope = selectedAgent ?? value;
  const usageMetrics = computeScopeTokens(scope, agents, value);
  const maxRenamed = Math.max(...agents.map((a) => a.renames), 1);

  return (
    <main className="main">
      {attnParts.length > 0 && (
        <div className="alert-strip" role="status">
          <span className="alert-strip-left">
            <AlertCircle size={15} aria-hidden="true" />
            <span>待处理：{attnParts.join('，')}</span>
          </span>
          <button type="button" className="alert-link" onClick={() => onNavigate('records', 'attention')}>
            前往处理 →
          </button>
        </div>
      )}

      <div className="view-bar">
        <div className="view-actions">
          <div className="segmented" role="group" aria-label="统计周期">
            <button
              type="button"
              className={`seg-btn ${preset === 'today' ? 'active' : ''}`}
              onClick={() => {
                setPreset('today');
                setPopoverOpen(false);
              }}
            >
              今日
            </button>
            <button
              type="button"
              className={`seg-btn ${preset === '7d' ? 'active' : ''}`}
              onClick={() => {
                setPreset('7d');
                setPopoverOpen(false);
              }}
            >
              近 7 天
            </button>
            <button
              type="button"
              className={`seg-btn ${preset === 'all' ? 'active' : ''}`}
              onClick={() => {
                setPreset('all');
                setPopoverOpen(false);
              }}
            >
              全部
            </button>
            <div className="popover-anchor" ref={popoverAnchorRef}>
              <button
                type="button"
                className={`seg-btn ${preset === 'custom' ? 'active' : ''}`}
                aria-expanded={popoverOpen}
                onClick={() => setPopoverOpen((open) => !open)}
              >
                <Calendar size={13} aria-hidden="true" />
                <span className="tabular">
                  {preset === 'custom'
                    ? customRange.start === customRange.end
                      ? customRange.start
                      : `${customRange.start} ~ ${customRange.end}`
                    : '自定义'}
                </span>
              </button>
              {popoverOpen && (
                <DatePopover
                  today={today}
                  value={activeRange}
                  onSelect={(next) => {
                    setCustomRange(next);
                    setPreset('custom');
                  }}
                  onSelectToday={() => setPreset('today')}
                  onClose={() => setPopoverOpen(false)}
                />
              )}
            </div>
          </div>
          <span className="card-sub tabular">{formatRangeLabel(activeRange)}</span>
        </div>

        <div className="view-actions">
          <button type="button" className="btn btn-sm" onClick={() => onNavigate('models')} title="当前生效的命名来源">
            <span style={{ color: 'var(--text-muted)' }}>当前来源</span>
            <span className="mono" style={{ fontSize: 12 }}>
              {sourceLabel}
            </span>
          </button>
          <button
            type="button"
            className="icon-btn bordered"
            title="刷新统计"
            aria-label="刷新统计"
            onClick={() => {
              setRefresh((v) => v + 1);
              onRefreshShell();
              toast.success('统计数据已刷新');
            }}
          >
            <RefreshCw size={14} aria-hidden="true" />
          </button>
        </div>
      </div>

      {error && (
        <div style={{ marginBottom: 16 }}>
          <StatusNotice error={error} />
        </div>
      )}

      {value ? (
        <>
          <KpiGrid
            value={value}
            agents={agents}
            enabledCount={enabledPlugins.length}
            totalCount={agentPlugins.length}
            enabledNames={enabledNames}
            onOpenAgents={() => onNavigate('agents')}
          />

          <section className="overview-split">
            <div className="card">
              <div className="card-header">
                <h2 className="card-title">Agent 分布</h2>
                <span className="card-sub">
                  {selectedAgent ? '点击所选行恢复全部' : '点击某行可筛选右侧明细'}
                </span>
              </div>
              {agents.length > 0 ? (
                <div className="agent-dist-list">
                  <div className="agent-dist-head">
                    <span>Agent</span>
                    <span>改名占比</span>
                    <span className="num">改名</span>
                    <span className="num">请求</span>
                  </div>
                  {agents.map((a) => {
                    const pct =
                      maxRenamed > 0 && a.renames > 0 ? Math.max(6, Math.round((a.renames / maxRenamed) * 100)) : 0;
                    const isSelected = selectedAgent?.agent === a.agent;
                    const color = agentColors[a.agent] ?? '#6366f1';
                    return (
                      <button
                        key={a.agent}
                        type="button"
                        className={`agent-dist-row ${isSelected ? 'selected' : ''}`}
                        onClick={() => setAgentFilter((cur) => (cur === a.agent ? 'all' : a.agent))}
                      >
                        <span className="agent-name-cell">
                          <span className="agent-icon-inline">
                            <AgentIcon id={a.agent} size={15} />
                          </span>
                          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {agentName(a.agent)}
                          </span>
                        </span>
                        <span className="bar-track">
                          <span className="bar-fill" style={{ width: `${pct}%`, background: color }} />
                        </span>
                        <span className="num tabular" style={{ fontWeight: 600 }}>
                          {num(a.renames)}
                        </span>
                        <span className="num tabular" style={{ color: 'var(--text-muted)' }}>
                          {num(a.attempts)}
                        </span>
                      </button>
                    );
                  })}
                </div>
              ) : (
                <div className="empty-inline">
                  <span>所选日期范围无 Agent 活动</span>
                </div>
              )}
            </div>

            <div className="card">
              <div className="card-header">
                <h2 className="card-title">Token 构成</h2>
                {selectedAgent ? (
                  <button
                    type="button"
                    className="pill pill-accent"
                    style={{ border: 0, cursor: 'pointer' }}
                    onClick={() => setAgentFilter('all')}
                    title="点击恢复全部"
                  >
                    {agentName(selectedAgent.agent)} ×
                  </button>
                ) : (
                  <span className="pill pill-neutral">
                    {value.testAttempts ? '全部请求' : '全部 Agent'}
                  </span>
                )}
              </div>
              <div className="card-body">
                {scope && scope.attempts > 0 ? (
                  <>
                    <div className="token-bar" aria-label="Token 占比条">
                      <div className="token-seg-in" style={{ width: `${usageMetrics.pUncached}%` }} title="常规输入" />
                      {usageMetrics.pCache > 0 && (
                        <div className="token-seg-cache" style={{ width: `${usageMetrics.pCache}%` }} title="缓存命中" />
                      )}
                      <div className="token-seg-out" style={{ width: `${usageMetrics.pOut}%` }} title="输出" />
                    </div>
                    <div className="kv-list">
                      <div className="kv-row">
                        <span className="kv-label">
                          <span className="swatch token-seg-in" />
                          输入（含缓存）
                        </span>
                        <span className="kv-val tabular">
                          <TokenFieldText row={scope} kind="input" />
                        </span>
                      </div>
                      <div className="kv-row">
                        <span className="kv-label">
                          <span className="swatch token-seg-cache" />
                          其中缓存命中
                        </span>
                        <span className="kv-val tabular">
                          <TokenFieldText row={scope} kind="cached" />
                          {usageMetrics.cacheRatio !== null && (
                            <span className="card-sub">({usageMetrics.cacheRatio}%)</span>
                          )}
                        </span>
                      </div>
                      <div className="kv-row">
                        <span className="kv-label">
                          <span className="swatch token-seg-out" />
                          输出
                        </span>
                        <span className="kv-val tabular">
                          <TokenFieldText row={scope} kind="output" />
                        </span>
                      </div>
                      <div className="kv-row">
                        <span className="kv-label">未回报用量次数</span>
                        <span
                          className="kv-val tabular"
                          style={{ color: scope.unknownUsage > 0 ? 'var(--text-secondary)' : 'var(--text-muted)' }}
                        >
                          {num(scope.unknownUsage)} 次
                        </span>
                      </div>
                    </div>
                  </>
                ) : (
                  <div className="empty-inline" style={{ padding: '18px 0' }}>
                    <span>此范围无模型请求</span>
                  </div>
                )}
              </div>
            </div>
          </section>

          <ModelUsageSection models={models} since={value.since} unknownUsage={value.unknownUsage} />
        </>
      ) : (
        !error && (
          <div className="card">
            <div className="empty-inline">{loading ? '正在读取统计…' : '暂无统计数据'}</div>
          </div>
        )
      )}
    </main>
  );
}
