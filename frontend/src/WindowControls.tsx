import { Copy, Minus, Square, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { errorText } from './rpc';
import { toast } from './toast';

export type DesktopWindow = {
  IsMaximised(): Promise<boolean>;
  Minimise(): Promise<void>;
  ToggleMaximise(): Promise<void>;
  Close(): Promise<void>;
};

export function WindowControls({ desktopWindow }: { desktopWindow: DesktopWindow }) {
  const [maximised, setMaximised] = useState(false);

  useEffect(() => {
    let request = 0;
    function refresh() {
      const current = ++request;
      void desktopWindow.IsMaximised().then((value) => {
        if (current === request) setMaximised(value);
      }).catch((error: unknown) => {
        if (current === request) console.error('无法读取窗口状态', error);
      });
    }
    refresh();
    window.addEventListener('resize', refresh);
    return () => {
      ++request;
      window.removeEventListener('resize', refresh);
    };
  }, [desktopWindow]);

  function perform(action: () => Promise<void>) {
    void action().catch((error: unknown) => toast.error(errorText(error)));
  }

  const maximiseLabel = maximised ? '还原窗口' : '最大化窗口';

  return (
    <div className="window-controls" role="group" aria-label="窗口控制">
      <button type="button" className="window-minimise" aria-label="最小化窗口" title="最小化窗口" onClick={() => perform(() => desktopWindow.Minimise())}>
        <Minus aria-hidden="true" />
      </button>
      <button type="button" className="window-maximise" aria-label={maximiseLabel} title={maximiseLabel} onClick={() => perform(() => desktopWindow.ToggleMaximise())}>
        {maximised ? <Copy aria-hidden="true" /> : <Square aria-hidden="true" />}
      </button>
      <button type="button" className="window-close" aria-label="关闭窗口" title="关闭窗口" onClick={() => perform(() => desktopWindow.Close())}>
        <X aria-hidden="true" />
      </button>
    </div>
  );
}
