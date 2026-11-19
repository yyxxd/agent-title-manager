import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { rpc, type NativeProvider, type Settings, type SubscriptionCatalog, type SubscriptionModel } from './rpc';
import { InlineActionError, useLoad } from './ui';

export type NativeChoice = { model: string; effort: string };

export const nativeChoice = (settings: Settings, provider: NativeProvider): NativeChoice =>
  provider === 'codex'
    ? { model: settings.codexModel, effort: settings.codexEffort }
    : { model: settings.claudeModel, effort: settings.claudeEffort };

export const effortFor = (model: SubscriptionModel, current: string) =>
  model.efforts.includes(current) ? current : model.defaultEffort ?? '';

function withNativeChoice(settings: Settings, provider: NativeProvider, choice: NativeChoice): Settings {
  return provider === 'codex'
    ? { ...settings, codexModel: choice.model, codexEffort: choice.effort }
    : { ...settings, claudeModel: choice.model, claudeEffort: choice.effort };
}

export function useSubscription(provider: Settings['provider']) {
  const [revision, setRevision] = useState(0);
  const force = useRef(false);
  const { value, error, loading } = useLoad(() => {
    if (provider === 'api') return Promise.resolve(undefined);
    const refresh = force.current;
    force.current = false;
    return rpc<SubscriptionCatalog>('providers.subscriptionModels', { provider, refresh });
  }, `${provider}:${revision}`);
  return {
    value,
    error,
    loading: provider !== 'api' && loading,
    refresh: () => {
      force.current = true;
      setRevision((old) => old + 1);
    },
  };
}

export function useSubscriptionDraft(settings: Settings, setSettings: Dispatch<SetStateAction<Settings>>) {
  const provider = settings.provider;
  const catalog = useSubscription(provider);
  const choice = provider === 'api' ? undefined : nativeChoice(settings, provider);
  const selected = catalog.value?.items.find(({ id }) => id === choice?.model);

  useEffect(() => {
    if (provider === 'api' || !selected) return;
    setSettings((draft) => {
      const current = nativeChoice(draft, provider);
      if (draft.provider !== provider || current.model !== selected.id || !current.effort) return draft;
      const effort = effortFor(selected, current.effort);
      return effort === current.effort ? draft : withNativeChoice(draft, provider, { ...current, effort });
    });
  }, [provider, selected, setSettings]);

  function editNative(source: NativeProvider, next: NativeChoice) {
    setSettings((old) => (old.provider !== source ? old : withNativeChoice(old, source, next)));
  }

  function nativeDraft(testing = false) {
    if (provider === 'api' || !choice) return settings;
    if (catalog.value?.authRequired) {
      if (testing) throw new InlineActionError(catalog.value.notice ?? '未登录');
      return settings;
    }
    if (!choice.model) throw new InlineActionError('请先选择命名模型。');
    if (catalog.value && !selected) throw new InlineActionError('当前模型已不在 CLI 目录中，请重新选择。');
    if (!selected || !choice.effort) return settings;
    return withNativeChoice(settings, provider, { ...choice, effort: effortFor(selected, choice.effort) });
  }

  return { catalog, selected, editNative, nativeDraft };
}
