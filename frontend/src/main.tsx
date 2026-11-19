import { createRoot } from 'react-dom/client';
import { useState } from 'react';
import { useAppearance } from './Appearance';
import { Overview } from './Overview';
import { Models } from './Models';
import { Plugins } from './Plugins';
import { Records } from './Records';
import { Toaster } from './toast';
import { useUpdateEvents } from './Update';
import './style.css';
import './refinement.css';
import './controls.css';
import './toast.css';

const pages = [
  ['overview', '概览'],
  ['models', '模型与规则'],
  ['plugins', 'Agent 插件'],
  ['records', '处理记录'],
] as const;

function App() {
  useUpdateEvents();
  const [page, setPage] = useState<(typeof pages)[number][0]>('overview');
  const [revision, setRevision] = useState(0);
  const appearance = useAppearance();
  function select(next: typeof page) {
    setPage(next);
    setRevision((value) => value + 1);
  }
  return <div className="shell" data-ui>
    <header className="shell-head">
      <nav aria-label="管理面板">{pages.map(([key, label]) =>
        <button key={key} type="button" aria-current={page === key ? 'page' : undefined} onClick={() => select(key)}>{label}</button>,
      )}</nav>
    </header>
    <main id="main" key={page}>
      {page === 'overview' && <Overview revision={revision} />}
      {page === 'models' && <Models appearance={appearance} />}
      {page === 'plugins' && <Plugins />}
      {page === 'records' && <Records />}
    </main>
    <Toaster />
  </div>;
}

const runtimePath = '/wails/runtime.js';
void import(/* @vite-ignore */ runtimePath).catch(() => undefined).finally(() => {
  createRoot(document.getElementById('root')!).render(<App />);
});
