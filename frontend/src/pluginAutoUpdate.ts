import { useEffect, useRef, useSyncExternalStore } from 'react';
import type { PluginAutoUpdateResult } from './rpc';
import { toast } from './toast';

type Snapshot = {
  result?: PluginAutoUpdateResult;
  signature?: string;
  upgradedDismissed: boolean;
  failedDismissed: boolean;
};

const listeners = new Set<() => void>();
let snapshot: Snapshot = {
  upgradedDismissed: false,
  failedDismissed: false,
};
const notifiedSignatures = new Set<string>();

function emit() {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

const getSnapshot = () => snapshot;

export function formatPluginVersionRange(from?: string, to?: string): string {
  const fromClean = from?.trim() ?? '';
  const toClean = to?.trim() ?? '';
  if (fromClean && toClean) return `v${fromClean} → v${toClean}`;
  if (toClean) return `v${toClean}`;
  if (fromClean) return `v${fromClean}`;
  return '';
}

function normalizeResult(result?: PluginAutoUpdateResult): PluginAutoUpdateResult | undefined {
  if (!result) return undefined;
  const upgraded = (result.upgraded ?? []).filter((item) => Boolean(item));
  const failed = (result.failed ?? []).filter((item) => Boolean(item));
  if (upgraded.length === 0 && failed.length === 0) return undefined;
  return {
    upgraded: upgraded.length > 0 ? upgraded : undefined,
    failed: failed.length > 0 ? failed : undefined,
  };
}

function buildSignature(result: PluginAutoUpdateResult): string {
  const upgraded = (result.upgraded ?? []).map((item) => [
    item.id,
    item.name,
    item.fromVersion,
    item.toVersion,
    item.enabled,
  ]);
  const failed = (result.failed ?? []).map((item) => [
    item.id,
    item.name,
    item.currentVersion,
    item.targetVersion,
    item.error,
  ]);
  return JSON.stringify({ upgraded, failed });
}

function notifyOnce(result: PluginAutoUpdateResult, signature: string) {
  if (notifiedSignatures.has(signature)) return;
  notifiedSignatures.add(signature);

  const upgraded = result.upgraded ?? [];
  if (upgraded.length > 0) {
    const summary = upgraded
      .map((item) => {
        const label = item.name || item.id || '内置插件';
        const ver = formatPluginVersionRange(item.fromVersion, item.toVersion);
        return ver ? `${label} ${ver}` : label;
      })
      .join('、');
    const restartHint = upgraded.some((item) => item.enabled)
      ? '，已运行的 Agent 需重启会话生效'
      : '';
    toast.success(`已自动更新内置插件：${summary}${restartHint}`);
  }

  const failed = result.failed ?? [];
  if (failed.length > 0) {
    const names = failed.map((item) => item.name || item.id || '内置插件').join('、');
    toast.error(`内置插件自动更新失败：${names}，可在 Agent 页面查看原因并手动重试`);
  }
}

export function syncPluginAutoUpdate(raw?: PluginAutoUpdateResult) {
  const normalized = normalizeResult(raw);
  if (!normalized) return;

  const signature = buildSignature(normalized);
  if (snapshot.signature !== signature) {
    snapshot = {
      result: normalized,
      signature,
      upgradedDismissed: false,
      failedDismissed: false,
    };
    emit();
  }

  notifyOnce(normalized, signature);
}

export function dismissUpgradedBanner() {
  if (snapshot.upgradedDismissed) return;
  snapshot = { ...snapshot, upgradedDismissed: true };
  emit();
}

export function dismissFailedBanner() {
  if (snapshot.failedDismissed) return;
  snapshot = { ...snapshot, failedDismissed: true };
  emit();
}

export function usePluginAutoUpdate() {
  return useSyncExternalStore(subscribe, getSnapshot);
}

export function usePluginAutoUpdateEvents(onUpdated: () => void) {
  const onUpdatedRef = useRef(onUpdated);
  onUpdatedRef.current = onUpdated;

  useEffect(() => {
    const events = window.wails?.Events;
    if (!events) return;
    return events.On('plugins:auto-update', ({ data }) => {
      syncPluginAutoUpdate(data as PluginAutoUpdateResult | undefined);
      onUpdatedRef.current();
    });
  }, []);
}
