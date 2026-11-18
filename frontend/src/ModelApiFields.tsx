import { ModelDiscovery, PopupClose } from './ModelDiscovery';
import { Info } from 'lucide-react';
import { useId, useRef } from 'react';
import { ModelSwitch } from './ModelSwitch';
import { rpc, type ApiEndpoints, type Settings } from './rpc';
import { Status, useLoad } from './ui';

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
  const baseUrlId = useId();
  const canUseSavedKey = hasApiKey && !addressChanged;
  const keyNeedsAttention = hasApiKey && addressChanged && !keyValue && !removeKey;

  return <div className="settings-rows panel-body model-api">
    <label>协议<select value={apiProtocol} onChange={(event) => edit('apiProtocol', event.target.value as Settings['apiProtocol'])}>
      {protocols.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
    </select></label>
    <div className="setting-row api-url-row">
      <label htmlFor={baseUrlId}>Base URL</label>
      <div className="api-url-field">
        <input id={baseUrlId} type="url" placeholder="https://…" value={apiBaseUrl} onChange={(event) => edit('apiBaseUrl', event.target.value)} />
        <EndpointPreview modelsUrl={listing.value?.modelsUrl} generationUrl={generationUrl}
          generationError={listing.value ? generation.error : undefined} />
      </div>
    </div>
    {listing.error && <Status error={listing.error} />}
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

function EndpointPreview({ generationUrl, modelsUrl, generationError }: {
  generationUrl?: string; modelsUrl?: string; generationError?: string;
}) {
  const panelId = useId();
  if (!generationUrl && !modelsUrl && !generationError) return null;
  return <>
    <button type="button" className="icon-button endpoint-trigger" aria-label="查看实际请求地址"
      title="查看实际请求地址" popoverTarget={panelId}><Info size={15} aria-hidden="true" /></button>
    <div id={panelId} popover="auto" className="settings-popup endpoint-popup" role="dialog" aria-label="实际请求地址">
      <div className="settings-popup-head">
        <h3>实际请求地址</h3>
        <PopupClose panelId={panelId} label="关闭请求地址" />
      </div>
      <dl className="endpoint-values">
        {generationUrl && <div><dt>生成地址</dt><dd><code>{generationUrl}</code></dd></div>}
        {modelsUrl && <div><dt>模型列表地址</dt><dd><code>{modelsUrl}</code></dd></div>}
      </dl>
      {generationError && <Status error={`无法预览生成地址：${generationError}`} />}
    </div>
  </>;
}
