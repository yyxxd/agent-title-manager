import { X } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';
import { rpc, type Plugin } from './rpc';
import { StatusNotice, useAction } from './ui';

export type PluginModalState = { mode: 'install' } | { mode: 'update'; plugin: Plugin };

export function PluginModal({
  modal,
  onClose,
  onSuccess,
}: {
  modal: PluginModalState;
  onClose: () => void;
  onSuccess: (message: string) => void;
}) {
  const isUpdate = modal.mode === 'update';
  const [source, setSource] = useState<'local' | 'github'>('local');
  const [path, setPath] = useState('');
  const [repo, setRepo] = useState('');
  const [ref, setRef] = useState('');
  const action = useAction();

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !action.busy) onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [action.busy, onClose]);

  function submit(event: FormEvent) {
    event.preventDefault();
    const trimmedPath = path.trim();
    const trimmedRepo = repo.trim();
    const trimmedRef = ref.trim();
    if (source === 'local' && !trimmedPath) return;
    if (source === 'github' && (!trimmedRepo || !trimmedRef)) return;

    const method = isUpdate ? 'plugins.update' : 'plugins.install';
    const payload =
      source === 'local'
        ? { ...(isUpdate ? { id: modal.plugin.id } : {}), source: 'local', path: trimmedPath }
        : { ...(isUpdate ? { id: modal.plugin.id } : {}), source: 'github', repo: trimmedRepo, ref: trimmedRef };

    void action.run(async () => {
      await rpc(method, payload);
      onSuccess(isUpdate ? `社区插件 ${modal.plugin.name} 已更新` : '社区插件安装成功');
    }, { success: 'silent', failure: 'inline' });
  }

  return (
    <div
      className="modal-backdrop"
      onClick={(event) => {
        if (event.target === event.currentTarget && !action.busy) onClose();
      }}
    >
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby="plugin-modal-title">
        <div className="modal-head">
          <h2 className="modal-title" id="plugin-modal-title">
            {isUpdate ? `更新社区插件 · ${modal.plugin.name}` : '安装社区插件'}
          </h2>
          <button
            type="button"
            className="icon-btn"
            disabled={action.busy}
            onClick={onClose}
            aria-label="关闭"
          >
            <X size={15} aria-hidden="true" />
          </button>
        </div>

        <form onSubmit={submit}>
          <div className="modal-body form-fields">
            <div className="field">
              <span className="field-label">安装来源</span>
              <div className="segmented" style={{ width: 'fit-content' }}>
                <button
                  type="button"
                  className={`seg-btn ${source === 'local' ? 'active' : ''}`}
                  disabled={action.busy}
                  onClick={() => setSource('local')}
                >
                  本地目录
                </button>
                <button
                  type="button"
                  className={`seg-btn ${source === 'github' ? 'active' : ''}`}
                  disabled={action.busy}
                  onClick={() => setSource('github')}
                >
                  GitHub 仓库
                </button>
              </div>
            </div>

            {source === 'local' ? (
              <div className="field">
                <label className="field-label" htmlFor="pm-path">
                  插件目录（需包含 agent-title-plugin.json）
                </label>
                <input
                  id="pm-path"
                  className="input mono"
                  required
                  disabled={action.busy}
                  placeholder="输入本地目录绝对路径"
                  value={path}
                  onChange={(e) => setPath(e.target.value)}
                />
              </div>
            ) : (
              <>
                <div className="field">
                  <label className="field-label" htmlFor="pm-repo">
                    GitHub 仓库
                  </label>
                  <input
                    id="pm-repo"
                    className="input mono"
                    required
                    disabled={action.busy}
                    placeholder="owner/repo"
                    value={repo}
                    onChange={(e) => setRepo(e.target.value)}
                  />
                </div>
                <div className="field">
                  <label className="field-label" htmlFor="pm-ref">
                    完整提交 SHA（40 位）
                  </label>
                  <input
                    id="pm-ref"
                    className="input mono"
                    required
                    disabled={action.busy}
                    placeholder="例如 a1b2c3d4..."
                    value={ref}
                    onChange={(e) => setRef(e.target.value)}
                  />
                </div>
              </>
            )}

            {action.error && <StatusNotice error={action.error} />}
          </div>

          <div className="modal-foot">
            <button type="button" className="btn btn-sm" disabled={action.busy} onClick={onClose}>
              取消
            </button>
            <button type="submit" className="btn btn-sm btn-primary" disabled={action.busy}>
              {action.busy ? '处理中…' : isUpdate ? '确认更新' : '安装插件'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
