export type ApiEnvelope<T> = {
  code: number;
  message: string;
  data?: T;
};

export type LoginResponse = {
  access_token: string;
  refresh_token: string;
  access_expires_at: number;
  refresh_expires_at: number;
};

export type Profile = {
  id: string;
  email: string;
  username: string;
  status: string;
  created_at: string;
};

export type User = {
  id: string;
  email: string;
  username: string;
  status: "active" | "disabled";
  created_at: string;
  updated_at?: string;
};

export type AdminSettings = {
  log_retention_days: number;
  redeem_code_retention_days: number;
  background: "aurora" | "silk" | "mesh" | "topography" | "noir" | "custom";
  public_base_url?: string;
  wallpaper_url?: string;
};

export type PublicSettings = {
  background: AdminSettings["background"];
  public_base_url?: string;
  wallpaper_url?: string;
};

export type RedeemCode = {
  id: string;
  code: string;
  plan_id: string;
  used_by?: string;
  used_at?: string;
  max_uses: number;
  used_count: number;
  status: "active" | "used";
  created_at: string;
  updated_at?: string;
};

export type ApiKey = {
  id: string;
  user_id?: string;
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
  user_id?: string;
  username?: string;
  user_email?: string;
  client_ip?: string;
  channel_id?: string;
  channel_name?: string;
  account_id?: string;
  account_name?: string;
  account_cred_type?: string;
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


export type Subscription = {
  plan_id: string;
  plan_name: string;
  plan_type: string;
  token_quota: number;
  used_quota: number;
  remaining_quota: number;
  windows: SubscriptionWindow[];
  starts_at: string;
  expires_at: string;
  status: string;
};

export type SubscriptionWindow = {
  type: "hour" | "week" | "month";
  limit: number;
  used: number;
  remaining: number;
  reset_at: string;
};

export type Plan = {
  id: string;
  name: string;
  type: string;
  policy_id?: string;
  token_quota: number;
  duration_days: number;
  enabled: boolean;
};

export type ChannelModelSync = {
  channel: Channel;
  models: string[];
  count: number;
};

export type Channel = {
  id: string;
  name: string;
  type: string;
  group: string;
  endpoint: string;
  enabled: boolean;
  models: string;
  model_aliases: string;
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
  endpoint?: string;
  weight: number;
  enabled: boolean;
  cooldown_until?: string;
  token_expiry?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at?: string;
};

export type OAuthAuthURL = {
  auth_url: string;
  state: string;
  redirect_uri: string;
  mode: "browser" | "device" | "manual_callback";
  user_code?: string;
  expires_at: string;
};

export type OAuthStatus = {
  state: string;
  provider: "openai" | "gemini" | "anthropic" | "antigravity";
  channel_id: string;
  status: "pending" | "completed" | "error" | "bound";
  ready_to_bind: boolean;
  error?: string;
  created_at: string;
  completed_at?: string;
  bound_account_id?: string;
};

export type AuditLogEntry = {
  id: number;
  user: string;
  action: string;
  resource: string;
  resource_id: string;
  old_value?: string;
  new_value?: string;
  ip_address?: string;
  created_at: string;
};

export type RelayNode = {
  id: string;
  name: string;
  base_url: string;
  region: string;
  egress_ip: string;
  weight: number;
  max_concurrency: number;
  status: "active" | "draining" | "disabled";
  health_status: "healthy" | "unhealthy" | "offline";
  current_concurrency: number;
  avg_latency_ms: number;
  error_rate: string;
  last_heartbeat_at?: string;
  created_at: string;
  updated_at?: string;
};

export type NodeChannel = {
  id: string;
  relay_node_id: string;
  channel_id: string;
  weight: number;
  enabled: boolean;
  created_at: string;
  updated_at?: string;
};

export type AccessPolicy = {
  id: string;
  allowed_models: string;
  max_concurrency: number;
  hourly_limit: number;
  weekly_limit: number;
  monthly_limit: number;
  enabled: boolean;
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
