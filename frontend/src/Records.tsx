import { ChevronDown, ChevronRight, RefreshCw, Search } from 'lucide-react';
import { useEffect, useState } from 'react';
import { agentName, formatTime, num, providerName, rpc, token, type Attempt, type RecordItem } from './rpc';
import { toast } from './toast';
import { ConfirmDialog, StatusNotice, useAction, useLoad } from './ui';

export type RecordFilter = 'all' | 'attention' | 'codex' | 'claude' | 'antigravity' | 'pi';

const states: Record<string, string> = {
  queued: '待处理',
  running: '命名中',
  candidate: '候选待领取',
  claimed: '待读回',
  applied: '已改名',
  kept: '保留原标题',
  stale: '已过期',
  failed: '生成失败',
  interrupted: '已中断',
  native: '已有原生标题',
  completed: '已完成',
  disabled: '插件已停用',
  invalid_response: '响应无效',
  rejected_tool: '工具调用已拒绝',
};

const syncStates: Record<string, { label: string; pill: string }> = {
  unbound: { label: 'Orca 未绑定', pill: 'pill-neutral' },
  unchanged: { label: 'Orca 已一致', pill: 'pill-neutral' },
  protected: { label: 'Orca 标题受保护', pill: 'pill-warning' },
  pending: { label: 'Orca 待同步', pill: 'pill-neutral' },
  writing: { label: 'Orca 同步中', pill: 'pill-accent' },
  synced: { label: 'Orca 已同步', pill: 'pill-success' },
  failed: { label: 'Orca 同步失败', pill: 'pill-danger' },
  stale: { label: 'Orca 同步已过期', pill: 'pill-neutral' },
  skipped: { label: 'Orca 已跳过', pill: 'pill-neutral' },
};

const failureMessages: Record<string, string> = {
  title_category: '标题开头缺少有效的分类标识及空格。',
  title_separator: '标题须用一个全角「｜」分隔对象和目标，两侧均须有内容且不留空格。',
  title_format: '标题包含不允许的字符或空白。',
  invalid_response: '命名服务返回的响应结构无效。',
  codex_login: 'Codex CLI 未登录 ChatGPT 订阅。',
  claude_login: '隔离模式下未检测到 Claude Code 的原生订阅登录。',
  model_selection: '请在模型设置中选择命名模型。',
  unauthorized: '命名服务拒绝了身份验证。',
  forbidden: '当前账号或凭据无权访问命名服务。',
  rate_limited: '命名请求被服务限流。',
  quota_exhausted: '命名服务的可用额度不足。',
  timeout: '命名请求超时。',
  canceled: '命名请求已取消。',
  request_failed: '命名服务请求失败。',
  tool_rejected: '命名模型尝试调用工具，结果已丢弃。',
  naming_failed: '自动命名失败，无法确定具体原因。',
  legacy_title_validation: '模型已返回，但标题未通过校验。具体规则未记录。',
  unrecorded: '具体失败原因未记录。',
};

function failureText(item: RecordItem): string | undefined {
  const failure = item.failure;
  if (!failure || (item.status !== 'failed' && item.status !== 'interrupted')) return undefined;
  if (failure.code === 'title_too_long' && failure.length !== undefined && failure.limit !== undefined) {
    return `标题超长（${failure.length} 个 Unicode 字符，当时上限 ${failure.limit} 个）。`;
  }
  return Object.hasOwn(failureMessages, failure.code) ? failureMessages[failure.code] : failureMessages.naming_failed;
}

function titlePillClass(status: string): string {
  if (status === 'applied') return 'pill-accent';
  if (status === 'failed' || status === 'interrupted' || status === 'invalid_response' || status === 'rejected_tool') {
    return 'pill-danger';
  }
  return 'pill-neutral';
}

function attemptSummary(item: RecordItem) {
  const last = item.attempts?.length ? item.attempts[item.attempts.length - 1] : undefined;
  if (!last) return { model: '', totalTokens: null as number | null };
  const model = last.actualModel || last.model || '';
  const hasKnown = typeof last.inputTokens === 'number' || typeof last.outputTokens === 'number';
  const totalTokens = hasKnown ? (last.inputTokens ?? 0) + (last.outputTokens ?? 0) : null;
  return { model, totalTokens };
}

export function Records({
  initialFilter = 'all',
  filterKey = 0,
  onChanged,
}: {
  initialFilter?: RecordFilter;
  filterKey?: number;
  onChanged: () => void;
}) {
  const [refresh, setRefresh] = useState(0);
  const [filter, setFilter] = useState<RecordFilter>(initialFilter);
  const [query, setQuery] = useState('');
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [confirmItem, setConfirmItem] = useState<RecordItem | null>(null);

  useEffect(() => {
    setFilter(initialFilter);
  }, [initialFilter, filterKey]);

  const { value, error, loading } = useLoad(
    () => rpc<{ items: RecordItem[]; testAttempts?: Attempt[] }>('records.list', { limit: 50 }),
    refresh,
  );
  const action = useAction();

  const items = value?.items ?? [];
  const attnCount = items.filter((r) => r.syncStatus === 'failed' || r.syncStatus === 'protected').length;
  const q = query.trim().toLowerCase();

  const filtered = items.filter((item) => {
    if (filter === 'attention' && item.syncStatus !== 'failed' && item.syncStatus !== 'protected') return false;
    if (
      (filter === 'codex' || filter === 'claude' || filter === 'antigravity' || filter === 'pi') &&
      item.agent !== filter
    ) {
      return false;
    }
    if (q) {
      const { model } = attemptSummary(item);
      const hay = `${item.title} ${model} ${agentName(item.agent)} ${item.sessionId} ${item.syncError ?? ''}`.toLowerCase();
      if (!hay.includes(q)) return false;
    }
    return true;
  });

  function sync(id: string, verb: 'adopt' | 'retry') {
    void action.run(async () => {
      const result = await rpc<{ accepted: boolean }>(`sync.${verb}`, { id });
      if (!result.accepted) throw new Error('记录状态已更新，请刷新后查看。');
      setConfirmItem(null);
      setRefresh((n) => n + 1);
      onChanged();
      return verb === 'adopt' ? '已采用新标题并同步至 Orca' : '已重新同步至 Orca';
    }, { success: 'toast', failure: confirmItem ? 'inline' : 'toast' });
  }

  return (
    <main className="main">
      <div className="filter-bar">
        <div className="segmented" role="group" aria-label="记录筛选">
          <button
            type="button"
            className={`seg-btn ${filter === 'all' ? 'active' : ''}`}
            onClick={() => setFilter('all')}
          >
            全部 ({items.length})
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'attention' ? 'active' : ''}`}
            onClick={() => setFilter('attention')}
          >
            待处理{attnCount > 0 ? ` (${attnCount})` : ''}
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'codex' ? 'active' : ''}`}
            onClick={() => setFilter('codex')}
          >
            Codex
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'claude' ? 'active' : ''}`}
            onClick={() => setFilter('claude')}
          >
            Claude
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'antigravity' ? 'active' : ''}`}
            onClick={() => setFilter('antigravity')}
          >
            Antigravity
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'pi' ? 'active' : ''}`}
            onClick={() => setFilter('pi')}
          >
            Pi
          </button>
        </div>

        <div className="view-actions">
          <div className="search-box">
            <span className="search-icon">
              <Search size={14} aria-hidden="true" />
            </span>
            <input
              type="text"
              className="input"
              placeholder="搜索会话标题或模型…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <button
            type="button"
            className="icon-btn bordered"
            title="刷新记录"
            aria-label="刷新记录"
            onClick={() => {
              setRefresh((n) => n + 1);
              onChanged();
              toast.success('处理记录已刷新');
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

      <div className="section-stack">
        <section className="card">
          {filtered.length > 0 ? (
            <div className="record-list">
              {filtered.map((item) => {
                const expanded = expandedId === item.id;
                const { model, totalTokens } = attemptSummary(item);
                const reason = failureText(item);
                const syncError = item.syncError?.trim() || undefined;
                const syncMeta = item.syncStatus ? syncStates[item.syncStatus] : undefined;
                const protectedWarn = item.syncStatus === 'protected';
                const reasons: { text: string; warn?: boolean }[] = [];
                if (reason && syncError && reason !== syncError) {
                  reasons.push({ text: `生成失败：${reason}` });
                  reasons.push({ text: `同步错误：${syncError}` });
                } else if (syncError) {
                  reasons.push({ text: syncError });
                } else if (reason) {
                  reasons.push({ text: reason });
                } else if (protectedWarn) {
                  reasons.push({ text: 'Orca 中存在手动编辑的标题，已自动保护未覆盖。', warn: true });
                }

                return (
                  <article className="record-item" key={item.id}>
                    <div className="record-main">
                      <div className="record-left">
                        <div className="record-title-line">
                          <span className="record-title">{item.title || '会话标题尚未生成'}</span>
                        </div>
                        <div className="record-meta tabular">
                          <span style={{ fontWeight: 600, color: 'var(--text-secondary)' }}>
                            {agentName(item.agent)}
                          </span>
                          <span>·</span>
                          <span>{formatTime(item.updatedAt)}</span>
                          {model && (
                            <>
                              <span>·</span>
                              <span className="mono">{model}</span>
                            </>
                          )}
                          {totalTokens !== null && (
                            <>
                              <span>·</span>
                              <span>{num(totalTokens)} Token</span>
                            </>
                          )}
                        </div>
                        {reasons.length > 0 && (
                          <div className="record-reasons">
                            {reasons.map((entry, index) => (
                              <div key={index} className={`record-reason ${entry.warn ? 'warn' : ''}`}>
                                {entry.text}
                              </div>
                            ))}
                          </div>
                        )}
                      </div>

                      <div className="record-right">
                        <span className={`pill ${titlePillClass(item.status)}`}>
                          {states[item.status] ?? item.status}
                        </span>
                        {item.syncStatus && (
                          <span className={`pill ${syncMeta?.pill ?? 'pill-neutral'}`}>
                            {syncMeta?.label ?? item.syncStatus}
                          </span>
                        )}
                        {item.syncStatus === 'failed' && (
                          <button
                            type="button"
                            className="btn btn-sm btn-primary"
                            disabled={action.busy}
                            onClick={() => sync(item.id, 'retry')}
                          >
                            重试同步
                          </button>
                        )}
                        {item.syncStatus === 'protected' && (
                          <button
                            type="button"
                            className="btn btn-sm"
                            disabled={action.busy}
                            onClick={() => {
                              action.reset();
                              setConfirmItem(item);
                            }}
                          >
                            采用新标题
                          </button>
                        )}
                        {!!item.attempts?.length && (
                          <button
                            type="button"
                            className="icon-btn"
                            style={{ width: 28, height: 28 }}
                            aria-expanded={expanded}
                            title="查看调用详情"
                            aria-label="查看调用详情"
                            onClick={() => setExpandedId((cur) => (cur === item.id ? null : item.id))}
                          >
                            {expanded ? (
                              <ChevronDown size={14} aria-hidden="true" />
                            ) : (
                              <ChevronRight size={14} aria-hidden="true" />
                            )}
                          </button>
                        )}
                      </div>
                    </div>

                    {expanded && !!item.attempts?.length && (
                      <div className="record-detail tabular">
                        {item.attempts.map((attempt, index) => (
                          <AttemptRow key={index} attempt={attempt} />
                        ))}
                      </div>
                    )}
                  </article>
                );
              })}
            </div>
          ) : (
            <div className="empty-inline">
              <span>
                {loading
                  ? '正在读取处理记录…'
                  : items.length === 0
                    ? '还没有处理记录'
                    : '当前筛选条件下没有处理记录。'}
              </span>
              {(filter !== 'all' || query) && (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => {
                    setFilter('all');
                    setQuery('');
                  }}
                >
                  显示全部
                </button>
              )}
            </div>
          )}
        </section>

        {!!value?.testAttempts?.length && (
          <section className="card">
            <div className="card-header">
              <h2 className="card-title">连接测试记录</h2>
              <span className="card-sub tabular">{value.testAttempts.length} 次</span>
            </div>
            <div className="record-detail tabular" style={{ borderTop: 0, background: 'transparent' }}>
              {value.testAttempts.map((attempt, index) => (
                <AttemptRow key={index} attempt={attempt} />
              ))}
            </div>
          </section>
        )}
      </div>

      {confirmItem && (
        <ConfirmDialog
          title="覆盖 Orca 中的手动标题？"
          confirmLabel="确认覆盖"
          busy={action.busy}
          error={action.error}
          onCancel={() => setConfirmItem(null)}
          onConfirm={() => sync(confirmItem.id, 'adopt')}
        >
          <p>
            将 {agentName(confirmItem.agent)} 会话 <code>{confirmItem.sessionId}</code> 对应的 Orca 标题更新为「
            {confirmItem.title}」。
          </p>
        </ConfirmDialog>
      )}
    </main>
  );
}

function AttemptRow({ attempt }: { attempt: Attempt }) {
  const actualTier = attempt.actualTier && attempt.actualTier !== 'unknown' ? attempt.actualTier : '未回报';
  const modelLabel = attempt.actualModel || attempt.model || '未知模型';

  return (
    <div className="record-attempt-row">
      <span className="detail-kv">
        <span className="detail-k">来源</span>
        <strong>{providerName(attempt.provider)}</strong>
      </span>
      <span className="detail-kv">
        <span className="detail-k">模型</span>
        <code className="mono">{modelLabel}</code>
        {attempt.provider === 'claude' && attempt.actualModel && attempt.model && attempt.actualModel !== attempt.model && (
          <span className="card-sub">（请求 {attempt.model}）</span>
        )}
      </span>
      <span className="detail-kv">
        <span className="detail-k">状态</span>
        <span>{states[attempt.status] ?? attempt.status}</span>
      </span>
      <span className="detail-kv">
        <span className="detail-k">档位</span>
        <span>
          请求 {attempt.requestedTier || '默认'} → 实际 {actualTier}
        </span>
      </span>
      <span className="detail-kv">
        <span className="detail-k">Token</span>
        <span>
          输入 {token(attempt.inputTokens)} · 输出 {token(attempt.outputTokens)} · 缓存{' '}
          {token(attempt.cachedTokens)}
        </span>
      </span>
      {attempt.startedAt && (
        <span className="detail-kv">
          <span className="detail-k">时间</span>
          <span>{formatTime(attempt.startedAt)}</span>
        </span>
      )}
    </div>
  );
}
