import type {
  Account,
  AdminSettings,
  AvailableModels,
  AccessPolicy,
  ApiEnvelope,
  ApiKey,
  AuditLogEntry,
  Channel,
  ChannelCatalog,
  ChannelModelSync,
  Dashboard,
  LoginResponse,
  NodeChannel,
  OAuthAuthURL,
  OAuthStatus,
  RedeemCode,
  RelayNode,
  PaginatedResponse,
  Plan,
  Profile,
  PublicPlan,
  PublicSettings,
  Subscription,
  UsageLogItem,
  UsageLogs,
  UsageSummary,
  User,
} from "@/types/api";

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "";

type RequestOptions = Omit<RequestInit, "body"> & {
  token?: string;
  body?: unknown;
  skipAuthRefresh?: boolean;
};

type AuthScope = "admin" | "user";

function storeAuth(scope: AuthScope, auth: LoginResponse) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(`uapi.${scope}.token`, auth.access_token);
  window.localStorage.setItem(`uapi.${scope}.refresh_token`, auth.refresh_token);
  window.localStorage.setItem(`uapi.${scope}.access_expires_at`, String(auth.access_expires_at));
  window.localStorage.setItem(`uapi.${scope}.refresh_expires_at`, String(auth.refresh_expires_at));
}

function clearAuth(scope: AuthScope) {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(`uapi.${scope}.token`);
  window.localStorage.removeItem(`uapi.${scope}.refresh_token`);
  window.localStorage.removeItem(`uapi.${scope}.access_expires_at`);
  window.localStorage.removeItem(`uapi.${scope}.refresh_expires_at`);
}

async function rawRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set("Content-Type", "application/json");
  if (options.token) {
    headers.set("Authorization", `Bearer ${options.token}`);
  }

  const response = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  const contentType = response.headers.get("content-type") || "";
  if (!contentType.toLowerCase().includes("application/json")) {
    const body = await response.text().catch(() => "");
    const looksLikeHTML = body.trimStart().startsWith("<");
    const message = looksLikeHTML
      ? `API 返回了 HTML 页面，请检查 ${path} 是否被反代到 Gateway 服务。`
      : `API 返回了非 JSON 响应：${body.slice(0, 160) || response.statusText}`;
    const error = new Error(message);
    (error as Error & { status?: number }).status = response.status;
    throw error;
  }
  const payload = (await response.json()) as ApiEnvelope<T>;
  if (!response.ok || payload.code !== 0) {
    const error = new Error(payload.message || "Request failed");
    (error as Error & { status?: number }).status = response.status;
    throw error;
  }
  return payload.data as T;
}

async function refreshStoredAuth(scope: AuthScope): Promise<string | null> {
  if (typeof window === "undefined") return null;
  const refreshToken = window.localStorage.getItem(`uapi.${scope}.refresh_token`);
  if (!refreshToken) return null;
  try {
    const auth = await rawRequest<LoginResponse>(`/api/${scope}/refresh`, {
      method: "POST",
      body: { refresh_token: refreshToken },
      skipAuthRefresh: true,
    });
    storeAuth(scope, auth);
    return auth.access_token;
  } catch {
    clearAuth(scope);
    return null;
  }
}

function tokenScope(token?: string): AuthScope | null {
  if (!token || typeof window === "undefined") return null;
  if (token === window.localStorage.getItem("uapi.admin.token")) return "admin";
  if (token === window.localStorage.getItem("uapi.user.token")) return "user";
  return null;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  try {
    return await rawRequest<T>(path, options);
  } catch (error) {
    const status = (error as Error & { status?: number }).status;
    const scope = tokenScope(options.token);
    if (status !== 401 || options.skipAuthRefresh || !scope) {
      throw error;
    }
    const accessToken = await refreshStoredAuth(scope);
    if (!accessToken) {
      throw error;
    }
    return rawRequest<T>(path, { ...options, token: accessToken, skipAuthRefresh: true });
  }
}

async function rawBlobRequest(path: string, options: RequestOptions = {}): Promise<Blob> {
  const headers = new Headers(options.headers);
  if (options.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  if (options.token) {
    headers.set("Authorization", `Bearer ${options.token}`);
  }

  const response = await fetch(`${API_BASE}${path}`, {
    ...options,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  });
  if (!response.ok) {
    const contentType = response.headers.get("content-type") || "";
    let message = response.statusText || "Request failed";
    if (contentType.toLowerCase().includes("application/json")) {
      const payload = (await response.json().catch(() => null)) as ApiEnvelope<unknown> | null;
      message = payload?.message || message;
    } else {
      const body = await response.text().catch(() => "");
      if (body) message = body.slice(0, 160);
    }
    const error = new Error(message);
    (error as Error & { status?: number }).status = response.status;
    throw error;
  }
  return response.blob();
}

async function requestBlob(path: string, options: RequestOptions = {}): Promise<Blob> {
  try {
    return await rawBlobRequest(path, options);
  } catch (error) {
    const status = (error as Error & { status?: number }).status;
    const scope = tokenScope(options.token);
    if (status !== 401 || options.skipAuthRefresh || !scope) {
      throw error;
    }
    const accessToken = await refreshStoredAuth(scope);
    if (!accessToken) {
      throw error;
    }
    return rawBlobRequest(path, { ...options, token: accessToken, skipAuthRefresh: true });
  }
}

async function uploadForm<T>(path: string, token: string, body: FormData): Promise<T> {
  const headers = new Headers();
  headers.set("Authorization", `Bearer ${token}`);
  const response = await fetch(`${API_BASE}${path}`, { method: "POST", headers, body });
  const payload = (await response.json()) as ApiEnvelope<T>;
  if (!response.ok || payload.code !== 0) {
    const error = new Error(payload.message || "Request failed");
    (error as Error & { status?: number }).status = response.status;
    throw error;
  }
  return payload.data as T;
}

export const authStorage = { storeAuth, clearAuth };

export const publicApi = {
  settings: () => request<PublicSettings>("/api/public/settings"),
};

export const userApi = {
  register: (body: { email: string; username: string; password: string }) =>
    request<LoginResponse>("/api/user/register", { method: "POST", body }),
  login: (body: { email: string; password: string }) =>
    request<LoginResponse>("/api/user/login", { method: "POST", body }),
  refresh: (refreshToken: string) => request<LoginResponse>("/api/user/refresh", { method: "POST", body: { refresh_token: refreshToken }, skipAuthRefresh: true }),
  profile: (token: string) => request<Profile>("/api/user/profile", { token }),
  updatePassword: (token: string, body: { old_password: string; new_password: string }) =>
    request<void>("/api/user/password", { method: "POST", token, body }),
  updateEmail: (token: string, body: { password: string; email: string }) =>
    request<void>("/api/user/email", { method: "POST", token, body }),
  keys: (token: string) => request<ApiKey[]>("/api/user/keys", { token }),
  createKey: (token: string, body: {
    name: string;
    ip_whitelist?: string;
    expires_at?: string;
    models?: string;
    permissions?: string;
  }) => request<ApiKey>("/api/user/keys", { method: "POST", token, body }),
  deleteKey: (token: string, keyID: string) => request<void>(`/api/user/keys/${keyID}`, { method: "DELETE", token }),
  usage: (token: string) => request<UsageSummary>("/api/user/usage", { token }),
  usageLogs: (token: string, page = 1, limit = 20) =>
    request<UsageLogs>(`/api/user/usage/logs?page=${page}&limit=${limit}`, { token }),
  subscription: (token: string) => request<Subscription>("/api/user/subscription", { token }),
  plans: (token: string) => request<PublicPlan[]>("/api/user/plans", { token }),
  models: (token: string) => request<AvailableModels>("/api/user/models", { token }),
  redeem: (token: string, code: string) => request<Subscription>("/api/user/redeem", { method: "POST", token, body: { code } }),
};

export const adminApi = {
  login: (body: { email: string; password: string }) =>
    request<LoginResponse>("/api/admin/login", { method: "POST", body }),
  refresh: (refreshToken: string) => request<LoginResponse>("/api/admin/refresh", { method: "POST", body: { refresh_token: refreshToken }, skipAuthRefresh: true }),
  initStatus: () => request<{ initialized: boolean }>("/api/admin/init-status"),
  setup: (body: { email: string; password: string }) => request<LoginResponse>("/api/admin/setup", { method: "POST", body }),
  dashboard: (token: string) => request<Dashboard>("/api/admin/dashboard", { token }),
  accessPolicies: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<AccessPolicy>>(`/api/admin/access-policies?page=${page}&limit=${limit}`, { token }),
  createAccessPolicy: (token: string, body: { allowed_models?: string; max_concurrency?: number; hourly_limit?: number; weekly_limit?: number; monthly_limit?: number; enabled?: boolean }) =>
    request<AccessPolicy>("/api/admin/access-policies", { method: "POST", token, body }),
  updateAccessPolicy: (token: string, id: string, body: Partial<{ allowed_models: string; max_concurrency: number; hourly_limit: number; weekly_limit: number; monthly_limit: number; enabled: boolean }>) =>
    request<AccessPolicy>(`/api/admin/access-policies?id=${id}`, { method: "PUT", token, body }),
  deleteAccessPolicy: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/access-policies?id=${id}`, { method: "DELETE", token }),
  relayNodes: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<RelayNode>>(`/api/admin/relay-nodes?page=${page}&limit=${limit}`, { token }),
  createRelayNode: (token: string, body: { name: string; base_url: string; region?: string; egress_ip?: string; weight?: number; max_concurrency?: number; status?: string; health_status?: string }) =>
    request<RelayNode>("/api/admin/relay-nodes", { method: "POST", token, body }),
  updateRelayNode: (token: string, id: string, body: Partial<{ name: string; base_url: string; region: string; egress_ip: string; weight: number; max_concurrency: number; status: string; health_status: string }>) =>
    request<RelayNode>(`/api/admin/relay-nodes?id=${id}`, { method: "PUT", token, body }),
  deleteRelayNode: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/relay-nodes?id=${id}`, { method: "DELETE", token }),
  nodeChannels: (token: string, page = 1, limit = 100) =>
    request<PaginatedResponse<NodeChannel>>(`/api/admin/node-channels?page=${page}&limit=${limit}`, { token }),
  createNodeChannel: (token: string, body: { relay_node_id: string; channel_id: string; weight?: number; enabled?: boolean }) =>
    request<NodeChannel>("/api/admin/node-channels", { method: "POST", token, body }),
  updateNodeChannel: (token: string, id: string, body: Partial<{ relay_node_id: string; channel_id: string; weight: number; enabled: boolean }>) =>
    request<NodeChannel>(`/api/admin/node-channels?id=${id}`, { method: "PUT", token, body }),
  deleteNodeChannel: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/node-channels?id=${id}`, { method: "DELETE", token }),
  channels: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<Channel>>(`/api/admin/channels?page=${page}&limit=${limit}`, { token }),
  channelCatalog: (token: string) =>
    request<ChannelCatalog>("/api/admin/channels/catalog", { token }),
  createChannel: (token: string, body: {
    name: string;
    type: string;
    group?: string;
    models?: string;
    model_aliases?: string;
    priority?: number;
    api_format?: string;
    force_stream?: boolean;
    affinity_ttl?: number;
    settings?: string;
  }) => request<Channel>("/api/admin/channels", { method: "POST", token, body }),
  updateChannel: (token: string, id: string, body: Partial<Pick<Channel, "name" | "type" | "group" | "models" | "model_aliases" | "priority" | "api_format" | "force_stream" | "affinity_ttl" | "settings" | "enabled">>) =>
    request<Channel>(`/api/admin/channels?id=${id}`, { method: "PUT", token, body }),
  deleteChannel: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/channels?id=${id}`, { method: "DELETE", token }),
  syncChannelModels: (token: string, id: string) =>
    request<ChannelModelSync>(`/api/admin/channels/models/sync?id=${id}`, { method: "POST", token }),
  startChannelOAuth: (token: string, body: {
    channel_id: string;
    provider?: string;
    account_name?: string;
    client_id?: string;
    client_secret?: string;
    token_url?: string;
    mode?: string;
  }) => request<OAuthAuthURL>("/api/admin/channels/oauth/auth-url", { method: "POST", token, body }),
  completeChannelOAuth: (token: string, body: { state?: string; callback_url?: string; code?: string; oauth_json?: string; channel_id?: string; provider?: string }) =>
    request<OAuthStatus>("/api/admin/channels/oauth/complete", { method: "POST", token, body }),
  channelOAuthStatus: (token: string, state: string) =>
    request<OAuthStatus>(`/api/admin/channels/oauth/status?state=${encodeURIComponent(state)}`, { token }),
  bindChannelOAuth: (token: string, body: { state: string; account_name?: string; weight?: number; enabled?: boolean }) =>
    request<Account>("/api/admin/channels/oauth/bind", { method: "POST", token, body }),
  accounts: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<Account>>(`/api/admin/accounts?page=${page}&limit=${limit}`, { token }),
  createAccount: (token: string, body: { channel_id: string; name: string; credentials: string; endpoint?: string; weight: number; enabled: boolean }) =>
    request<Account>("/api/admin/accounts", { method: "POST", token, body }),
  updateAccount: (token: string, id: string, body: Partial<{ channel_id: string; name: string; credentials: string; endpoint: string; weight: number; enabled: boolean; cooldown_until: string }>) =>
    request<Account>(`/api/admin/accounts?id=${id}`, { method: "PUT", token, body }),
  deleteAccount: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/accounts?id=${id}`, { method: "DELETE", token }),
  refreshAccountQuota: (token: string, accountId: string) =>
    request<Record<string, unknown>>("/api/admin/accounts/" + accountId + "/refresh-quota", { method: "POST", token }),
  refreshChannelQuota: (token: string, channelId: string) =>
    request<{ refreshed: number; errors: number; error_messages?: string[] }>("/api/admin/channels/" + channelId + "/refresh-quota", { method: "POST", token }),
  exportAccount: (token: string, body: { id: string; password: string }) =>
    request<Record<string, unknown>>("/api/admin/accounts/export", { method: "POST", token, body }),
  plans: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<Plan>>(`/api/admin/plans?page=${page}&limit=${limit}`, { token }),
  createPlan: (token: string, body: { name: string; type: string; duration_days?: number; enabled?: boolean; public?: boolean; allowed_models?: string; max_concurrency?: number; hourly_limit?: number; weekly_limit?: number; monthly_limit?: number }) =>
    request<Plan>("/api/admin/plans", { method: "POST", token, body }),
  updatePlan: (token: string, id: string, body: Partial<{ name: string; type: string; duration_days: number; enabled: boolean; public: boolean; allowed_models: string; max_concurrency: number; hourly_limit: number; weekly_limit: number; monthly_limit: number }>) =>
    request<Plan>(`/api/admin/plans?id=${id}`, { method: "PUT", token, body }),
  deletePlan: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/plans?id=${id}`, { method: "DELETE", token }),
  logs: (token: string, page = 1, limit = 20, filters: { user?: string; ip?: string; model?: string; start?: string; end?: string } = {}) => {
    const query = new URLSearchParams({ page: String(page), limit: String(limit) });
    Object.entries(filters).forEach(([key, value]) => { if (value) query.set(key, value); });
    return request<PaginatedResponse<UsageLogItem>>(`/api/admin/logs?${query.toString()}`, { token });
  },
  auditLogs: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<AuditLogEntry>>(`/api/admin/audit-logs?page=${page}&limit=${limit}`, { token }),
  settings: (token: string) => request<AdminSettings>("/api/admin/settings", { token }),
  updateSettings: (token: string, body: Partial<AdminSettings>) => request<AdminSettings>("/api/admin/settings", { method: "PUT", token, body }),
  exportSettings: (token: string, password: string) => requestBlob("/api/admin/settings/export", { method: "POST", token, body: { password } }),
  exportUsers: (token: string, password: string) => requestBlob("/api/admin/users/export", { method: "POST", token, body: { password } }),
  importSettings: (token: string, password: string, file: File) => {
    const body = new FormData();
    body.set("password", password);
    body.set("file", file);
    return uploadForm<Record<string, number>>("/api/admin/settings/import", token, body);
  },
  importUsers: (token: string, password: string, file: File) => {
    const body = new FormData();
    body.set("password", password);
    body.set("file", file);
    return uploadForm<Record<string, number>>("/api/admin/users/import", token, body);
  },
  uploadWallpaper: (token: string, file: File) => {
    const body = new FormData();
    body.set("file", file);
    return uploadForm<AdminSettings>("/api/admin/settings/wallpaper", token, body);
  },
  redeemCodes: (token: string, page = 1, limit = 20, status = "active") =>
    request<PaginatedResponse<RedeemCode>>(`/api/admin/redeem-codes?page=${page}&limit=${limit}&status=${encodeURIComponent(status)}`, { token }),
  createRedeemCode: (token: string, body: { code?: string; plan_id: string; max_uses?: number }) =>
    request<RedeemCode>("/api/admin/redeem-codes", { method: "POST", token, body }),
  deleteRedeemCode: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/redeem-codes?id=${id}`, { method: "DELETE", token }),
  users: (token: string, page = 1, limit = 20, status?: "active" | "disabled") => {
    const query = new URLSearchParams({ page: String(page), limit: String(limit) });
    if (status) query.set("status", status);
    return request<PaginatedResponse<User>>(`/api/admin/users?${query.toString()}`, { token });
  },
  updateUser: (token: string, id: string, body: Partial<Pick<User, "status">> & { new_password?: string; plan_id?: string; plan_starts_at?: string; plan_expires_at?: string }) =>
    request<User>(`/api/admin/users?id=${id}`, { method: "PUT", token, body }),
  deleteUser: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/users?id=${id}`, { method: "DELETE", token }),
};
