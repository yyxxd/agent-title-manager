import { Minus, X } from 'lucide-react';
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
        <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" aria-hidden="true">
          {maximised ? (
            <>
              <rect x="3.5" y="5.5" width="5" height="5" />
              <path d="M5.5 5.5v-2h5v5h-2" />
            </>
          ) : <rect x="3.5" y="3.5" width="7" height="7" />}
        </svg>
      </button>
      <button type="button" className="window-close" aria-label="关闭窗口" title="关闭窗口" onClick={() => perform(() => desktopWindow.Close())}>
        <X aria-hidden="true" />
      </button>
    </div>
  );
}
