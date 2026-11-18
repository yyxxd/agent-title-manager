import { createRoot } from 'react-dom/client';
import { useState } from 'react';
import { Activity, AudioLines, Blocks, ListTree, Settings2 } from 'lucide-react';
import { Overview } from './Overview';
import { Models } from './Models';
import { Plugins } from './Plugins';
import { Records } from './Records';
import './style.css';

const pages = [
  ['overview', '概览', Activity],
  ['models', '模型与规则', Settings2],
  ['plugins', 'Agent 插件', Blocks],
  ['records', '处理记录', ListTree],
] as const;

function App() {
  const [page, setPage] = useState<(typeof pages)[number][0]>('overview');
  const [revision, setRevision] = useState(0);
  function select(next: typeof page) {
    setPage(next);
    setRevision((value) => value + 1);
  }
  return <div className="shell" data-ui>
    <header className="shell-head">
      <div className="brand"><AudioLines size={19} strokeWidth={2} /><span>Agent Title Manager</span></div>
      <nav aria-label="管理面板">{pages.map(([key, label, Icon]) =>
        <button key={key} type="button" aria-current={page === key ? 'page' : undefined} onClick={() => select(key)}><Icon size={16} /><span>{label}</span></button>,
      )}</nav>
    </header>
    <main id="main" key={page}>
      {page === 'overview' && <Overview openPlugins={() => select('plugins')} revision={revision} />}
      {page === 'models' && <Models />}
      {page === 'plugins' && <Plugins />}
      {page === 'records' && <Records />}
    </main>
  </div>;
}

const runtimePath = '/wails/runtime.js';
void import(/* @vite-ignore */ runtimePath).catch(() => undefined).finally(() => {
  createRoot(document.getElementById('root')!).render(<App />);
});
