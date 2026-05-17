export type ApiEnvelope<T> = {
  code: number;
  message: string;
  data?: T;
};

export type LoginResponse = {
  token: string;
  expires_at?: number;
};

export type Profile = {
  id: string;
  email: string;
  username: string;
  status: string;
  balance: number;
  created_at: string;
};

export type User = {
  id: string;
  email: string;
  username: string;
  status: "active" | "disabled";
  balance: number;
  created_at: string;
  updated_at?: string;
};

export type ApiKey = {
  id: string;
  name: string;
  key: string;
  enabled: boolean;
  created_at: string;
};

export type Plan = {
  id: string;
  name: string;
  type: string;
  token_quota: number;
  enabled: boolean;
};

export type Channel = {
  id: string;
  name: string;
  type: string;
  endpoint: string;
  enabled: boolean;
  models: string;
  priority: number;
  api_format: string;
  force_stream: boolean;
  affinity_ttl: number;
  created_at: string;
  updated_at?: string;
};

export type Account = {
  id: string;
  channel_id: string;
  name: string;
  cred_type: "api_key" | "oauth_token";
  weight: number;
  enabled: boolean;
  cooldown_until?: string;
  token_expiry?: string;
  created_at: string;
  updated_at?: string;
};

export type Dashboard = {
  total_requests: number;
  total_tokens: number;
  active_channels: number;
  active_accounts: number;
};

export type PaginatedResponse<T> = {
  total: number;
  page: number;
  limit: number;
  items: T[];
};
