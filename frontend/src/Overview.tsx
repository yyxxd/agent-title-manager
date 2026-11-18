import { RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { agentName, formatTime, localDate, num, providerName, rpc, type Counts, type Daily, type ModelUsage, type UsageSummary } from './rpc';
import { Empty, Section, Status, useLoad } from './ui';
import './overview.css';
import { DateFilter } from './DateFilter';
import { datePresets, type DateRange } from './dates';

const tokenFields = [['input', '输入'], ['output', '输出'], ['cached', '缓存（计入输入）']] as const;
type TokenKind = typeof tokenFields[number][0];
type AgentUsage = Counts & { agent: string };

const usageAgentName = (agent: string) => agent === 'test' ? '连接测试' : agentName(agent);
const missingHint = '至少一项 Token 用量未记录的请求次数';

export function Overview({ revision }: { revision: number }) {
  const presets = datePresets(localDate());
  const [selection, setSelection] = useState<{ range: DateRange; preset?: string }>(() => {
    const today = presets[0];
    return { range: today.range, preset: today.label };
  });
  const { preset } = selection;
  const range = presets.find(({ label }) => label === preset)?.range ?? selection.range;
  const [refresh, setRefresh] = useState(0);
  const [agent, setAgent] = useState('');
  const { value, error } = useLoad(() => rpc<Daily>('stats.range', range), `${range.start}:${range.end}:${refresh}:${revision}`);
  const rows = value?.byAgent ?? [];
  const agents = rows.filter((row) => row.agent !== 'test');
  const selected = rows.find((row) => row.agent === agent);
  const tokenScope = selected ?? value;
  return <div className="daily-overview" data-refreshed={refresh || undefined}>
    <header className="page-head">
      <h1>概览</h1>
      <div className="actions">
        <DateFilter range={range} preset={preset} onChange={(next, label) => setSelection({ range: next, preset: label })} />
        <button type="button" className="icon-button" aria-label="刷新统计" title="刷新统计" onClick={() => setRefresh((previous) => previous + 1)}><RefreshCw size={16} /></button>
      </div>
    </header>
    <Status error={error} />
    {value ? <>
      <DailySummary value={value} agents={agents} />
      <div className="daily-overview-detail">
        <Section title="Agent 贡献">
          {agents.length ? <Activity key={refresh} rows={agents} /> : <Empty>所选日期无 Agent 活动</Empty>}
        </Section>
        <Section title="Token 明细" action={rows.length > 0 && <select aria-label="Token 明细的 Agent" value={selected?.agent ?? ''} onChange={(event) => setAgent(event.target.value)}>
          <option value="">{value?.testAttempts ? '全部请求' : '全部 Agent'}</option>
          {rows.map((row) => <option key={row.agent} value={row.agent}>{usageAgentName(row.agent)}</option>)}
        </select>}>
          {tokenScope && tokenScope.attempts > 0
            ? <TokenRows value={tokenScope} scoped={!!selected} />
            : <Empty>此范围无模型请求</Empty>}
        </Section>
      </div>
      <Section title="模型明细" action={!!value?.testAttempts && <span className="daily-overview-note">全部请求</span>}>
        {value?.byModel?.length ? <ModelTable rows={value.byModel} /> : <Empty>所选日期无模型调用明细</Empty>}
      </Section>
      {value.since && <footer className="daily-overview-note">统计自 {formatTime(value.since)} 起</footer>}
    </> : !error && <div className="daily-overview-frame" role="status"><Empty>正在读取统计…</Empty></div>}
  </div>;
}

function DailySummary({ value, agents }: { value: Daily; agents: AgentUsage[] }) {
  const attempts = value.attempts - (value.testAttempts ?? 0);
  const usageRows = (value.testAttempts ? agents : [value]).filter((row) => row.attempts > 0);
  const usage = usageRows.flatMap((row) => [tokenField(row, 'input'), tokenField(row, 'output')]);
  const known = usage.filter((field) => field.value !== null);
  const total = known.reduce((sum, field) => sum + (field.value ?? 0), 0);
  const partial = usageRows.reduce((sum, row) => sum + row.attempts, 0) !== attempts
    || usage.some(({ value, known, complete }) => value === null || (known !== undefined && !complete));
  const coverage = partial ? ' · 部分回报' : usage.some((field) => !field.complete) ? ' · 覆盖未知' : '';
  const detail = !attempts ? '无命名请求' : !known.length ? '用量未知' : `输入 + 输出${coverage}`;

  return <section className="daily-overview-frame" aria-label="所选日期汇总">
    <dl className="daily-overview-lead">
      <div><dt>已改名</dt><dd>{num(value.renames)} <small>次</small></dd></div>
      <div>
        <dt>命名用量</dt>
        <dd>{!attempts || known.length ? <>{num(total)} <small>Token</small></> : <span className="daily-overview-note">—</span>}</dd>
        <dd className="daily-overview-note">{detail}</dd>
      </div>
    </dl>
    <dl className="daily-overview-secondary">
      <div><dt>保留原标题</dt><dd>{num(value.kept)} 次</dd></div>
      <div><dt>模型请求</dt><dd>{num(value.attempts)} 次{!!value.testAttempts && <small>含 {num(value.testAttempts)} 次测试</small>}</dd></div>
      <div><dt title={missingHint}>用量缺报</dt><dd>{num(value.unknownUsage)} 次</dd></div>
    </dl>
  </section>;
}

function Activity({ rows }: { rows: AgentUsage[] }) {
  const ranked = [...rows].sort((a, b) => b.renames - a.renames || b.attempts - a.attempts || a.agent.localeCompare(b.agent));
  const max = ranked[0].renames;
  return <table className="daily-overview-agents">
    <thead><tr><th scope="col">Agent</th><th scope="col" colSpan={2} className="number">改名</th><th scope="col" className="number">请求</th><th scope="col" className="number" title={missingHint}>缺报</th></tr></thead>
    <tbody>{ranked.map((row) => <tr key={row.agent}>
      <th scope="row">{agentName(row.agent)}</th>
      <td className="daily-overview-bar-cell">
        {max > 0 && <div className="daily-overview-bar" aria-hidden="true"><span style={{ width: `${row.renames / max * 100}%` }} /></div>}
      </td>
      <td className="number daily-overview-count">{num(row.renames)}</td>
      <td className="number">{num(row.attempts)}</td>
      <td className="number">{num(row.unknownUsage)}</td>
    </tr>)}</tbody>
  </table>;
}

function TokenRows({ value, scoped }: { value: UsageSummary; scoped: boolean }) {
  return <dl className="daily-overview-token-rows">
    {tokenFields.map(([kind, label]) => <div key={kind}><dt>{label}</dt><dd><TokenValue row={value} kind={kind} /></dd></div>)}
    {scoped && <div><dt title={missingHint}>用量缺报</dt><dd>{num(value.unknownUsage)} 次</dd></div>}
  </dl>;
}

function ModelTable({ rows }: { rows: ModelUsage[] }) {
  return <table className="daily-overview-models" role="table" aria-label="模型调用与用量">
    <thead role="rowgroup"><tr role="row">
      <th scope="col" role="columnheader">模型</th><th scope="col" role="columnheader" className="number">调用</th>
      <th scope="col" role="columnheader" className="number">输入</th><th scope="col" role="columnheader" className="number">输出</th>
      <th scope="col" role="columnheader" className="number">缓存</th><th scope="col" role="columnheader" className="number" title={missingHint}>用量缺报</th>
    </tr></thead>
    <tbody role="rowgroup">{rows.map((row) => <tr role="row" key={JSON.stringify([row.provider, row.model])}>
      <th scope="row" role="rowheader">{row.model || '未知模型'}<span className="daily-overview-note">{row.provider ? providerName(row.provider) : '未知提供方'}</span></th>
      <td role="cell" className="number"><span className="daily-overview-cell-label">调用</span>{num(row.attempts)}
        {!!row.testAttempts && <span className="daily-overview-note">含 {num(row.testAttempts)} 次测试</span>}
      </td>
      {tokenFields.map(([kind, label]) => <td role="cell" className="number" key={kind}>
        <span className="daily-overview-cell-label">{kind === 'cached' ? '缓存' : label}</span><TokenValue row={row} kind={kind} />
      </td>)}
      <td role="cell" className="number"><span className="daily-overview-cell-label">用量缺报</span>{num(row.unknownUsage)}</td>
    </tr>)}</tbody>
  </table>;
}

function tokenField(row: UsageSummary, kind: TokenKind) {
  const tokens = row[`${kind}Tokens`];
  const known = row[`${kind}Known`];
  const value = typeof tokens === 'number' && known !== 0 ? tokens : null;
  return { value, known, complete: value !== null && (known === row.attempts || (known === undefined && row.unknownUsage === 0)) };
}

function TokenValue({ row, kind }: { row: UsageSummary; kind: TokenKind }) {
  const { value, known, complete } = tokenField(row, kind);
  if (value === null) return <span className="daily-overview-note">未回报</span>;
  return <>{num(value)}{!complete && <span className="daily-overview-note" title={known === undefined ? undefined : `已回报 ${num(known)} / ${num(row.attempts)} 次`}>{known === undefined ? '（覆盖未知）' : '（部分）'}</span>}</>;
}
