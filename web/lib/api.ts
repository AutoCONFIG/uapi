import type {
  Account,
  AccessPolicy,
  ApiEnvelope,
  ApiKey,
  AuditLogEntry,
  Channel,
  Dashboard,
  LoginResponse,
  NodeAccount,
  OAuthAuthURL,
  OAuthStatus,
  RelayNode,
  PaginatedResponse,
  Plan,
  Profile,
  UsageLogItem,
  UsageLogs,
  UsageSummary,
  User,
} from "@/types/api";

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "";

type RequestOptions = Omit<RequestInit, "body"> & {
  token?: string;
  body?: unknown;
};

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
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
  const payload = (await response.json()) as ApiEnvelope<T>;
  if (!response.ok || payload.code !== 0) {
    throw new Error(payload.message || "Request failed");
  }
  return payload.data as T;
}

export const userApi = {
  register: (body: { email: string; username: string; password: string }) =>
    request<LoginResponse>("/api/user/register", { method: "POST", body }),
  login: (body: { email: string; password: string }) =>
    request<LoginResponse>("/api/user/login", { method: "POST", body }),
  refresh: (token: string) => request<LoginResponse>("/api/user/refresh", { method: "POST", body: { token } }),
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
  plans: (token: string) => request<Plan[]>("/api/user/plans", { token }),
  subscribe: (token: string, planID: string) =>
    request<void>(`/api/user/subscription/${planID}`, { method: "POST", token }),
  redeem: (token: string, code: string) => request<{ value: number }>("/api/user/redeem", { method: "POST", token, body: { code } }),
};

export const adminApi = {
  login: (body: { email: string; password: string }) =>
    request<LoginResponse>("/api/admin/login", { method: "POST", body }),
  initStatus: () => request<{ initialized: boolean }>("/api/admin/init-status"),
  setup: (body: { email: string; password: string }) => request<void>("/api/admin/setup", { method: "POST", body }),
  dashboard: (token: string) => request<Dashboard>("/api/admin/dashboard", { token }),
  accessPolicies: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<AccessPolicy>>(`/api/admin/access-policies?page=${page}&limit=${limit}`, { token }),
  createAccessPolicy: (token: string, body: { name: string; allowed_models?: string; max_concurrency?: number; hourly_limit?: number; weekly_limit?: number; monthly_limit?: number; enabled?: boolean }) =>
    request<AccessPolicy>("/api/admin/access-policies", { method: "POST", token, body }),
  updateAccessPolicy: (token: string, id: string, body: Partial<{ name: string; allowed_models: string; max_concurrency: number; hourly_limit: number; weekly_limit: number; monthly_limit: number; enabled: boolean }>) =>
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
  nodeAccounts: (token: string, page = 1, limit = 100) =>
    request<PaginatedResponse<NodeAccount>>(`/api/admin/node-accounts?page=${page}&limit=${limit}`, { token }),
  createNodeAccount: (token: string, body: { relay_node_id: string; account_id: string; weight?: number; enabled?: boolean }) =>
    request<NodeAccount>("/api/admin/node-accounts", { method: "POST", token, body }),
  updateNodeAccount: (token: string, id: string, body: Partial<{ relay_node_id: string; account_id: string; weight: number; enabled: boolean }>) =>
    request<NodeAccount>(`/api/admin/node-accounts?id=${id}`, { method: "PUT", token, body }),
  deleteNodeAccount: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/node-accounts?id=${id}`, { method: "DELETE", token }),
  channels: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<Channel>>(`/api/admin/channels?page=${page}&limit=${limit}`, { token }),
  createChannel: (token: string, body: {
    name: string;
    type: string;
    group?: string;
    endpoint: string;
    models?: string;
    priority?: number;
    api_format?: string;
    force_stream?: boolean;
    affinity_ttl?: number;
  }) => request<Channel>("/api/admin/channels", { method: "POST", token, body }),
  updateChannel: (token: string, id: string, body: Partial<Pick<Channel, "name" | "type" | "group" | "endpoint" | "models" | "priority" | "api_format" | "force_stream" | "affinity_ttl" | "enabled">>) =>
    request<Channel>(`/api/admin/channels?id=${id}`, { method: "PUT", token, body }),
  deleteChannel: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/channels?id=${id}`, { method: "DELETE", token }),
  startChannelOAuth: (token: string, body: {
    channel_id: string;
    provider?: string;
    account_name?: string;
    client_id?: string;
    client_secret?: string;
    token_url?: string;
    mode?: string;
  }) => request<OAuthAuthURL>("/api/admin/channels/oauth/auth-url", { method: "POST", token, body }),
  completeChannelOAuth: (token: string, body: { state?: string; callback_url?: string; code?: string; oauth_json?: string }) =>
    request<OAuthStatus>("/api/admin/channels/oauth/complete", { method: "POST", token, body }),
  channelOAuthStatus: (token: string, state: string) =>
    request<OAuthStatus>(`/api/admin/channels/oauth/status?state=${encodeURIComponent(state)}`, { token }),
  bindChannelOAuth: (token: string, body: { state: string; account_name?: string; weight?: number; enabled?: boolean }) =>
    request<Account>("/api/admin/channels/oauth/bind", { method: "POST", token, body }),
  accounts: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<Account>>(`/api/admin/accounts?page=${page}&limit=${limit}`, { token }),
  createAccount: (token: string, body: { channel_id: string; name: string; credentials: string; weight: number; enabled: boolean }) =>
    request<Account>("/api/admin/accounts", { method: "POST", token, body }),
  updateAccount: (token: string, id: string, body: Partial<{ channel_id: string; name: string; credentials: string; weight: number; enabled: boolean; cooldown_until: string }>) =>
    request<Account>(`/api/admin/accounts?id=${id}`, { method: "PUT", token, body }),
  deleteAccount: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/accounts?id=${id}`, { method: "DELETE", token }),
  tokens: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<ApiKey>>(`/api/admin/tokens?page=${page}&limit=${limit}`, { token }),
  createToken: (token: string, body: { name: string; key: string; enabled?: boolean; ip_whitelist?: string; models?: string; permissions?: string; policy_id?: string }) =>
    request<ApiKey>("/api/admin/tokens", { method: "POST", token, body }),
  updateToken: (token: string, id: string, body: Partial<{ name: string; ip_whitelist: string; models: string; permissions: string; unlimited: boolean; policy_id: string }>) =>
    request<ApiKey>(`/api/admin/tokens?id=${id}`, { method: "PUT", token, body }),
  deleteToken: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/tokens?id=${id}`, { method: "DELETE", token }),
  plans: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<Plan>>(`/api/admin/plans?page=${page}&limit=${limit}`, { token }),
  createPlan: (token: string, body: { name: string; type: string; limits?: string; model_ratios?: string; completion_ratio?: string; token_quota?: number; enabled?: boolean }) =>
    request<Plan>("/api/admin/plans", { method: "POST", token, body }),
  updatePlan: (token: string, id: string, body: Partial<{ name: string; type: string; limits: string; model_ratios: string; completion_ratio: string; token_quota: number; enabled: boolean }>) =>
    request<Plan>(`/api/admin/plans?id=${id}`, { method: "PUT", token, body }),
  deletePlan: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/plans?id=${id}`, { method: "DELETE", token }),
  logs: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<UsageLogItem>>(`/api/admin/logs?page=${page}&limit=${limit}`, { token }),
  auditLogs: (token: string, page = 1, limit = 20) =>
    request<PaginatedResponse<{ id: number; action: string; target_type: string; target_id: string; actor: string; created_at: string }>>(`/api/admin/audit-logs?page=${page}&limit=${limit}`, { token }),
  users: (token: string, page = 1, limit = 20, status?: "active" | "disabled") => {
    const query = new URLSearchParams({ page: String(page), limit: String(limit) });
    if (status) query.set("status", status);
    return request<PaginatedResponse<User>>(`/api/admin/users?${query.toString()}`, { token });
  },
  updateUser: (token: string, id: string, body: Partial<Pick<User, "status" | "balance">> & { new_password?: string }) =>
    request<User>(`/api/admin/users?id=${id}`, { method: "PUT", token, body }),
  deleteUser: (token: string, id: string) =>
    request<{ deleted: boolean }>(`/api/admin/users?id=${id}`, { method: "DELETE", token }),
};
