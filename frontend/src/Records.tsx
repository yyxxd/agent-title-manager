import { ChevronDown, ChevronRight, RefreshCw, Search } from 'lucide-react';
import { useRef, useState } from 'react';
import {
  agentName, canAdoptTitle, formatTime, needsAttention, num, recordCounts, rpc,
  type RecordCounts, type RecordCursor, type RecordItem, type RecordList,
} from './rpc';
import { AttemptRow } from './RecordAttempt';
import { toast } from './toast';
import { ConfirmDialog, StatusNotice, useAction, useLoad } from './ui';

export type RecordFilter = 'all' | 'attention' | 'codex' | 'claude' | 'antigravity' | 'pi';

const states: Record<string, string> = {
  queued: '待处理',
  running: '命名中',
  candidate: '候选待领取',
  claimed: '待读回',
  applied: '已改名',
  kept: '已跳过改名',
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

const decisionMessages: Record<string, string> = {
  manual_locked: '手动重命名锁定',
  unknown_protected: '已有标题来源未确认，已保留。',
  model_keep: '模型选择保留原标题，具体依据未记录。',
  model_keep_goal: '模型判断本轮延续原标题的目标。',
  model_keep_progress: '模型判断本轮是已有工作的步骤或进展。',
  model_keep_unspecified: '模型未提供可确认的更具体依据。',
  empty_title: '模型未生成标题。',
  confirmation_only: '本轮仅确认，沿用现有标题。',
  throttled: '尚未达到重新评估的轮数或时间间隔。',
  duplicate_turn: '本轮内容与上一轮相同。',
  no_content: '本轮没有可用于命名的内容。',
  title_collision: '候选标题与同项目中的会话重名。',
  same_title: '模型给出的标题与原标题相同。',
};

const modelKeepReasons = new Set(['model_keep', 'model_keep_goal', 'model_keep_progress', 'model_keep_unspecified']);

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
  const attempts = item.attempts ?? [];
  const last = attempts.at(-1);
  const model = last?.actualModel || last?.model || '';
  let known = 0;
  let total = 0;
  for (const attempt of attempts) {
    for (const value of [attempt.inputTokens, attempt.outputTokens]) {
      if (typeof value === 'number') {
        known++;
        total += value;
      }
    }
  }
  return { model, count: attempts.length, totalTokens: known ? total : null, partial: known < attempts.length * 2 };
}

export function Records({
  initialFilter = 'all',
  onOpened,
  onChanged,
}: {
  initialFilter?: RecordFilter;
  onOpened: (unread: number) => void;
  onChanged: () => void;
}) {
  const [refresh, setRefresh] = useState(0);
  const [filter, setFilter] = useState<RecordFilter>(initialFilter);
  const [query, setQuery] = useState('');
  const [cursors, setCursors] = useState<RecordCursor[]>([]);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [confirmItem, setConfirmItem] = useState<RecordItem | null>(null);

  const visit = useRef<Promise<RecordList> | null>(null);
  const before = cursors.at(-1);
  const { value, error, loading } = useLoad(
    async () => {
      const params = { limit: 50, filter, query: query.trim(), before };
      if (!visit.current) {
        visit.current = rpc<RecordList & { counts: RecordCounts }>('records.open', params).then((result) => {
          onOpened(result.counts.unread);
          return result;
        }).catch((error) => {
          visit.current = null;
          throw error;
        });
        return visit.current;
      }
      await visit.current;
      return rpc<RecordList>('records.list', params);
    },
    `${refresh}:${filter}:${before?.updated}:${before?.rowId}:${query.trim()}`,
  );
  const action = useAction();

  const items = value?.items ?? [];
  const counts = value?.counts ?? recordCounts(items);
  const q = query.trim().toLowerCase();

  const filtered = value?.counts ? items : items.filter((item) => {
    if (filter === 'attention' && !needsAttention(item)) return false;
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

  function selectFilter(next: RecordFilter) {
    setFilter(next);
    setCursors([]);
  }

  function sync(id: string, verb: 'adopt' | 'retry') {
    void action.run(async () => {
      const result = await rpc<{ accepted: boolean }>(`sync.${verb}`, { id });
      if (!result.accepted) throw new Error('记录状态已更新，请刷新后查看。');
      setConfirmItem(null);
      setCursors([]);
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
            onClick={() => selectFilter('all')}
          >
            全部 ({counts.total})
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'attention' ? 'active' : ''}`}
            onClick={() => selectFilter('attention')}
          >
            提醒记录
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'codex' ? 'active' : ''}`}
            onClick={() => selectFilter('codex')}
          >
            Codex
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'claude' ? 'active' : ''}`}
            onClick={() => selectFilter('claude')}
          >
            Claude
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'antigravity' ? 'active' : ''}`}
            onClick={() => selectFilter('antigravity')}
          >
            Antigravity
          </button>
          <button
            type="button"
            className={`seg-btn ${filter === 'pi' ? 'active' : ''}`}
            onClick={() => selectFilter('pi')}
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
              maxLength={500}
              onChange={(e) => {
                setQuery(e.target.value);
                setCursors([]);
              }}
            />
          </div>
          <button
            type="button"
            className="icon-btn bordered"
            title="刷新记录"
            aria-label="刷新记录"
            onClick={() => {
              setRefresh((n) => n + 1);
              setCursors([]);
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
                const { model, count, totalTokens, partial } = attemptSummary(item);
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
                }
                if (protectedWarn) reasons.push({
                  text: item.orcaTitleOrigin === 'manual'
                    ? 'Orca 手动重命名锁定'
                    : 'Orca 现有标题的来源未确认，已保留。',
                  warn: true,
                });
                if (item.reason && Object.hasOwn(decisionMessages, item.reason)) {
                  reasons.push({
                    text: decisionMessages[item.reason],
                    warn: item.reason === 'manual_locked' || item.reason === 'unknown_protected',
                  });
                } else if (item.status === 'kept') {
                  reasons.push({ text: '历史原因未记录。' });
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
                          <span>· {count ? `${num(count)} 次调用` : '未调用模型'}</span>
                          {count > 0 && (
                            <span>· {totalTokens === null
                              ? 'Token 未回报'
                              : partial
                                ? `已知 ${num(totalTokens)} Token（部分回报）`
                                : `${num(totalTokens)} Token`}
                            </span>
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
                          {item.reason && modelKeepReasons.has(item.reason) ? '保留原标题' : states[item.status] ?? item.status}
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
                        {canAdoptTitle(item) && (
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
                          <AttemptRow key={index} attempt={attempt} status={states[attempt.status] ?? attempt.status} />
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
                    : counts.total === 0 && !query
                    ? '还没有处理记录'
                    : '当前筛选条件下没有处理记录。'}
              </span>
              {(filter !== 'all' || query) && (
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  onClick={() => {
                    selectFilter('all');
                    setQuery('');
                  }}
                >
                  显示全部
                </button>
              )}
            </div>
          )}
          {(before || value?.next) && (
            <div className="card-header">
              <span className="card-sub">第 {cursors.length + 1} 页</span>
              <div className="view-actions">
                <button type="button" className="btn btn-sm" disabled={!before || loading}
                  onClick={() => setCursors((old) => old.slice(0, -1))}>上一页</button>
                <button type="button" className="btn btn-sm" disabled={!value?.next || loading}
                  onClick={() => {
                    const next = value?.next;
                    if (next) setCursors((old) => old.at(-1) === next ? old : [...old, next]);
                  }}>下一页</button>
              </div>
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
                <AttemptRow key={index} attempt={attempt} status={states[attempt.status] ?? attempt.status} />
              ))}
            </div>
          </section>
        )}
      </div>

      {confirmItem && (
        <ConfirmDialog
          title="覆盖 Orca 中受保护的标题？"
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
