import { RefreshCw } from 'lucide-react';
import { useState } from 'react';
import { agentName, formatTime, providerName, rpc, token, type Attempt, type RecordItem } from './rpc';
import { ConfirmDialog, Empty, Section, Status, useAction, useLoad } from './ui';

const states: Record<string, string> = {
  queued: '待处理', running: '命名中', candidate: '候选待领取', claimed: '待读回',
  applied: '标题已应用', kept: '保留原名', stale: '已过期', failed: '生成失败',
  interrupted: '已中断', native: '已有原生标题', completed: '已完成', disabled: '插件已停用',
  invalid_response: '响应无效', rejected_tool: '工具调用已拒绝',
};
const syncStates: Record<string, string> = {
  unbound: 'Orca 会话未确认绑定', unchanged: 'Orca 标题已一致', protected: 'Orca 标题受保护',
  pending: 'Orca 待同步', writing: 'Orca 同步中', synced: 'Orca 已同步并读回',
  failed: 'Orca 同步失败', stale: 'Orca 同步已过期', skipped: 'Orca 安全跳过',
};

export function Records() {
  const [refresh, setRefresh] = useState(0);
  const [confirm, setConfirm] = useState('');
  const { value, error } = useLoad(() => rpc<{ items: RecordItem[]; testAttempts?: Attempt[] }>('records.list', { limit: 50 }), refresh);
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
    <Status error={error ?? (!confirm ? action.error : undefined)} message={action.message} />
    <Section title="最近处理的会话">
      {value?.items?.length ? value.items.map((item) => <RecordRow key={item.id} item={item} busy={action.busy}
        confirming={confirm === item.id} error={action.error} confirm={() => { action.reset(); setConfirm(item.id); }} cancel={() => setConfirm('')}
        sync={(verb) => void sync(item.id, verb)} />) : <Empty>{value ? '还没有处理记录' : error ? '无法读取记录' : '正在读取记录…'}</Empty>}
    </Section>
    {!!value?.testAttempts?.length && <Section title="连接测试">
      <ul className="attempt-list">{value.testAttempts.map((attempt, index) => <AttemptRow key={index} attempt={attempt} />)}</ul>
    </Section>}
  </>;
}

function RecordRow({ item, busy, confirming, error, confirm, cancel, sync }: {
  item: RecordItem; busy: boolean; confirming: boolean; confirm: () => void;
  error?: string; cancel: () => void; sync: (verb: 'adopt' | 'retry') => void;
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
      {item.syncStatus === 'protected' && <button disabled={busy} onClick={confirm}>采用 Agent 标题</button>}
    </div>
    {confirming && <ConfirmDialog title="采用 Agent 标题" busy={busy} error={error}
      confirmLabel="确认采用" onCancel={cancel} onConfirm={() => sync('adopt')}>
      <p>将 {agentName(item.agent)} 会话 <code>{item.sessionId}</code> 对应的 Orca 标题改为“{item.title}”。</p>
    </ConfirmDialog>}
    {!!item.attempts?.length && <details><summary>模型与用量 · {item.attempts.length} 次</summary>
      <ul className="attempt-list">{item.attempts.map((attempt, index) => <AttemptRow key={index} attempt={attempt} />)}</ul>
    </details>}
  </article>;
}

function AttemptRow({ attempt }: { attempt: Attempt }) {
  const actual = attempt.actualTier && attempt.actualTier !== 'unknown' ? attempt.actualTier : '未回报';
  const usage = (value?: number | null) => token(value, '未回报');
  return <li>
    <strong>{providerName(attempt.provider)} · {attempt.model || '未知模型'}</strong>
    {attempt.startedAt && <span>{formatTime(attempt.startedAt)}</span>}
    <span>{states[attempt.status] ?? attempt.status} · 请求档位 {attempt.requestedTier || '未回报'} · 实际档位 {actual}</span>
    <span>Token 输入 {usage(attempt.inputTokens)} · 输出 {usage(attempt.outputTokens)} · 缓存 {usage(attempt.cachedTokens)}</span>
  </li>;
}
