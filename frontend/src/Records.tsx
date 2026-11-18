import { RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { agentName, formatTime, num, rpc, type Attempt, type RecordItem } from './rpc';
import { Empty, Section, Status, useAction, useLoad } from './ui';

const states: Record<string, string> = {
  queued: '待处理', running: '命名中', candidate: '候选待领取', claimed: '待读回',
  applied: '标题已应用', kept: '保留原名', stale: '已过期', failed: '生成失败',
  interrupted: '已中断', native: '已有原生标题', completed: '已完成',
  invalid_response: '响应无效', rejected_tool: '工具调用已拒绝',
};
const syncStates: Record<string, string> = {
  unbound: 'Orca 会话未确认绑定', unchanged: 'Orca 标题已一致', protected: 'Orca 标题受保护',
  pending: 'Orca 待同步', writing: 'Orca 同步中', synced: 'Orca 已同步并读回',
  failed: 'Orca 同步失败', stale: 'Orca 同步已过期',
};

export function Records() {
  const [refresh, setRefresh] = useState(0);
  const [confirm, setConfirm] = useState('');
  const { value, error } = useLoad(() => rpc<{ items: RecordItem[] }>('records.list', { limit: 50 }), refresh);
  const action = useAction();
  function sync(id: string, verb: 'adopt' | 'retry') {
    return action.run(async () => {
      const result = await rpc<{ accepted: boolean }>(`sync.${verb}`, { id });
      if (!result.accepted) throw new Error('记录状态已更新，请刷新后查看。');
      setConfirm('');
      setRefresh((value) => value + 1);
      return '同步已排队。';
    });
  }

  return <>
    <header className="page-head"><h1>处理记录</h1>
      <button className="icon-button" aria-label="刷新记录" title="刷新记录" onClick={() => setRefresh(refresh + 1)}><RefreshCw size={16} /></button>
    </header>
    <Status error={error ?? action.error} message={action.message} />
    <Section title="最近处理的会话">
      {value?.items?.length ? value.items.map((item) => <RecordRow key={item.id} item={item} busy={action.busy}
        confirming={confirm === item.id} confirm={() => setConfirm(item.id)} cancel={() => setConfirm('')}
        sync={(verb) => void sync(item.id, verb)} />) : <Empty>{value ? '还没有处理记录' : error ? '无法读取记录' : '正在读取记录…'}</Empty>}
    </Section>
  </>;
}

function RecordRow({ item, busy, confirming, confirm, cancel, sync }: {
  item: RecordItem; busy: boolean; confirming: boolean; confirm: () => void;
  cancel: () => void; sync: (verb: 'adopt' | 'retry') => void;
}) {
  return <article className="record-row">
    <div className="record-head">
      <div>
        <h3>{item.title || '会话标题尚未生成'}</h3>
        <p className="muted">{agentName(item.agent)} · {formatTime(item.updatedAt)} · {states[item.status] ?? item.status}</p>
      </div>
      {item.syncStatus && <span className={item.syncStatus === 'failed' ? 'sync error-text' : 'sync'}>{syncStates[item.syncStatus] ?? item.syncStatus}</span>}
    </div>
    {item.syncError && <p>{item.syncError}</p>}
    {item.syncStatus === 'protected' && !item.syncError && <p className="muted">当前 Orca 标题可能由用户设置，已保留。</p>}
    <div className="actions">
      {item.syncStatus === 'failed' && <button disabled={busy} onClick={() => sync('retry')}>重试同步</button>}
      {item.syncStatus === 'protected' && !confirming && <button disabled={busy} onClick={confirm}>采用 Agent 标题</button>}
    </div>
    {confirming && <div className="confirm">
      <p>将 {agentName(item.agent)} 会话 <code>{item.sessionId}</code> 对应的 Orca 标题改为“{item.title}”。</p>
      <div className="actions"><button disabled={busy} onClick={cancel}>取消</button><button className="primary" disabled={busy} onClick={() => sync('adopt')}>确认采用</button></div>
    </div>}
    {!!item.attempts?.length && <details><summary>模型与用量 · {item.attempts.length} 次</summary>
      <ul className="attempt-list">{item.attempts.map((attempt, index) => <AttemptRow key={index} attempt={attempt} />)}</ul>
    </details>}
  </article>;
}

function AttemptRow({ attempt }: { attempt: Attempt }) {
  const token = (value?: number) => typeof value === 'number' ? num(value) : '未回报';
  const actual = attempt.actualTier && attempt.actualTier !== 'unknown' ? attempt.actualTier : '未回报';
  return <li>
    <strong>{attempt.provider === 'api' ? '自定义 API' : agentName(attempt.provider)} · {attempt.model || '未知模型'}</strong>
    <span>{states[attempt.status] ?? attempt.status} · 请求档位 {attempt.requestedTier || '未回报'} · 实际档位 {actual}</span>
    <span>{attempt.usageKnown ? `输入 ${token(attempt.inputTokens)} · 输出 ${token(attempt.outputTokens)} · 缓存 ${token(attempt.cachedTokens)} Token` : 'Token 用量未回报'}</span>
  </li>;
}
