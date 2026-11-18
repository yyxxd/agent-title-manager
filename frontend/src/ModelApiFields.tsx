import { useId, useRef, useState } from 'react';
import { ModelSwitch } from './ModelSwitch';
import { rpc, type ApiEndpoints, type ApiModel, type ApiTarget, type Settings } from './rpc';
import { Status, useAction, useLoad } from './ui';

const protocols = [
  ['chat', 'OpenAI Chat Completions'],
  ['responses', 'OpenAI Responses'],
  ['anthropic', 'Anthropic Messages'],
  ['gemini', 'Google Gemini'],
] as const;

type ApiFieldsProps = {
  settings: Settings; edit: <K extends keyof Settings>(field: K, value: Settings[K]) => void;
  keyValue: string; apiKey?: string; removeKey: boolean; addressChanged: boolean; busy: boolean;
  credentialRevision: number; setKey: (key: string) => void; setRemoveKey: (enabled: boolean) => void;
};

export function ApiFields({
  settings, edit, keyValue, apiKey, removeKey, addressChanged, busy, credentialRevision, setKey, setRemoveKey,
}: ApiFieldsProps) {
  const { apiBaseUrl, apiProtocol, apiModel, hasApiKey } = settings;
  const target = { apiBaseUrl, apiProtocol };
  const hasAddress = !!apiBaseUrl.trim();
  const targetKey = JSON.stringify([apiProtocol, apiBaseUrl]);
  const listing = useLoad(() => hasAddress
    ? rpc<ApiEndpoints>('providers.endpoints', target)
    : Promise.resolve(undefined), targetKey);
  const generation = useLoad(() => hasAddress && apiModel
    ? rpc<ApiEndpoints>('providers.endpoints', { ...target, apiModel })
    : Promise.resolve(undefined), JSON.stringify([targetKey, apiModel]));
  const generationUrl = listing.value && (apiModel ? generation.value?.generationUrl : listing.value.generationUrl);
  const listingUnavailable = listing.value?.modelsUrl === '';
  const modelInput = useRef<HTMLInputElement>(null);
  const keyId = useId();
  const canUseSavedKey = hasApiKey && !addressChanged;
  const keyNeedsAttention = hasApiKey && addressChanged && !keyValue && !removeKey;

  return <div className="settings-rows panel-body model-api">
    <label>协议<select value={apiProtocol} onChange={(event) => edit('apiProtocol', event.target.value as Settings['apiProtocol'])}>
      {protocols.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
    </select></label>
    <label>Base URL<input type="url" placeholder="https://…" value={apiBaseUrl} onChange={(event) => edit('apiBaseUrl', event.target.value)} /></label>
    <EndpointPreview modelsUrl={listing.value?.modelsUrl} generationUrl={generationUrl}
      addressError={listing.error} generationError={listing.value ? generation.error : undefined} />
    <label>API 密钥<input autoComplete="new-password" type="password" value={keyValue} disabled={removeKey}
      placeholder={canUseSavedKey ? '已保存密钥' : '输入密钥'} aria-describedby={keyNeedsAttention ? keyId : undefined}
      onChange={(event) => setKey(event.target.value)} /></label>
    {keyNeedsAttention && <p className="model-key-note muted" id={keyId}>地址已更改。保存前请替换或移除旧密钥。</p>}
    {hasApiKey && <ModelSwitch checked={removeKey} busy={busy} onChange={setRemoveKey}>保存时移除旧密钥</ModelSwitch>}
    <label>API 模型<input ref={modelInput} placeholder="输入模型 ID" value={apiModel} onChange={(event) => edit('apiModel', event.target.value)} /></label>
    <ModelDiscovery key={JSON.stringify([targetKey, credentialRevision])}
      target={target} apiKey={apiKey} blocked={!listing.value?.modelsUrl}
      listingUnavailable={listingUnavailable} selected={apiModel} onSelect={(id) => {
        edit('apiModel', id);
        modelInput.current?.focus();
      }} />
  </div>;
}

function EndpointPreview({ generationUrl, modelsUrl, addressError, generationError }: {
  generationUrl?: string; modelsUrl?: string; addressError?: string; generationError?: string;
}) {
  if (addressError) return <Status error={addressError} />;
  return <>
    {(generationUrl || modelsUrl) && <dl className="model-endpoints setting-row" aria-label="实际请求地址">
      {generationUrl && <><dt>生成地址</dt><dd><code>{generationUrl}</code></dd></>}
      {modelsUrl && <><dt>模型列表地址</dt><dd><code>{modelsUrl}</code></dd></>}
    </dl>}
    {generationError && <Status error={`无法预览生成地址。${generationError}`} />}
  </>;
}

function ModelDiscovery({ target, apiKey, blocked, listingUnavailable, selected, onSelect }: {
  target: ApiTarget; apiKey?: string; blocked: boolean; listingUnavailable: boolean;
  selected: string; onSelect: (id: string) => void;
}) {
  const [items, setItems] = useState<ApiModel[]>();
  const [query, setQuery] = useState('');
  const action = useAction();
  const normalized = query.trim().toLowerCase();
  const filtered = items?.filter(({ id, name }) => id.toLowerCase().includes(normalized) || name?.toLowerCase().includes(normalized));

  function fetchModels() {
    setItems(undefined);
    setQuery('');
    void action.run(async () => {
      const result = await rpc<{ items: ApiModel[] }>('providers.models', { ...target, apiKey });
      setItems(result.items ?? []);
    });
  }

  return <div className="model-discovery" aria-busy={action.busy}>
    <div className="model-discovery-controls">
      {!!items?.length && <input type="search" aria-label="查找模型" placeholder="查找模型" value={query} onChange={(event) => setQuery(event.target.value)}
        onKeyDown={(event) => { if (event.key === 'Enter') event.preventDefault(); }} />}
      <button type="button" disabled={blocked || action.busy} onClick={fetchModels}>{action.busy ? '正在获取…' : '获取模型列表'}</button>
    </div>
    {action.error ? <Status error={`${action.error} 可手动输入模型 ID。`} /> :
      <Status message={listingUnavailable ? '此地址不提供模型列表，可手动输入模型 ID。' :
        items?.length === 0 ? '模型列表为空，可手动输入模型 ID。' :
        filtered?.length === 0 ? '没有匹配的模型。' : undefined} />}
    {!!filtered?.length && <ul className="model-options" aria-label="可用模型">
      {filtered.map(({ id, name }) => <li key={id}><button type="button" aria-pressed={id === selected} onClick={() => onSelect(id)}>
        <span>{name || id}</span>{name && name !== id && <small className="muted">{id}</small>}
      </button></li>)}
    </ul>}
  </div>;
}
