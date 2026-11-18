import { useCallback, useEffect, useRef, useState } from 'react';
import { errorText, rpc } from './rpc';
import { Status, useAction } from './ui';

const modes = [['system', '跟随系统'], ['light', '浅色'], ['dark', '深色']] as const;
type Mode = (typeof modes)[number][0];
type State = { mode: Mode; theme: 'light' | 'dark'; revision: number };

export function useAppearance() {
  const [state, setState] = useState<State>();
  const [loadError, setLoadError] = useState<string>();
  const latest = useRef(0);
  const accept = useCallback((next: State) => {
    if (next.revision < latest.current) return;
    latest.current = next.revision;
    document.documentElement.dataset.theme = next.theme;
    setState(next);
    setLoadError(undefined);
  }, []);
  useEffect(() => {
    const stop = window.wails?.Events?.On('desktop:appearance', ({ data }) => accept(data as State));
    void rpc<State>('desktop.appearance.get').then(accept, (error) => setLoadError(errorText(error)));
    return stop;
  }, [accept]);
  return { state, accept, loadError };
}

export type AppearanceControl = ReturnType<typeof useAppearance>;

export function AppearanceRow({ state, accept, loadError }: AppearanceControl) {
  const action = useAction();
  return <>
    <fieldset className="setting-row appearance-row" disabled={!state} aria-busy={action.busy}>
      <span id="appearance-label">外观</span>
      <div className="segmented" role="radiogroup" aria-labelledby="appearance-label">
        {modes.map(([mode, label]) => <label key={mode}>
          <input type="radio" name="appearance" value={mode} checked={state?.mode === mode}
            onChange={() => { if (!action.busy) void action.run(async () => accept(await rpc<State>('desktop.appearance.set', { mode }))); }} />
          <span>{label}</span>
        </label>)}
      </div>
    </fieldset>
    <Status error={loadError ?? action.error} />
  </>;
}
