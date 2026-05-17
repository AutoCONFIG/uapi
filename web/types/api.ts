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
  ip_whitelist: string;
  expires_at?: string;
  models: string;
  permissions: string;
  created_at: string;
};

export type UsageModelPoint = {
  model: string;
  requests: number;
  total_tokens: number;
};

export type UsageDailyPoint = {
  date: string;
  requests: number;
  total_tokens: number;
};

export type UsageSummary = {
  total_requests: number;
  failed_requests: number;
  success_rate: number;
  total_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  by_model: UsageModelPoint[];
  daily: UsageDailyPoint[];
};

export type UsageLogItem = {
  id: number;
  created_at: string;
  model: string;
  is_stream: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  latency_ms: number;
  status_code: number;
  error_message?: string;
};

export type UsageLogs = {
  total: number;
  page: number;
  limit: number;
  logs: UsageLogItem[];
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

export type OAuthAuthURL = {
  auth_url: string;
  state: string;
  redirect_uri: string;
  expires_at: string;
};

export type OAuthStatus = {
  state: string;
  provider: "openai" | "gemini";
  channel_id: string;
  status: "pending" | "completed" | "error" | "bound";
  ready_to_bind: boolean;
  error?: string;
  created_at: string;
  completed_at?: string;
  bound_account_id?: string;
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
