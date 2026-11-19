import { useEffect, useState } from 'react';
import { ModelCombobox } from './ModelCombobox';
import { rpc, type ApiEndpoints, type ApiModel, type ApiProtocol, type Settings } from './rpc';
import { toast } from './toast';
import { StatusNotice, useAction, useLoad } from './ui';

const protocols: { value: ApiProtocol; label: string }[] = [
  { value: 'responses', label: 'OpenAI Responses' },
  { value: 'chat', label: 'OpenAI Chat Completions' },
  { value: 'anthropic', label: 'Anthropic Messages' },
  { value: 'gemini', label: 'Google Gemini' },
];

type ApiFieldsProps = {
  title: string;
  settings: Settings;
  edit: <K extends keyof Settings>(field: K, value: Settings[K]) => void;
  keyValue: string;
  apiKey?: string;
  removeKey: boolean;
  addressChanged: boolean;
  busy: boolean;
  credentialRevision: number;
  setKey: (key: string) => void;
  setRemoveKey: (enabled: boolean) => void;
};

export function ModelApiFields({
  title,
  settings,
  edit,
  keyValue,
  apiKey,
  removeKey,
  addressChanged,
  busy,
  credentialRevision,
  setKey,
  setRemoveKey,
}: ApiFieldsProps) {
  const { apiBaseUrl, apiProtocol, apiModel, hasApiKey } = settings;
  const target = { apiBaseUrl, apiProtocol };
  const hasAddress = !!apiBaseUrl.trim();
  const targetKey = JSON.stringify([apiProtocol, apiBaseUrl]);

  const listing = useLoad(
    () => (hasAddress ? rpc<ApiEndpoints>('providers.endpoints', target) : Promise.resolve(undefined)),
    targetKey,
  );
  const generation = useLoad(
    () =>
      hasAddress && apiModel
        ? rpc<ApiEndpoints>('providers.endpoints', { ...target, apiModel })
        : Promise.resolve(undefined),
    JSON.stringify([targetKey, apiModel]),
  );

  const [models, setModels] = useState<ApiModel[]>();
  const modelsAction = useAction();

  useEffect(() => {
    setModels(undefined);
    modelsAction.reset();
  }, [targetKey, credentialRevision]);

  const generationUrl = listing.value && (apiModel ? generation.value?.generationUrl : listing.value.generationUrl);
  const modelsUrl = listing.value?.modelsUrl;
  const listingUnavailable = modelsUrl === '';
  const canUseSavedKey = hasApiKey && !addressChanged;
  const keyNeedsAttention = hasApiKey && addressChanged && !keyValue.trim() && !removeKey;

  function fetchModels(notify = false) {
    if (!hasAddress || listingUnavailable) return;
    void modelsAction.run(async () => {
      const result = await rpc<{ items: ApiModel[] }>('providers.models', { ...target, apiKey });
      setModels(result.items ?? []);
      if (notify) toast.success('已拉取最新可用模型列表');
    }, { success: 'silent', failure: 'inline' });
  }

  return (
    <section className="card">
      <div className="card-header">
        <h2 className="card-title">{title}</h2>
        <span className="card-sub">支持 OpenAI / Anthropic / Gemini 协议</span>
      </div>
      <div className="card-body form-fields">
        <div className="field-row-2">
          <div className="field">
            <label className="field-label" htmlFor="api-protocol">
              接口协议
            </label>
            <select
              id="api-protocol"
              className="select"
              disabled={busy}
              value={apiProtocol}
              onChange={(e) => edit('apiProtocol', e.target.value as ApiProtocol)}
            >
              {protocols.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
          </div>

          <div className="field">
            <label className="field-label" htmlFor="api-key">
              API 密钥
            </label>
            <div className="input-group">
              <input
                id="api-key"
                type="password"
                className="input"
                autoComplete="new-password"
                disabled={busy || removeKey}
                value={keyValue}
                placeholder={canUseSavedKey ? '已安全保存（留空则沿用）' : '输入 API Key'}
                onChange={(e) => {
                  if (removeKey) setRemoveKey(false);
                  setKey(e.target.value);
                }}
              />
              {hasApiKey && (
                <button
                  type="button"
                  className={`btn btn-sm ${removeKey ? '' : 'btn-ghost'}`}
                  disabled={busy}
                  title={removeKey ? '撤销清除' : '保存时清除已存密钥'}
                  onClick={() => {
                    const next = !removeKey;
                    setRemoveKey(next);
                    if (next) setKey('');
                  }}
                >
                  {removeKey ? '已标记清除' : '清除'}
                </button>
              )}
            </div>
          </div>
        </div>

        {keyNeedsAttention && (
          <p className="field-hint warn">Base URL 已修改：保存前请填写新密钥，或点击右侧「清除」移除旧密钥。</p>
        )}

        <div className="field">
          <label className="field-label" htmlFor="api-base-url">
            Base URL
          </label>
          <input
            id="api-base-url"
            type="url"
            className="input mono"
            disabled={busy}
            placeholder="https://api.example.com"
            value={apiBaseUrl}
            onChange={(e) => edit('apiBaseUrl', e.target.value)}
          />
          {listing.error && <StatusNotice error={listing.error} />}
          {(generationUrl || modelsUrl) && (
            <div className="field-preview mono">
              {generationUrl && <span>实际请求：{generationUrl}</span>}
              {modelsUrl && <span>模型列表：{modelsUrl}</span>}
            </div>
          )}
          {listing.value && generation.error && <StatusNotice error={`无法预览生成地址：${generation.error}`} />}
          {listingUnavailable && <p className="field-hint">此地址不提供模型列表接口，请直接输入模型 ID。</p>}
        </div>

        <div className="field">
          <span className="field-label">API 模型</span>
          <ModelCombobox
            value={apiModel}
            placeholder="输入模型 ID 或从右侧列表选择"
            options={(models ?? []).map((item) => ({ id: item.id, name: item.name }))}
            allowCustomInput
            disabled={busy}
            dropdownDisabled={!hasAddress || listingUnavailable}
            busy={modelsAction.busy}
            error={modelsAction.error ? `${modelsAction.error} 可手动输入模型 ID。` : undefined}
            emptyMessage="模型列表为空，可手动输入模型 ID。"
            onInputChange={(next) => edit('apiModel', next)}
            onSelect={(id) => edit('apiModel', id)}
            onOpenFetch={() => {
              if (!models && !modelsAction.busy) fetchModels(false);
            }}
            onRefresh={() => fetchModels(true)}
          />
        </div>
      </div>
    </section>
  );
}
