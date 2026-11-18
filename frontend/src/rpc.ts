declare global {
  interface Window {
    wails?: { Call: { ByName: (name: string, ...args: unknown[]) => Promise<unknown> } };
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

export type Counts = {
  attempts: number;
  renames: number;
  kept: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  unknownUsage: number;
};

export type Daily = Counts & {
  date: string;
  since?: string;
  byAgent: (Counts & { agent: string })[] | null;
};

export type Settings = {
  provider: 'api' | 'codex';
  codexModel: string;
  fast: boolean;
  apiBaseUrl: string;
  apiModel: string;
  hasApiKey: boolean;
  fallbackEnabled: boolean;
  maxTitleLength: number;
};

export type Plugin = {
  id: string;
  name: string;
  version: string;
  enabled: boolean;
  status: string;
  kind: string;
  detail?: string;
};

export type Attempt = {
  provider: string;
  model: string;
  status: string;
  startedAt?: string;
  requestedTier?: string;
  actualTier?: string;
  inputTokens?: number;
  outputTokens?: number;
  cachedTokens?: number;
  usageKnown: boolean;
};

export type RecordItem = {
  id: string;
  agent: string;
  sessionId: string;
  title: string;
  status: string;
  updatedAt: string;
  syncStatus?: string;
  syncError?: string;
  attempts: Attempt[] | null;
};

export type Preferences = { startup: boolean; startupAvailable: boolean };

const agents: Record<string, string> = { codex: 'Codex', claude: 'Claude', pi: 'Pi', orca: 'Orca' };
export const agentName = (id: string) => agents[id] ?? id;
export const num = (value: number) => new Intl.NumberFormat('zh-CN').format(value);
export const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false });

export function localDate() {
  const now = new Date();
  const part = (value: number) => String(value).padStart(2, '0');
  return `${now.getFullYear()}-${part(now.getMonth() + 1)}-${part(now.getDate())}`;
}
