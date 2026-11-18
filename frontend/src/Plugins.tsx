import { useState, type FormEvent } from 'react';
import { rpc, type Plugin } from './rpc';
import { ConfirmDialog, Empty, Section, Status, useAction, useLoad } from './ui';
import './plugin-record-rows.css';

const states: Record<string, string> = {
  enabled: '已启用', disabled: '已停用', action_required: '待操作', unavailable: '不可用', recovery_required: '待恢复',
};

type PluginState = Plugin & {
  takeoverRequired?: boolean;
  canRestore?: boolean;
  entryPath?: string;
  availableVersion?: string;
};
type Confirmation = { id: string; verb: 'enable' | 'restore' };

export function Plugins() {
  const [refresh, setRefresh] = useState(0);
  const [transfer, setTransfer] = useState<{ id?: string } | null>(null);
  const [confirm, setConfirm] = useState<Confirmation | null>(null);
  const { value, error } = useLoad(() => rpc<{ items: PluginState[] }>('plugins.list'), refresh);
  const action = useAction();
  const reload = () => setRefresh((value) => value + 1);
  const mutate = (verb: string, params: object) => action.run(async () => {
    await rpc(`plugins.${verb}`, params);
    setConfirm(null);
    setTransfer(null);
    reload();
    return '插件状态已更新。';
  });
  const items = value?.items ?? [];
  return <>
    <header className="page-head"><h1>Agent 插件</h1>
      <button type="button" className="primary" disabled={action.busy} onClick={() => setTransfer({})}>安装社区插件</button>
    </header>
    <Status error={error ?? (!confirm ? action.error : undefined)} message={action.busy ? '正在处理插件…' : action.message} />
    {transfer && <Transfer key={transfer.id ?? 'install'} id={transfer.id} busy={action.busy}
      dismiss={() => setTransfer(null)} submit={(params) => void mutate(transfer.id ? 'update' : 'install', params)} />}
    {!value && !error && <Empty>正在读取插件…</Empty>}
    {value && <>
      <Section title="内置适配器">
        {items.filter((plugin) => plugin.kind === 'builtin').map(renderPlugin)}
      </Section>
      <Section title="社区插件">
        {items.some((plugin) => plugin.kind === 'community') ? items.filter((plugin) => plugin.kind === 'community').map(renderPlugin) : <Empty>尚未安装社区插件</Empty>}
      </Section>
    </>}
  </>;

  function renderPlugin(plugin: PluginState) {
    const waiting = confirm?.id === plugin.id;
    return <article className="plugin-row" key={plugin.id}>
      <div className="plugin-copy">
        <h3>{plugin.name}</h3>
        <div className="muted item-meta">{plugin.version && <span>v{plugin.version}</span>}<span>{states[plugin.status] ?? plugin.status}</span></div>
        <PluginDetail detail={plugin.detail} status={plugin.status} />
        {plugin.entryPath && <details><summary>启动入口</summary><code>{plugin.entryPath}</code></details>}
      </div>
      <div className="actions">
        {plugin.enabled ? <button disabled={action.busy} onClick={() => void mutate('disable', { id: plugin.id })}>停用</button>
          : <button disabled={action.busy || plugin.status === 'unavailable' || plugin.status === 'recovery_required'} onClick={() => plugin.takeoverRequired ? ask(plugin.id, 'enable') : void mutate('enable', { id: plugin.id })}>{plugin.takeoverRequired ? '接管旧插件' : '启用'}</button>}
        {plugin.kind === 'community' && <button disabled={action.busy || plugin.enabled} onClick={() => setTransfer({ id: plugin.id })}>更新</button>}
        {plugin.kind === 'builtin' && plugin.availableVersion &&
          <button disabled={action.busy} onClick={() => void mutate('update', { id: plugin.id, source: 'bundled' })}>更新至 v{plugin.availableVersion}</button>}
        {plugin.canRestore && <button disabled={action.busy} onClick={() => ask(plugin.id, 'restore')}>恢复</button>}
      </div>
      {waiting && <ConfirmDialog title={confirm.verb === 'enable' ? '接管旧插件' : '恢复插件'} busy={action.busy}
        error={action.error} confirmLabel={confirm.verb === 'enable' ? '确认接管' : '确认恢复'}
        onCancel={() => setConfirm(null)} onConfirm={() => void mutate(confirm.verb, { id: plugin.id, ...(confirm.verb === 'enable' && { takeover: true }) })}>
        <p>{confirm.verb === 'enable' ? `接管后，本应用将处理 ${plugin.name} 的命名触发。可通过“恢复”撤销。` : `将 ${plugin.name} 恢复到上一状态。`}</p>
      </ConfirmDialog>}
    </article>;
  }

  function ask(id: string, verb: Confirmation['verb']) {
    action.reset();
    setConfirm({ id, verb });
  }
}

function PluginDetail({ detail, status }: Pick<Plugin, 'detail' | 'status'>) {
  const text = detail?.trim();
  if (!text) return null;
  const canCollapse = status === 'enabled' || status === 'disabled';
  if (canCollapse && (text.length > 120 || /[\r\n]/.test(text))) {
    return <details><summary>使用说明</summary><p className="plugin-detail">{text}</p></details>;
  }
  return <p className="muted plugin-detail">{text}</p>;
}

function Transfer({ id, busy, dismiss, submit }: {
  id?: string; busy: boolean; dismiss: () => void; submit: (params: object) => void;
}) {
  const [source, setSource] = useState('local');
  function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const fields = Object.fromEntries([...new FormData(event.currentTarget)].map(([key, value]) => [key, String(value).trim()]));
    submit({ id, source, ...fields });
  }
  return <Section title={id ? `更新 ${id}` : '安装社区插件'}>
    <form onSubmit={onSubmit}>
      <fieldset disabled={busy} className="settings-rows">
        <label>来源<select value={source} onChange={(event) => setSource(event.target.value)}>
          <option value="local">本地插件</option><option value="github">GitHub 仓库</option>
        </select></label>
        {source === 'local' ? <label>本地插件目录<input name="path" required placeholder="C:\插件目录" /></label> : <>
          <label>仓库<input name="repo" required pattern="[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+" placeholder="owner/repo" /></label>
          <label>提交 SHA<input name="ref" required pattern="[0-9a-fA-F]{40}" placeholder="40 位提交 SHA" /></label>
        </>}
        <div className="actions setting-actions"><button type="button" onClick={dismiss}>取消</button><button type="submit" className="primary">{id ? '更新插件' : '安装插件'}</button></div>
      </fieldset>
    </form>
  </Section>;
}
