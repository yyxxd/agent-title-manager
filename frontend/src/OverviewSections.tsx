import {
  formatTime,
  num,
  providerName,
  token,
  type Counts,
  type Daily,
  type ModelUsage,
  type UsageSummary,
} from './rpc';

export type TokenKind = 'input' | 'output' | 'cached';
export type AgentUsage = Counts & { agent: string };

export function tokenField(row: UsageSummary, kind: TokenKind) {
  const tokens = row[`${kind}Tokens`];
  const known = row[`${kind}Known`];
  const value = typeof tokens === 'number' && known !== 0 ? tokens : null;
  const complete = value !== null && (known === row.attempts || (known === undefined && row.unknownUsage === 0));
  return { value, known, complete };
}

export function computeScopeTokens(scope: UsageSummary | undefined, agents: AgentUsage[], daily?: Daily) {
  if (!scope) {
    return { pUncached: 0, pCache: 0, pOut: 0, cacheRatio: null as number | null };
  }
  const inVal = tokenField(scope, 'input').value ?? 0;
  const outVal = tokenField(scope, 'output').value ?? 0;
  const cacheVal = tokenField(scope, 'cached').value ?? 0;
  const sum = inVal + outVal;
  if (sum <= 0 && (!daily || agents.length === 0)) {
    return { pUncached: 0, pCache: 0, pOut: 0, cacheRatio: null as number | null };
  }
  const safeSum = sum > 0 ? sum : 1;
  const uncachedIn = Math.max(0, inVal - cacheVal);
  const pUncached = Math.round((uncachedIn / safeSum) * 100);
  const pCache = Math.round((Math.min(cacheVal, inVal) / safeSum) * 100);
  const pOut = Math.max(0, 100 - pUncached - pCache);
  const cacheRatio = inVal > 0 ? Math.round((cacheVal / inVal) * 100) : null;
  return { pUncached, pCache, pOut, cacheRatio };
}

export function TokenFieldText({
  row,
  kind,
  mutedWhenMissing,
}: {
  row: UsageSummary;
  kind: TokenKind;
  mutedWhenMissing?: boolean;
}) {
  const { value, known, complete } = tokenField(row, kind);
  if (value === null) {
    return <span style={{ color: mutedWhenMissing ? 'var(--text-muted)' : 'var(--text-secondary)' }}>未回报</span>;
  }
  return (
    <>
      {num(value)}
      {!complete && (
        <span
          className="partial-tag"
          title={known === undefined ? undefined : `已回报 ${num(known)} / ${num(row.attempts)} 次`}
        >
          {known === undefined ? '未知覆盖' : '部分'}
        </span>
      )}
    </>
  );
}

export function KpiGrid({
  value,
  agents,
  enabledCount,
  totalCount,
  enabledNames,
  onOpenAgents,
}: {
  value: Daily;
  agents: AgentUsage[];
  enabledCount: number;
  totalCount: number;
  enabledNames: string[];
  onOpenAgents: () => void;
}) {
  const namingAttempts = value.attempts - (value.testAttempts ?? 0);
  const usageRows = (value.testAttempts ? agents : [value]).filter((row) => row.attempts > 0);
  const fields = usageRows.flatMap((row) => [tokenField(row, 'input'), tokenField(row, 'output')]);
  const known = fields.filter((f) => f.value !== null);
  const totalTok = known.reduce((sum, f) => sum + (f.value ?? 0), 0);
  const partial =
    usageRows.reduce((sum, row) => sum + row.attempts, 0) !== namingAttempts ||
    fields.some((f) => f.value === null || (f.known !== undefined && !f.complete));
  const inputVal = tokenField(value, 'input').value;
  const outputVal = tokenField(value, 'output').value;
  const cacheVal = tokenField(value, 'cached').value;

  return (
    <section className="kpi-grid" aria-label="核心统计">
      <div className="card kpi-card">
        <div className="kpi-label">
          <span>已改名</span>
        </div>
        <div className="kpi-value accent tabular">
          {num(value.renames)}
          <span className="kpi-unit">次</span>
        </div>
        <div className="kpi-foot tabular">保留原标题 {num(value.kept)} 次</div>
      </div>

      <div className="card kpi-card">
        <div className="kpi-label">
          <span>命名用量</span>
          {partial && known.length > 0 && (
            <span className="pill pill-neutral" title={`有 ${value.unknownUsage} 次调用未回报完整 Token`}>
              部分回报
            </span>
          )}
        </div>
        <div className="kpi-value tabular">
          {!namingAttempts || known.length > 0 ? (
            <>
              {num(totalTok)}
              <span className="kpi-unit">Token</span>
            </>
          ) : (
            <span className="kpi-unit" style={{ fontSize: 18 }}>
              用量未知
            </span>
          )}
        </div>
        <div className="kpi-foot tabular">
          输入 {token(inputVal)} · 输出 {token(outputVal)}
        </div>
      </div>

      <div className="card kpi-card">
        <div className="kpi-label">
          <span>模型请求</span>
        </div>
        <div className="kpi-value tabular">
          {num(value.attempts)}
          <span className="kpi-unit">次</span>
        </div>
        <div className="kpi-foot tabular">
          缓存命中 {token(cacheVal)} Token
          {value.testAttempts ? `（含 ${num(value.testAttempts)} 次测试）` : ''}
        </div>
      </div>

      <div
        className="card kpi-card clickable"
        role="button"
        tabIndex={0}
        title="管理已启用的 Agent"
        onClick={onOpenAgents}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onOpenAgents();
          }
        }}
      >
        <div className="kpi-label">
          <span>启用 Agent</span>
          <span style={{ color: 'var(--accent)', fontSize: 12 }}>管理 →</span>
        </div>
        <div className="kpi-value tabular">
          {enabledCount}
          <span className="kpi-unit">/ {totalCount}</span>
        </div>
        <div className="kpi-foot">{enabledNames.length ? enabledNames.join(' · ') : '当前均已停用'}</div>
      </div>
    </section>
  );
}

export function ModelUsageSection({
  models,
  since,
  unknownUsage,
}: {
  models: ModelUsage[];
  since?: string;
  unknownUsage: number;
}) {
  return (
    <section className="card">
      <div className="card-header">
        <h2 className="card-title">模型明细</h2>
        <span className="card-sub tabular">共 {models.length} 个模型</span>
      </div>
      {models.length > 0 ? (
        <div style={{ overflowX: 'auto' }}>
          <table className="data-table">
            <thead>
              <tr>
                <th>模型</th>
                <th className="num">调用</th>
                <th className="num">输入</th>
                <th className="num">输出</th>
                <th className="num">缓存</th>
                <th className="num">未回报</th>
              </tr>
            </thead>
            <tbody>
              {models.map((m) => (
                <tr key={`${m.provider}:${m.model}`}>
                  <td>
                    <div className="model-cell">
                      <span className="model-id mono">{m.model || '未知模型'}</span>
                      <span className="model-source">{m.provider ? providerName(m.provider) : '未知提供方'}</span>
                    </div>
                  </td>
                  <td className="num tabular">
                    {num(m.attempts)}
                    {!!m.testAttempts && <span className="partial-tag">含 {num(m.testAttempts)} 测试</span>}
                  </td>
                  <td className="num tabular">
                    <TokenFieldText row={m} kind="input" />
                  </td>
                  <td className="num tabular">
                    <TokenFieldText row={m} kind="output" />
                  </td>
                  <td className="num tabular">
                    <TokenFieldText row={m} kind="cached" mutedWhenMissing />
                  </td>
                  <td
                    className="num tabular"
                    style={{ color: m.unknownUsage > 0 ? 'var(--warning)' : 'var(--text-muted)' }}
                  >
                    {num(m.unknownUsage)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="empty-inline">
          <span>所选日期范围无模型调用明细</span>
        </div>
      )}
      <div className="overview-footnote">
        <span>
          Token 合计 = 输入 + 输出（缓存计入输入）
          {since ? ` · 统计自 ${formatTime(since)} 起` : ''}
        </span>
        <span className="tabular">所选范围共 {num(unknownUsage)} 次调用未回报用量</span>
      </div>
    </section>
  );
}
