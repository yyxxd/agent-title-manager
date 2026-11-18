import { ArrowRight, RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { agentName, formatTime, localDate, num, rpc, type Counts, type Daily } from './rpc';
import { Empty, Section, Status, useLoad } from './ui';

const metrics = [['renames', '标题已应用'], ['attempts', '模型请求'], ['kept', '保留原标题'], ['unknownUsage', '用量未回报']] as const;

export function Overview({ openPlugins, revision }: { openPlugins: () => void; revision: number }) {
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
    <section className="metrics panel" aria-label="当日标题统计">
      {metrics.map(([key, label]) => <div key={key}><span>{label}</span><strong>{value ? num(value[key]) : '—'}</strong></div>)}
    </section>
    <div className="overview-grid">
      <Section title="Agent 活动">
        {rows.length ? <Activity rows={rows} /> : <Empty>{value ? '当天没有 Agent 活动' : error ? '无法载入活动' : '正在读取统计…'}</Empty>}
      </Section>
      <Section title="已知 Token" action={!!rows.length && <select className="agent-filter" aria-label="Token 用量的 Agent" value={selected?.agent ?? ''} onChange={(event) => setAgent(event.target.value)}>
        <option value="">全部 Agent</option>
        {rows.map((row) => <option key={row.agent} value={row.agent}>{agentName(row.agent)}</option>)}
      </select>}>
        {value ? <TokenRows value={selected ?? value} /> : <Empty>{error ? '无法载入用量' : '正在读取用量…'}</Empty>}
      </Section>
    </div>
    <footer className="overview-foot">
      {value?.since && <span>统计自 {formatTime(value.since)} 开始</span>}
      <button className="text-button" onClick={openPlugins}>管理 Agent <ArrowRight size={15} /></button>
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

function TokenRows({ value }: { value: Counts }) {
  return <dl className="token-rows">
    <div><dt>输入</dt><dd>{num(value.inputTokens)}</dd></div>
    <div><dt>输出</dt><dd>{num(value.outputTokens)}</dd></div>
    <div><dt>缓存</dt><dd>{num(value.cachedTokens)}</dd></div>
    <div><dt>用量未回报</dt><dd>{num(value.unknownUsage)} 次</dd></div>
  </dl>;
}
