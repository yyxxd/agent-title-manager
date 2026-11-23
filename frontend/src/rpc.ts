declare global {
  interface Window {
    wails?: {
      Call: { ByName: (name: string, ...args: unknown[]) => Promise<unknown> };
      Events?: { On: (name: string, callback: (event: { data: unknown }) => void) => () => void };
    };
  }
}

export async function rpc<T>(method: string, params: object = {}): Promise<T> {
  if (!window.wails?.Call) throw new Error('桌面服务未连接，请从 Windows 应用打开。');
  return window.wails.Call.ByName(
    'github.com/yyxxd/agent-title-manager/internal/desktop.Service.Call',
    method,
    JSON.stringify(params),
  ) as Promise<T>;
}

export function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  if (error && typeof error === 'object' && 'message' in error) return String(error.message);
  return '操作失败，请重试。';
}

export type TokenUsage = {
  inputTokens?: number | null;
  outputTokens?: number | null;
  cachedTokens?: number | null;
};

export type UsageSummary = TokenUsage & {
  attempts: number;
  testAttempts?: number;
  inputKnown?: number;
  outputKnown?: number;
  cachedKnown?: number;
  unknownUsage: number;
};

export type Counts = UsageSummary & {
  renames: number;
  kept: number;
  modelKept?: number;
};

export type ModelUsage = UsageSummary & {
  provider: string;
  model: string;
};

export type Daily = Counts & {
  date: string;
  since?: string;
  byAgent: (Counts & { agent: string })[] | null;
  byModel?: ModelUsage[] | null;
  namingUsage?: UsageSummary;
};

export type ApiProtocol = 'chat' | 'responses' | 'anthropic' | 'gemini';

export type ApiTarget = { apiProtocol: ApiProtocol; apiBaseUrl: string };
export type ApiEndpoints = { generationUrl: string; modelsUrl: string };
export type ApiModel = { id: string; name?: string };
export type NativeProvider = 'codex' | 'claude';
export type SubscriptionModel = ApiModel & { resolvedModel?: string; efforts: string[]; defaultEffort?: string };
export type SubscriptionCatalog = { items: SubscriptionModel[]; notice?: string; authRequired?: boolean };

export type Settings = ApiTarget & {
  provider: 'api' | NativeProvider;
  codexModel: string;
  codexEffort: string;
  claudeModel: string;
  claudeEffort: string;
  fast: boolean;
  apiModel: string;
  hasApiKey: boolean;
  fallbackEnabled: boolean;
  maxTitleLength: number;
};

export type Plugin = {
  id: string;
  name: string;
  version: string;
  availableVersion?: string;
  enabled: boolean;
  status: string;
  kind: 'builtin' | 'community' | 'integration';
  detail?: string;
  entryPath?: string;
};

export type AutoUpdatedPlugin = {
  id: string;
  name: string;
  fromVersion: string;
  toVersion: string;
  enabled: boolean;
};

export type AutoUpdateFailedPlugin = {
  id: string;
  name: string;
  currentVersion: string;
  targetVersion: string;
  error: string;
};

export type PluginAutoUpdateResult = {
  upgraded?: AutoUpdatedPlugin[];
  failed?: AutoUpdateFailedPlugin[];
};

export type PluginListResponse = {
  items: Plugin[];
  autoUpdate?: PluginAutoUpdateResult;
};

export type Attempt = TokenUsage & {
  provider: string;
  model: string;
  actualModel?: string;
  source?: string;
  status: string;
  startedAt?: string;
  requestedTier?: string;
  actualTier?: string;
};

export type RecordItem = {
  reason?: string;
  orcaTitleOrigin?: string;
  canAdopt?: boolean;
  id: string;
  agent: string;
  sessionId: string;
  title: string;
  status: string;
  updatedAt: string;
  syncStatus?: string;
  syncError?: string;
  failure?: { code: string; length?: number; limit?: number };
  attempts: Attempt[] | null;
  sessionKey?: string;
  kind?: string;
  locked?: boolean;
  protected?: boolean;
  changedAt?: string;
  changedFound?: boolean;
  canRetry?: boolean;
  identityName?: string;
  checkedAt?: string;
  suggestedTitle?: string;
  changes?: { id: string; from: string; title: string; source: 'auto' | 'manual' | 'observed'; at?: string; found?: boolean; side?: string }[];
};

export type RecordCursor = { updated: number; rowId: number };
export type RecordCounts = { total: number; unread: number; attention: number; failed: number; syncFailed: number; protected: number };
export type RecordList = { items: RecordItem[]; counts?: RecordCounts; next?: RecordCursor; testAttempts?: Attempt[] };

export function recordCounts(items: RecordItem[]): RecordCounts {
  const attention = items.filter(needsAttention).length;
  return {
    total: items.length,
    unread: attention,
    attention,
    failed: items.filter((item) => item.status === 'failed').length,
    syncFailed: items.filter((item) => item.syncStatus === 'failed').length,
    protected: items.filter(canAdoptTitle).length,
  };
}

export function canAdoptTitle(item: RecordItem) {
  return item.syncStatus === 'protected' && (item.canAdopt ?? true);
}

export function needsAttention(item: RecordItem) {
  return item.status === 'failed' || item.syncStatus === 'failed' || canAdoptTitle(item);
}

export type Preferences = { startup: boolean; startupAvailable: boolean };

const agents: Record<string, string> = {
  codex: 'Codex',
  claude: 'Claude Code',
  antigravity: 'Antigravity',
  pi: 'Pi',
  orca: 'Orca',
};
export const agentName = (id: string) => (Object.hasOwn(agents, id) ? agents[id] : id);
export const providerName = (id: string) => (id === 'api' ? '自定义 API' : id === 'claude' ? 'Claude Code' : agentName(id));
export const num = (value: number) => new Intl.NumberFormat('zh-CN').format(value);
export const token = (value?: number | null, missing = '未回报') => (typeof value === 'number' ? num(value) : missing);
export const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false });

export function localDate() {
  const now = new Date();
  const part = (value: number) => String(value).padStart(2, '0');
  return `${now.getFullYear()}-${part(now.getMonth() + 1)}-${part(now.getDate())}`;
}
