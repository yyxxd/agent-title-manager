import { useCallback, useEffect, useRef, useState } from 'react';
import { errorText, rpc } from './rpc';
import { Segmented } from './Segmented';
import { toast } from './toast';
import { StatusNotice, useAction } from './ui';

export const appearanceModes = [
  ['system', '跟随系统'],
  ['light', '浅色'],
  ['dark', '深色'],
] as const;

export type AppearanceMode = (typeof appearanceModes)[number][0];
export type AppearanceState = { mode: AppearanceMode; theme: 'light' | 'dark'; revision: number };

const modeLabels: Record<AppearanceMode, string> = {
  system: '跟随系统',
  light: '浅色',
  dark: '深色',
};

export function useAppearance() {
  const [state, setState] = useState<AppearanceState>();
  const [loadError, setLoadError] = useState<string>();
  const latest = useRef(0);

  const accept = useCallback((next: AppearanceState) => {
    if (next.revision < latest.current) return;
    latest.current = next.revision;
    document.documentElement.dataset.theme = next.theme;
    setState(next);
    setLoadError(undefined);
  }, []);

  useEffect(() => {
    const stop = window.wails?.Events?.On('desktop:appearance', ({ data }) => accept(data as AppearanceState));
    void rpc<AppearanceState>('desktop.appearance.get').then(accept, (error) => setLoadError(errorText(error)));
    return stop;
  }, [accept]);

  const setMode = useCallback(
    async (mode: AppearanceMode, notify = false) => {
      const next = await rpc<AppearanceState>('desktop.appearance.set', { mode });
      accept(next);
      if (notify) toast.success(`外观已设为${modeLabels[mode]}`);
      return next;
    },
    [accept],
  );

  const toggleLightDark = useCallback(async () => {
    const currentTheme = state?.theme ?? (document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light');
    const nextMode: AppearanceMode = currentTheme === 'dark' ? 'light' : 'dark';
    try {
      await setMode(nextMode);
      toast.success(`已切换为${nextMode === 'dark' ? '深色' : '浅色'}外观`);
    } catch (error) {
      toast.error(errorText(error));
    }
  }, [setMode, state?.theme]);

  return { state, accept, setMode, toggleLightDark, loadError };
}

export type AppearanceControl = ReturnType<typeof useAppearance>;

export function AppearanceRow({ state, setMode, loadError }: AppearanceControl) {
  const action = useAction();

  return (
    <>
      <div className="setting-row">
        <div className="setting-copy">
          <span className="setting-title" id="appearance-label">
            主题外观
          </span>
          <span className="setting-desc">默认跟随 Windows 系统深浅色设置</span>
        </div>
        <Segmented
          role="radiogroup"
          aria-labelledby="appearance-label"
          value={state?.mode}
          onKeyDown={(event) => {
            const keys = ['ArrowLeft', 'ArrowUp', 'ArrowRight', 'ArrowDown'];
            const arrow = keys.indexOf(event.key);
            if (arrow < 0 || !state || action.busy) return;
            const radios = event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]');
            const current = [...radios].findIndex((radio) => radio === event.target);
            if (current < 0) return;
            event.preventDefault();
            const direction = arrow < 2 ? -1 : 1;
            const next = radios[(current + direction + radios.length) % radios.length];
            next.focus();
            next.click();
          }}
        >
          {appearanceModes.map(([mode, label]) => (
            <button
              key={mode}
              type="button"
              role="radio"
              data-segment={mode}
              aria-checked={state?.mode === mode}
              aria-disabled={action.busy || undefined}
              className="seg-btn"
              disabled={!state}
              tabIndex={state?.mode === mode ? 0 : -1}
              onClick={() => {
                if (state?.mode === mode || action.busy) return;
                void action.run(async () => {
                  await setMode(mode, true);
                }, { success: 'silent', failure: 'toast' });
              }}
            >
              {label}
            </button>
          ))}
        </Segmented>
      </div>
      {loadError && (
        <div style={{ padding: '0 18px 14px' }}>
          <StatusNotice error={loadError} />
        </div>
      )}
    </>
  );
}
