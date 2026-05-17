import type { ApiEnvelope, ApiKey, Dashboard, LoginResponse, PaginatedResponse, Plan, Profile } from "@/types/api";

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
  keys: (token: string) => request<ApiKey[]>("/api/user/keys", { token }),
  createKey: (token: string, name: string) => request<ApiKey>("/api/user/keys", { method: "POST", token, body: { name } }),
  deleteKey: (token: string, keyID: string) => request<void>(`/api/user/keys/${keyID}`, { method: "DELETE", token }),
  usage: (token: string) => request<Record<string, unknown>>("/api/user/usage", { token }),
  usageLogs: (token: string, page = 1, limit = 20) =>
    request<Record<string, unknown>>(`/api/user/usage/logs?page=${page}&limit=${limit}`, { token }),
  plans: (token: string) => request<Plan[]>("/api/user/plans", { token }),
  subscribe: (token: string, planID: string) =>
    request<void>(`/api/user/subscription/${planID}`, { method: "POST", token }),
  redeem: (token: string, code: string) => request<{ value: number }>("/api/user/redeem", { method: "POST", token, body: { code } }),
};

export const adminApi = {
  login: (body: { username: string; password: string }) =>
    request<LoginResponse>("/api/admin/login", { method: "POST", body }),
  initStatus: () => request<{ initialized: boolean }>("/api/admin/init-status"),
  setup: (body: { username: string; password: string }) => request<void>("/api/admin/setup", { method: "POST", body }),
  dashboard: (token: string) => request<Dashboard>("/api/admin/dashboard", { token }),
  channels: (token: string) => request<PaginatedResponse<unknown>>("/api/admin/channels", { token }),
  accounts: (token: string) => request<PaginatedResponse<unknown>>("/api/admin/accounts", { token }),
  users: (token: string) => request<PaginatedResponse<unknown>>("/api/admin/users", { token }),
  tokens: (token: string) => request<PaginatedResponse<unknown>>("/api/admin/tokens", { token }),
  plans: (token: string) => request<PaginatedResponse<unknown>>("/api/admin/plans", { token }),
  logs: (token: string) => request<PaginatedResponse<unknown>>("/api/admin/logs", { token }),
};
