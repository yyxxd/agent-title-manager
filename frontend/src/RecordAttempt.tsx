import { formatTime, providerName, token, type Attempt } from './rpc';

export function AttemptRow({ attempt, status }: { attempt: Attempt; status: string }) {
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
        <span>{status}</span>
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
