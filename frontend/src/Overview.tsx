import { RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { agentName, formatTime, localDate, num, providerName, rpc, token, type Counts, type Daily, type ModelUsage, type UsageSummary } from './rpc';
import { Empty, Section, Status, useLoad } from './ui';

const metrics = [['renames', '标题已应用'], ['attempts', '模型请求'], ['kept', '保留原标题'], ['unknownUsage', '用量未回报']] as const;

export function Overview({ revision }: { revision: number }) {
  const [date, setDate] = useState(localDate);
  const [refresh, setRefresh] = useState(0);
  const [agent, setAgent] = useState('');
  const { value, error } = useLoad(() => rpc<Daily>('stats.daily', { date }), `${date}:${refresh}:${revision}`);
  const rows = value?.byAgent ?? [];
  const selected = rows.find((row) => row.agent === agent);
  return <>
    <header className="page-head">
      <h1>概览</h1>
      <div className="actions">
        <input aria-label="统计日期" type="date" max={localDate()} value={date} onChange={(event) => event.target.value && setDate(event.target.value)} />
        <button type="button" className="icon-button" aria-label="刷新统计" title="刷新统计" onClick={() => setRefresh(refresh + 1)}><RefreshCw size={16} /></button>
      </div>
    </header>
    <Status error={error} />
    <dl className="metrics" aria-label="当日标题统计">
      {metrics.map(([key, label]) => <div key={key}>
        <dt>{label}</dt><dd>{value ? num(value[key]) : '—'}
          {key === 'attempts' && !!value?.testAttempts && <small>含 {num(value.testAttempts)} 次测试</small>}
        </dd>
      </div>)}
    </dl>
    <div className="overview-grid">
      <Section title="Agent 活动">
        {rows.length ? <Activity rows={rows} /> : <Empty>{value ? '当天没有 Agent 活动' : error ? '无法载入活动' : '正在读取统计…'}</Empty>}
      </Section>
      <Section title="已知 Token" action={!!rows.length && <select className="agent-filter" aria-label="Token 用量的 Agent" value={selected?.agent ?? ''} onChange={(event) => setAgent(event.target.value)}>
        <option value="">全部 Agent</option>
        {rows.map((row) => <option key={row.agent} value={row.agent}>{agentName(row.agent)}</option>)}
      </select>}>
        {value ? <TokenRows value={selected ?? value} scoped={!!selected} /> : <Empty>{error ? '无法载入用量' : '正在读取用量…'}</Empty>}
      </Section>
    </div>
    <Section title="模型调用">
      {value?.byModel?.length ? <ModelTable rows={value.byModel} /> : <Empty>{value ? '当天没有模型活动' : error ? '无法载入模型活动' : '正在读取模型活动…'}</Empty>}
    </Section>
    <footer className="overview-foot">
      {value?.since && <span>统计自 {formatTime(value.since)} 开始</span>}
    </footer>
  </>;
}

function Activity({ rows }: { rows: (Counts & { agent: string })[] }) {
  const max = Math.max(1, ...rows.map((row) => row.renames));
  return <div className="table-wrap"><table>
    <thead><tr><th>Agent</th><th className="number">已应用</th><th className="number">模型请求</th><th className="number">用量未回报</th></tr></thead>
    <tbody>{rows.map((row) => <tr key={row.agent}>
      <th scope="row">{agentName(row.agent)}</th>
      <td className="number"><span className="value-track" style={{ '--ratio': `${row.renames / max * 100}%` } as React.CSSProperties}>{num(row.renames)}</span></td>
      <td className="number">{num(row.attempts)}</td>
      <td className="number">{num(row.unknownUsage)}</td>
    </tr>)}</tbody>
  </table></div>;
}

function TokenRows({ value, scoped }: { value: Counts; scoped: boolean }) {
  return <dl className="token-rows">
    <div><dt>输入</dt><dd><TokenTotal row={value} kind="input" /></dd></div>
    <div><dt>输出</dt><dd><TokenTotal row={value} kind="output" /></dd></div>
    <div><dt>缓存</dt><dd><TokenTotal row={value} kind="cached" /></dd></div>
    {scoped && <div><dt>用量未回报</dt><dd>{num(value.unknownUsage)} 次</dd></div>}
  </dl>;
}

function ModelTable({ rows }: { rows: ModelUsage[] }) {
  return <div className="table-wrap" tabIndex={0} role="region" aria-label="当日模型调用">
    <table>
      <thead><tr>
        <th>模型</th><th className="number">调用</th><th className="number">已知输入</th>
        <th className="number">已知输出</th><th className="number">已知缓存</th><th className="number">用量未回报</th>
      </tr></thead>
      <tbody>{rows.map((row) => <tr key={`${row.provider}:${row.model}`}>
        <th scope="row">{row.model || '未知模型'}<span className="model-source">{providerName(row.provider)}</span></th>
        <td className="number">{num(row.attempts)}{!!row.testAttempts && <span className="model-source">含 {num(row.testAttempts)} 次测试</span>}</td>
        <td className="number"><TokenTotal row={row} kind="input" /></td>
        <td className="number"><TokenTotal row={row} kind="output" /></td>
        <td className="number"><TokenTotal row={row} kind="cached" /></td>
        <td className="number">{num(row.unknownUsage)}</td>
      </tr>)}</tbody>
    </table>
  </div>;
}

function TokenTotal({ row, kind }: { row: UsageSummary; kind: 'input' | 'output' | 'cached' }) {
  const value = row[`${kind}Tokens`];
  const known = row[`${kind}Known`];
  if (typeof value !== 'number' || known === 0) return <span aria-label="未回报" title="未回报">—</span>;
  return <>{token(value)}{typeof known === 'number' && known < row.attempts &&
    <span className="muted" title={`已回报 ${known} / ${row.attempts} 次`}>（部分）</span>}</>;
}
